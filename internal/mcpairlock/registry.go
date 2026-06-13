package mcpairlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const defaultHealthTimeout = 2 * time.Second

var (
	// ErrUnknownServer is returned when callers request a server outside the
	// trusted Airlock registry.
	ErrUnknownServer = errors.New("mcp airlock server not found")

	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(aws_secret_access_key|secret_access_key|session_token|client_secret|access_token|refresh_token|private_key|token)\s*[:=]\s*["']?[^"'\s]+`),
		regexp.MustCompile(`AKIA[0-9A-Z]{12,}`),
		regexp.MustCompile(`ASIA[0-9A-Z]{12,}`),
	}
)

// Check describes one Airlock validation result.
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ServerDefinition describes a trusted external MCP server without storing
// credentials or launcher state.
type ServerDefinition struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Vendor            string   `json:"vendor"`
	Description       string   `json:"description"`
	SourceURL         string   `json:"source_url"`
	DocsURL           string   `json:"docs_url,omitempty"`
	InstallHint       string   `json:"install_hint,omitempty"`
	Transport         string   `json:"transport"`
	Command           string   `json:"command,omitempty"`
	Args              []string `json:"args,omitempty"`
	HealthCheckArgs   []string `json:"health_check_args,omitempty"`
	VersionConstraint string   `json:"version_constraint,omitempty"`
	Trusted           bool     `json:"trusted"`
	ReadOnlyDefault   bool     `json:"read_only_default"`
	CredentialMode    string   `json:"credential_mode"`
	Capabilities      []string `json:"capabilities,omitempty"`
}

// ServerStatus is the public health/status view returned by the API and MCP
// tools. It deliberately excludes resolved executable paths and environment.
type ServerStatus struct {
	Server           ServerDefinition `json:"server"`
	Ready            bool             `json:"ready"`
	Running          bool             `json:"running"`
	Configured       bool             `json:"configured"`
	CommandAvailable bool             `json:"command_available"`
	State            string           `json:"state"`
	Summary          string           `json:"summary"`
	Checks           []Check          `json:"checks"`
	CheckedAt        string           `json:"checked_at,omitempty"`
	StartedAt        string           `json:"started_at,omitempty"`
	LastExitAt       string           `json:"last_exit_at,omitempty"`
	LastExitReason   string           `json:"last_exit_reason,omitempty"`
}

// ProbeResult is the sanitized result shape used by health-check probes.
type ProbeResult struct {
	Output   string
	Err      error
	TimedOut bool
}

// ProbeFunc lets tests replace process execution. Production uses exec.Command
// with a constrained environment and timeout.
type ProbeFunc func(ctx context.Context, command string, args []string, timeout time.Duration) ProbeResult

// Option configures a Manager.
type Option func(*Manager)

// Manager owns trusted MCP Airlock server definitions and read-only checks.
type Manager struct {
	definitions []ServerDefinition
	timeout     time.Duration
	probe       ProbeFunc
	launcher    LauncherFunc
	lifecycle   *lifecycleStore
}

// NewManager creates an Airlock registry. projectsDir is accepted for future
// per-project allowlists, but the first slice only serves built-in trusted
// definitions and optional process environment command overrides.
func NewManager(projectsDir string, opts ...Option) *Manager {
	_ = projectsDir
	m := &Manager{
		definitions: builtInDefinitions(),
		timeout:     defaultHealthTimeout,
		probe:       defaultProbe,
		launcher:    defaultLauncher,
		lifecycle:   newLifecycleStore(),
	}
	for _, opt := range opts {
		opt(m)
	}
	m.definitions = normalizeDefinitions(m.definitions)
	return m
}

// WithDefinitions replaces trusted definitions. It is intended for tests and
// future controlled registries.
func WithDefinitions(definitions []ServerDefinition) Option {
	return func(m *Manager) {
		m.definitions = copyDefinitions(definitions)
	}
}

// WithProbe replaces the health-check executor.
func WithProbe(probe ProbeFunc) Option {
	return func(m *Manager) {
		if probe != nil {
			m.probe = probe
		}
	}
}

// WithTimeout changes the health-check timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(m *Manager) {
		if timeout > 0 {
			m.timeout = timeout
		}
	}
}

// List returns trusted server definitions and passive status. It never starts
// an external MCP process.
func (m *Manager) List(_ context.Context) []ServerStatus {
	statuses := make([]ServerStatus, 0, len(m.definitions))
	for _, definition := range m.definitions {
		statuses = append(statuses, m.withLifecycleStatus(m.passiveStatus(definition)))
	}
	sort.SliceStable(statuses, func(i, j int) bool {
		return statuses[i].Server.Name < statuses[j].Server.Name
	})
	return statuses
}

// Check runs a bounded, read-only health check for one trusted server.
func (m *Manager) Check(ctx context.Context, id string) (ServerStatus, error) {
	definition, ok := m.lookup(id)
	if !ok {
		return ServerStatus{}, ErrUnknownServer
	}
	status := m.passiveStatus(definition)
	status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	if status.State != "available" {
		return m.withLifecycleStatus(status), nil
	}
	args := append([]string{}, definition.Args...)
	args = append(args, definition.HealthCheckArgs...)
	if len(args) == 0 {
		status.Ready = true
		status.State = "ready"
		status.Summary = "Command is available. No active health probe is configured for this server."
		status.Checks = append(status.Checks, Check{Name: "health_probe", Status: "warn", Message: "no version or health command is configured"})
		return m.withLifecycleStatus(status), nil
	}
	result := m.probe(ctx, definition.Command, args, m.timeout)
	if result.TimedOut {
		status.Ready = false
		status.State = "timeout"
		status.Summary = "Health check timed out before the MCP server responded."
		status.Checks = append(status.Checks, Check{Name: "health_probe", Status: "error", Message: "probe timed out"})
		return m.withLifecycleStatus(status), nil
	}
	if result.Err != nil {
		status.Ready = false
		status.State = "unhealthy"
		message := result.Err.Error()
		if output := redactOutput(result.Output); output != "" {
			message = fmt.Sprintf("%s: %s", message, output)
		}
		status.Summary = "Health check failed. Review the local command configuration before enabling this server."
		status.Checks = append(status.Checks, Check{Name: "health_probe", Status: "error", Message: message})
		return m.withLifecycleStatus(status), nil
	}
	status.Ready = true
	status.State = "ready"
	status.Summary = "Health check completed without exposing cloud credentials."
	message := "probe succeeded"
	if output := redactOutput(result.Output); output != "" {
		message = output
	}
	status.Checks = append(status.Checks, Check{Name: "health_probe", Status: "pass", Message: message})
	return m.withLifecycleStatus(status), nil
}

func (m *Manager) lookup(id string) (ServerDefinition, bool) {
	id = strings.TrimSpace(id)
	for _, definition := range m.definitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return ServerDefinition{}, false
}

func (m *Manager) passiveStatus(definition ServerDefinition) ServerStatus {
	status := ServerStatus{
		Server:  definition,
		State:   "available",
		Summary: "Command is configured and available. Run a health check before routing an agent to it.",
		Checks: []Check{
			{Name: "trusted_registry", Status: "pass", Message: "server is in IaC Studio's trusted MCP Airlock registry"},
			{Name: "credential_scope", Status: "pass", Message: "Airlock health checks do not forward cloud credentials"},
		},
	}
	if !definition.Trusted {
		status.State = "blocked"
		status.Summary = "Server is not marked trusted."
		status.Checks = append(status.Checks, Check{Name: "trusted_registry", Status: "error", Message: "definition is not trusted"})
		return status
	}
	if !definition.ReadOnlyDefault {
		status.Checks = append(status.Checks, Check{Name: "default_mode", Status: "warn", Message: "server is not read-only by default"})
	} else {
		status.Checks = append(status.Checks, Check{Name: "default_mode", Status: "pass", Message: "server starts in read-only review mode"})
	}
	if definition.Transport != "stdio" {
		status.State = "unsupported_transport"
		status.Summary = "Only stdio MCP servers are supported by this Airlock launcher."
		status.Checks = append(status.Checks, Check{Name: "transport", Status: "error", Message: "unsupported transport: " + definition.Transport})
		return status
	}
	command := strings.TrimSpace(definition.Command)
	if command == "" {
		status.Configured = false
		status.State = "not_configured"
		status.Summary = "No local command is configured for this MCP server."
		status.Checks = append(status.Checks, Check{Name: "command", Status: "warn", Message: "configure a command before launching this server"})
		return status
	}
	status.Configured = true
	if err := validateCommand(command); err != nil {
		status.State = "invalid_config"
		status.Summary = "Configured command is invalid."
		status.Checks = append(status.Checks, Check{Name: "command", Status: "error", Message: err.Error()})
		return status
	}
	if _, err := exec.LookPath(command); err != nil {
		status.State = "command_missing"
		status.Summary = "Configured command was not found on PATH."
		status.Checks = append(status.Checks, Check{Name: "command", Status: "error", Message: "command is not installed or not on PATH"})
		return status
	}
	status.CommandAvailable = true
	status.Checks = append(status.Checks, Check{Name: "command", Status: "pass", Message: "command is available on PATH"})
	return status
}

func builtInDefinitions() []ServerDefinition {
	return []ServerDefinition{
		{
			ID:              "aws-official",
			Name:            "AWS MCP Server",
			Vendor:          "AWS",
			Description:     "Official AWS MCP entry point for cloud inventory and operational context.",
			SourceURL:       "https://github.com/awslabs/mcp",
			DocsURL:         "https://github.com/awslabs/mcp",
			InstallHint:     "Set IAC_STUDIO_MCP_AWS_OFFICIAL_COMMAND after installing the AWS MCP server locally.",
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
			Capabilities:    []string{"aws inventory context", "documentation lookup", "read-only operational review"},
		},
		{
			ID:              "terraform-official",
			Name:            "Terraform MCP Server",
			Vendor:          "HashiCorp",
			Description:     "Official Terraform MCP server for registry, module, provider, and Terraform workflow context.",
			SourceURL:       "https://github.com/hashicorp/terraform-mcp-server",
			DocsURL:         "https://developer.hashicorp.com/terraform/mcp-server",
			InstallHint:     "Install terraform-mcp-server on PATH or set IAC_STUDIO_MCP_TERRAFORM_OFFICIAL_COMMAND.",
			Transport:       "stdio",
			Command:         "terraform-mcp-server",
			HealthCheckArgs: []string{"--version"},
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
			Capabilities:    []string{"terraform registry", "module discovery", "provider documentation", "read-only plan review"},
		},
	}
}

func normalizeDefinitions(definitions []ServerDefinition) []ServerDefinition {
	out := copyDefinitions(definitions)
	for i := range out {
		out[i].ID = strings.TrimSpace(out[i].ID)
		out[i].Command = strings.TrimSpace(out[i].Command)
		out[i] = applyEnvOverrides(out[i])
		if out[i].Transport == "" {
			out[i].Transport = "stdio"
		}
		if out[i].CredentialMode == "" {
			out[i].CredentialMode = "none"
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func copyDefinitions(definitions []ServerDefinition) []ServerDefinition {
	out := make([]ServerDefinition, len(definitions))
	for i, definition := range definitions {
		out[i] = definition
		out[i].Args = append([]string{}, definition.Args...)
		out[i].HealthCheckArgs = append([]string{}, definition.HealthCheckArgs...)
		out[i].Capabilities = append([]string{}, definition.Capabilities...)
	}
	return out
}

func applyEnvOverrides(definition ServerDefinition) ServerDefinition {
	prefix := "IAC_STUDIO_MCP_" + strings.ToUpper(strings.ReplaceAll(definition.ID, "-", "_"))
	if command := strings.TrimSpace(os.Getenv(prefix + "_COMMAND")); command != "" {
		definition.Command = command
	}
	if args := splitEnvArgs(os.Getenv(prefix + "_ARGS")); len(args) > 0 {
		definition.Args = args
	}
	if args := splitEnvArgs(os.Getenv(prefix + "_HEALTH_ARGS")); len(args) > 0 {
		definition.HealthCheckArgs = args
	}
	return definition
}

func splitEnvArgs(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return strings.Fields(value)
}

func validateCommand(command string) error {
	if command == "" {
		return errors.New("command is required")
	}
	if strings.ContainsAny(command, "\x00\r\n\t|&;<>`$") || strings.Contains(command, " ") {
		return fmt.Errorf("command %q must be a single executable name or absolute path without shell metacharacters", command)
	}
	return nil
}

func defaultProbe(ctx context.Context, command string, args []string, timeout time.Duration) ProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, command, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = minimalEnv()
	output, err := cmd.CombinedOutput()
	return ProbeResult{
		Output:   string(output),
		Err:      err,
		TimedOut: errors.Is(probeCtx.Err(), context.DeadlineExceeded),
	}
}

func minimalEnv() []string {
	env := []string{}
	if path := os.Getenv("PATH"); path != "" {
		env = append(env, "PATH="+path)
	}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "WINDIR"} {
			if value := os.Getenv(key); value != "" {
				env = append(env, key+"="+value)
			}
		}
	}
	return env
}

func redactOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	if len(output) > 600 {
		output = output[:600] + "..."
	}
	for _, pattern := range secretPatterns {
		output = pattern.ReplaceAllString(output, "[REDACTED]")
	}
	return output
}
