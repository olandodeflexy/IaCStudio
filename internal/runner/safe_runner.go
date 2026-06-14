package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SafeRunner wraps Runner with timeouts, cancellation, approval gates, and kill switch.
type SafeRunner struct {
	runner   *Runner
	mu       sync.Mutex
	active   map[string]*Execution // projectDir -> running execution
	defaults SafetyConfig
}

// SafetyConfig defines execution safety parameters.
type SafetyConfig struct {
	DefaultTimeout  time.Duration `json:"default_timeout"` // max time for any command
	PlanTimeout     time.Duration `json:"plan_timeout"`
	ApplyTimeout    time.Duration `json:"apply_timeout"`
	InitTimeout     time.Duration `json:"init_timeout"`
	RequireApproval bool          `json:"require_approval"` // require plan review before apply
	MaxOutputBytes  int           `json:"max_output_bytes"` // prevent OOM from huge plans
}

// Execution tracks a running command with cancellation support.
type Execution struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Tool      string    `json:"tool"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
	Status    string    `json:"status"` // running | completed | failed | cancelled | timed_out
	cancel    context.CancelFunc
}

// ExecutionResult is returned when a command completes.
type ExecutionResult struct {
	ID        string        `json:"id"`
	Output    string        `json:"output"`
	ExitCode  int           `json:"exit_code"`
	Duration  time.Duration `json:"duration"`
	Status    string        `json:"status"`
	Truncated bool          `json:"truncated"` // true if output was cut
}

// PlanGate holds a pending plan that needs approval before apply.
type PlanGate struct {
	Project    string     `json:"project"`
	Tool       string     `json:"tool"`
	PlanOutput string     `json:"plan_output"`
	Summary    string     `json:"summary"` // "3 to add, 1 to change, 0 to destroy"
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
}

func DefaultSafetyConfig() SafetyConfig {
	return SafetyConfig{
		DefaultTimeout:  5 * time.Minute,
		PlanTimeout:     10 * time.Minute,
		ApplyTimeout:    30 * time.Minute,
		InitTimeout:     5 * time.Minute,
		RequireApproval: true,
		MaxOutputBytes:  10 * 1024 * 1024, // 10MB
	}
}

func NewSafeRunner(config SafetyConfig) *SafeRunner {
	return &SafeRunner{
		runner:   New(),
		active:   make(map[string]*Execution),
		defaults: config,
	}
}

// Execute runs a command with timeout, cancellation, and output limiting.
//
// env names the layered-v1 environment, threaded through to the
// underlying Runner.buildArgs so pulumi commands receive an explicit
// `--stack <env>`. Empty env runs in projectDir's own workspace
// (flat layouts).
func (sr *SafeRunner) Execute(ctx context.Context, projectDir, tool, command, env string) (*ExecutionResult, error) {
	return sr.ExecuteWithEnv(ctx, projectDir, tool, command, env, nil)
}

// ExecuteWithEnv runs a command with additional environment variables. Values
// in extraEnv override the inherited process environment for this command only.
func (sr *SafeRunner) ExecuteWithEnv(ctx context.Context, projectDir, tool, command, env string, extraEnv map[string]string) (*ExecutionResult, error) {
	return sr.executeWithEnv(ctx, projectDir, tool, command, env, extraEnv, false)
}

// ExecuteWithScopedEnv runs a command with a minimal command environment plus
// the selected connection variables. It is intended for Cloud Connection scoped
// runs where ambient host cloud credentials must not leak into the subprocess.
func (sr *SafeRunner) ExecuteWithScopedEnv(ctx context.Context, projectDir, tool, command, env string, extraEnv map[string]string) (*ExecutionResult, error) {
	return sr.executeWithEnv(ctx, projectDir, tool, command, env, extraEnv, true)
}

func (sr *SafeRunner) executeWithEnv(ctx context.Context, projectDir, tool, command, env string, extraEnv map[string]string, scoped bool) (*ExecutionResult, error) {
	// Project-level lock — prevent concurrent executions on the same project
	// which would cause terraform state lock contention and corruption.
	sr.mu.Lock()
	if existing, running := sr.active[projectDir]; running {
		sr.mu.Unlock()
		return nil, fmt.Errorf("project already has a running command: %s %s (started %s). Kill it first or wait for it to finish",
			existing.Tool, existing.Command, existing.StartedAt.Format("15:04:05"))
	}
	sr.mu.Unlock()

	timeout := sr.timeoutFor(command)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	execID := fmt.Sprintf("%s-%s-%d", tool, command, time.Now().UnixNano())
	execution := &Execution{
		ID:        execID,
		Project:   projectDir,
		Tool:      tool,
		Command:   command,
		StartedAt: time.Now(),
		Status:    "running",
		cancel:    cancel,
	}

	sr.mu.Lock()
	sr.active[projectDir] = execution
	sr.mu.Unlock()

	defer func() {
		sr.mu.Lock()
		delete(sr.active, projectDir)
		sr.mu.Unlock()
	}()

	// Build command args
	args := sr.runner.buildArgs(tool, command, env)
	if len(args) == 0 {
		return nil, fmt.Errorf("unknown tool/command: %s %s", tool, command)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = projectDir
	switch {
	case scoped:
		cmd.Env = scopedCommandEnv(os.Environ(), extraEnv)
	case len(extraEnv) > 0:
		cmd.Env = mergeEnv(os.Environ(), extraEnv)
	}

	configureCommandCancel(cmd)

	var stdout, stderr bytes.Buffer
	// Limit output to prevent OOM
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: sr.defaults.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: sr.defaults.MaxOutputBytes}

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	result := &ExecutionResult{
		ID:       execID,
		Duration: duration,
	}

	// Combine output
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n" + stderr.String()
	}
	result.Output = output

	// Check if output was truncated
	if lw, ok := cmd.Stdout.(*limitedWriter); ok && lw.truncated {
		result.Truncated = true
		result.Output += "\n\n--- OUTPUT TRUNCATED (exceeded limit) ---"
	}

	// Determine status
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		result.Status = "timed_out"
		execution.Status = "timed_out"
		return result, fmt.Errorf("command timed out after %v", timeout)
	case ctx.Err() == context.Canceled:
		result.Status = "cancelled"
		execution.Status = "cancelled"
		return result, fmt.Errorf("command was cancelled")
	case err != nil:
		result.Status = "failed"
		execution.Status = "failed"
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		return result, err
	default:
		result.Status = "completed"
		execution.Status = "completed"
		result.ExitCode = 0
		return result, nil
	}
}

func scopedCommandEnv(base []string, overrides map[string]string) []string {
	env := mergeEnv(minimalCommandEnv(base), overrides)
	if !envHasKey(env, "AWS_EC2_METADATA_DISABLED") {
		env = append(env, "AWS_EC2_METADATA_DISABLED=true")
	}
	return env
}

func minimalCommandEnv(base []string) []string {
	allow := map[string]bool{
		"PATH":               true,
		"Path":               true,
		"PATHEXT":            true,
		"HOME":               true,
		"USERPROFILE":        true,
		"HOMEDRIVE":          true,
		"HOMEPATH":           true,
		"APPDATA":            true,
		"LOCALAPPDATA":       true,
		"SystemRoot":         true,
		"WINDIR":             true,
		"TMPDIR":             true,
		"TMP":                true,
		"TEMP":               true,
		"LANG":               true,
		"LC_ALL":             true,
		"SSL_CERT_FILE":      true,
		"SSL_CERT_DIR":       true,
		"REQUESTS_CA_BUNDLE": true,
		"CURL_CA_BUNDLE":     true,
		"HTTP_PROXY":         true,
		"HTTPS_PROXY":        true,
		"NO_PROXY":           true,
		"http_proxy":         true,
		"https_proxy":        true,
		"no_proxy":           true,
	}
	out := make([]string, 0, len(allow))
	seen := map[string]bool{}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || seen[key] || !allow[key] || isCloudCredentialEnvKey(key) {
			continue
		}
		out = append(out, key+"="+value)
		seen[key] = true
	}
	return out
}

func envHasKey(env []string, want string) bool {
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && key == want {
			return true
		}
	}
	return false
}

func isCloudCredentialEnvKey(key string) bool {
	if strings.HasPrefix(key, "TF_TOKEN_") {
		return true
	}
	switch key {
	case
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_PROFILE",
		"AWS_DEFAULT_PROFILE",
		"AWS_SHARED_CREDENTIALS_FILE",
		"AWS_CONFIG_FILE",
		"AWS_SDK_LOAD_CONFIG",
		"AWS_ROLE_ARN",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
		"AWS_EC2_METADATA_DISABLED",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"ARM_CLIENT_ID",
		"ARM_CLIENT_SECRET",
		"ARM_CLIENT_CERTIFICATE_PATH",
		"ARM_CLIENT_CERTIFICATE_PASSWORD",
		"ARM_TENANT_ID",
		"ARM_SUBSCRIPTION_ID",
		"ARM_USE_MSI",
		"ARM_MSI_ENDPOINT",
		"ARM_OIDC_TOKEN",
		"ARM_USE_OIDC",
		"AZURE_CLIENT_ID",
		"AZURE_CLIENT_SECRET",
		"AZURE_TENANT_ID",
		"AZURE_SUBSCRIPTION_ID",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"GOOGLE_CREDENTIALS",
		"GOOGLE_CLOUD_KEYFILE_JSON",
		"GOOGLE_OAUTH_ACCESS_TOKEN",
		"GOOGLE_PROJECT",
		"GOOGLE_CLOUD_PROJECT",
		"GOOGLE_REGION",
		"CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE",
		"CLOUDSDK_CORE_PROJECT",
		"CLOUDSDK_COMPUTE_REGION",
		"TF_CLOUD_TOKEN",
		"TFE_TOKEN":
		return true
	default:
		return false
	}
}

func mergeEnv(base []string, overrides map[string]string) []string {
	values := map[string]string{}
	order := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = value
	}
	for key, value := range overrides {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = value
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, key+"="+values[key])
	}
	return out
}

// Kill cancels a running execution for a project (the kill switch).
func (sr *SafeRunner) Kill(projectDir string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	exec, ok := sr.active[projectDir]
	if !ok {
		return fmt.Errorf("no active execution for project")
	}
	exec.cancel()
	exec.Status = "cancelled"
	return nil
}

// ActiveExecutions returns all currently running commands.
func (sr *SafeRunner) ActiveExecutions() []*Execution {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	var execs []*Execution
	for _, e := range sr.active {
		execs = append(execs, e)
	}
	return execs
}

// DetectTools delegates to the underlying runner.
func (sr *SafeRunner) DetectTools() []ToolInfo {
	return sr.runner.DetectTools()
}

// ExecutePlanJSON runs terraform plan with JSON output for structured parsing.
func (sr *SafeRunner) ExecutePlanJSON(ctx context.Context, projectDir, tool string) (*ExecutionResult, error) {
	timeout := sr.defaults.PlanTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	binary := "terraform"
	if tool == "opentofu" {
		binary = "tofu"
	}

	cmd := exec.CommandContext(ctx, binary, "plan", "-json", "-no-color")
	cmd.Dir = projectDir
	configureCommandCancel(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: sr.defaults.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: sr.defaults.MaxOutputBytes}

	start := time.Now()
	err := cmd.Run()

	result := &ExecutionResult{
		ID:       fmt.Sprintf("plan-json-%d", time.Now().UnixNano()),
		Output:   stdout.String(),
		Duration: time.Since(start),
	}

	if err != nil {
		result.Status = "failed"
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		// Still return output — plan -json outputs errors as JSON too
		return result, nil
	}

	result.Status = "completed"
	return result, nil
}

// ExportSavedPlanJSON renders the saved Terraform/OpenTofu plan file created
// by `plan -out=tfplan` into full JSON (`show -json tfplan`). The result is
// used by policy engines and the semantic apply gate.
func (sr *SafeRunner) ExportSavedPlanJSON(ctx context.Context, projectDir, tool string, extraEnv map[string]string) (*ExecutionResult, error) {
	return sr.exportSavedPlanJSON(ctx, projectDir, tool, extraEnv, false)
}

// ExportSavedPlanJSONWithScopedEnv is the Cloud Connection scoped variant of
// ExportSavedPlanJSON. It avoids leaking ambient host cloud credentials while
// rendering the saved plan.
func (sr *SafeRunner) ExportSavedPlanJSONWithScopedEnv(ctx context.Context, projectDir, tool string, extraEnv map[string]string) (*ExecutionResult, error) {
	return sr.exportSavedPlanJSON(ctx, projectDir, tool, extraEnv, true)
}

func (sr *SafeRunner) exportSavedPlanJSON(ctx context.Context, projectDir, tool string, extraEnv map[string]string, scoped bool) (*ExecutionResult, error) {
	timeout := sr.defaults.PlanTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var binary string
	switch tool {
	case "terraform":
		binary = "terraform"
	case "opentofu":
		binary = "tofu"
	default:
		return nil, fmt.Errorf("saved plan JSON export is not supported for %s", tool)
	}

	cmd := exec.CommandContext(ctx, binary, "show", "-json", "tfplan")
	cmd.Dir = projectDir
	switch {
	case scoped:
		cmd.Env = scopedCommandEnv(os.Environ(), extraEnv)
	case len(extraEnv) > 0:
		cmd.Env = mergeEnv(os.Environ(), extraEnv)
	}
	configureCommandCancel(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: sr.defaults.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: sr.defaults.MaxOutputBytes}

	start := time.Now()
	err := cmd.Run()

	result := &ExecutionResult{
		ID:       fmt.Sprintf("plan-show-json-%d", time.Now().UnixNano()),
		Output:   stdout.String(),
		Duration: time.Since(start),
	}
	if stderr.Len() > 0 {
		result.Output += "\n" + stderr.String()
	}
	if lw, ok := cmd.Stdout.(*limitedWriter); ok && lw.truncated {
		result.Truncated = true
		result.Output += "\n\n--- OUTPUT TRUNCATED (exceeded limit) ---"
	}

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		result.Status = "timed_out"
		return result, fmt.Errorf("command timed out after %v", timeout)
	case ctx.Err() == context.Canceled:
		result.Status = "cancelled"
		return result, fmt.Errorf("command was cancelled")
	case err != nil:
		result.Status = "failed"
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
		return result, err
	default:
		result.Status = "completed"
		result.ExitCode = 0
		return result, nil
	}
}

// RequiresApproval returns true if the command needs plan review first.
// Covers both the Terraform/Ansible vocabulary ("apply", "destroy")
// and Pulumi's native verbs ("up", "refresh") so every state-mutating
// action lands on the approval gate regardless of which tool the user
// picked. refresh is gated because Pulumi's refresh re-reads and
// overwrites the stack state file with whatever it observes in the
// cloud — a surprise refresh can hide drift an operator wanted to
// investigate.
func (sr *SafeRunner) RequiresApproval(command string) bool {
	if !sr.defaults.RequireApproval {
		return false
	}
	switch command {
	case "apply", "up", "destroy", "refresh":
		return true
	}
	return false
}

// ParsePlanSummary extracts the summary line from terraform plan output.
func ParsePlanSummary(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "to add") && strings.Contains(line, "to change") {
			return strings.TrimSpace(line)
		}
		if strings.Contains(line, "No changes") || strings.Contains(line, "no changes") {
			return "No changes. Infrastructure is up-to-date."
		}
	}
	return "Unable to parse plan summary"
}

func (sr *SafeRunner) timeoutFor(command string) time.Duration {
	switch command {
	case "init":
		return sr.defaults.InitTimeout
	case "plan", "preview":
		return sr.defaults.PlanTimeout
	case "apply", "up", "destroy", "refresh":
		// Pulumi's "up" and "refresh" mutate state the same way
		// terraform's "apply" / "destroy" do — give them the long
		// apply-timeout bucket so real stacks don't hit the 5-minute
		// DefaultTimeout prematurely.
		return sr.defaults.ApplyTimeout
	default:
		return sr.defaults.DefaultTimeout
	}
}

// limitedWriter caps output size to prevent OOM on huge terraform plans.
type limitedWriter struct {
	buf       *bytes.Buffer
	limit     int
	truncated bool
}

func (lw *limitedWriter) Write(p []byte) (n int, err error) {
	if lw.buf.Len()+len(p) > lw.limit {
		remaining := lw.limit - lw.buf.Len()
		if remaining > 0 {
			lw.buf.Write(p[:remaining])
		}
		lw.truncated = true
		return len(p), nil // pretend we wrote it all so the command doesn't error
	}
	return lw.buf.Write(p)
}
