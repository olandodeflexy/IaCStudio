package mcpairlock

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestListIncludesTrustedBuiltinsWithoutHealthProbe(t *testing.T) {
	calls := 0
	manager := NewManager(t.TempDir(), WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
		calls++
		return ProbeResult{}
	}))

	statuses := manager.List(context.Background())

	if calls != 0 {
		t.Fatalf("List must not execute health probes, got %d calls", calls)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected built-in AWS and Terraform servers, got %d", len(statuses))
	}
	if !containsStatus(statuses, "aws-official") || !containsStatus(statuses, "terraform-official") {
		t.Fatalf("missing built-in server statuses: %+v", statuses)
	}
	for _, status := range statuses {
		if !status.Server.Trusted || !status.Server.ReadOnlyDefault || status.Server.CredentialMode != "none" {
			t.Fatalf("built-in server is not locked down by default: %+v", status.Server)
		}
	}
}

func TestCheckNotConfiguredDoesNotExecuteProbe(t *testing.T) {
	calls := 0
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "aws",
			Name:            "AWS",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			calls++
			return ProbeResult{}
		}),
	)

	status, err := manager.Check(context.Background(), "aws")

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if calls != 0 {
		t.Fatalf("not configured servers must not execute probes, got %d calls", calls)
	}
	if status.State != "not_configured" || status.Ready {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestCheckRejectsShellCommandConfiguration(t *testing.T) {
	manager := NewManager(t.TempDir(), WithDefinitions([]ServerDefinition{{
		ID:              "unsafe",
		Name:            "Unsafe",
		Command:         "terraform-mcp-server; aws sts get-caller-identity",
		Trusted:         true,
		ReadOnlyDefault: true,
		CredentialMode:  "none",
	}}))

	status, err := manager.Check(context.Background(), "unsafe")

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.State != "invalid_config" || status.CommandAvailable {
		t.Fatalf("expected invalid command config, got %+v", status)
	}
}

func TestListReportsSingleTrustVerdictForUntrustedDefinitions(t *testing.T) {
	manager := NewManager(t.TempDir(), WithDefinitions([]ServerDefinition{{
		ID:              "untrusted",
		Name:            "Untrusted",
		Command:         testExecutable(t),
		Trusted:         false,
		ReadOnlyDefault: true,
		CredentialMode:  "none",
	}}))

	statuses := manager.List(context.Background())

	if len(statuses) != 1 {
		t.Fatalf("expected one status, got %+v", statuses)
	}
	status := statuses[0]
	if status.State != "blocked" {
		t.Fatalf("expected blocked status, got %+v", status)
	}
	trustChecks := 0
	for _, check := range status.Checks {
		if check.Name != "trusted_registry" {
			continue
		}
		trustChecks++
		if check.Status != "error" {
			t.Fatalf("expected trusted_registry error check, got %+v", check)
		}
	}
	if trustChecks != 1 {
		t.Fatalf("expected one trusted_registry check, got %d in %+v", trustChecks, status.Checks)
	}
}

func TestValidateCommandAllowsAbsolutePathsWithSpaces(t *testing.T) {
	commands := []string{
		"/Applications/Hashi Corp/terraform-mcp-server",
		`C:\Program Files\Terraform MCP\terraform-mcp-server.exe`,
		`\\server\Terraform MCP\terraform-mcp-server.exe`,
	}

	for _, command := range commands {
		if err := validateCommand(command); err != nil {
			t.Fatalf("validateCommand(%q): %v", command, err)
		}
	}
}

func TestValidateCommandRejectsRelativeCommandsWithSpaces(t *testing.T) {
	command := "terraform mcp server"

	if err := validateCommand(command); err == nil {
		t.Fatalf("expected relative command with spaces to be rejected")
	}
}

func TestCheckRedactsProbeOutput(t *testing.T) {
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         "go",
			HealthCheckArgs: []string{"version"},
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithProbe(func(_ context.Context, command string, args []string, _ time.Duration) ProbeResult {
			if command != "go" || strings.Join(args, " ") != "version" {
				t.Fatalf("unexpected probe command: %s %v", command, args)
			}
			return ProbeResult{Output: "ok aws_secret_access_key=super-secret AKIA1234567890ABCDE"}
		}),
	)

	status, err := manager.Check(context.Background(), "terraform")

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !status.Ready || status.State != "ready" {
		t.Fatalf("expected ready status, got %+v", status)
	}
	message := status.Checks[len(status.Checks)-1].Message
	if strings.Contains(message, "super-secret") || strings.Contains(message, "AKIA") {
		t.Fatalf("probe output leaked sensitive material: %q", message)
	}
}

func TestCheckUnknownServerFailsClosed(t *testing.T) {
	manager := NewManager(t.TempDir())

	_, err := manager.Check(context.Background(), "unknown")

	if !errors.Is(err, ErrUnknownServer) {
		t.Fatalf("expected ErrUnknownServer, got %v", err)
	}
}

func TestStartStopLifecycleUsesLauncherAndReportsRunning(t *testing.T) {
	command := testExecutable(t)
	handle := newFakeProcess()
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         command,
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithLauncher(func(_ context.Context, definition ServerDefinition, _ time.Duration) (ProcessHandle, error) {
			if definition.Command != command {
				t.Fatalf("unexpected launch command: %q", definition.Command)
			}
			return handle, nil
		}),
	)

	status, err := manager.Start(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !status.Running || status.State != "running" || status.StartedAt == "" {
		t.Fatalf("expected running status, got %+v", status)
	}

	listed := manager.List(context.Background())
	if len(listed) != 1 || !listed[0].Running {
		t.Fatalf("expected running status in list, got %+v", listed)
	}

	status, err = manager.Stop(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if status.Running || status.State != "stopped" || !handle.stopped {
		t.Fatalf("expected stopped status, got status=%+v stopped=%v", status, handle.stopped)
	}
}

func TestStartRejectsUnsupportedTransportWithoutLaunching(t *testing.T) {
	calls := 0
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "http",
			Name:            "HTTP",
			Command:         testExecutable(t),
			Transport:       "sse",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithLauncher(func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
			calls++
			return newFakeProcess(), nil
		}),
	)

	status, err := manager.Start(context.Background(), "http")

	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if calls != 0 {
		t.Fatalf("unsupported transport should not launch, got %d calls", calls)
	}
	if status.State != "unsupported_transport" || status.Running {
		t.Fatalf("expected unsupported transport status, got %+v", status)
	}
}

func TestExitedProcessIsReapedIntoStatus(t *testing.T) {
	handle := newFakeProcess()
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         testExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithLauncher(func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
			return handle, nil
		}),
	)

	if _, err := manager.Start(context.Background(), "terraform"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	handle.exit(errors.New("boom secret_access_key=super-secret"))

	statuses := manager.List(context.Background())
	if len(statuses) != 1 {
		t.Fatalf("expected one status, got %+v", statuses)
	}
	status := statuses[0]
	if status.Running || status.State != "exited" || status.LastExitAt == "" {
		t.Fatalf("expected reaped exited status, got %+v", status)
	}
	if strings.Contains(status.LastExitReason, "super-secret") {
		t.Fatalf("exit reason leaked secret value: %q", status.LastExitReason)
	}
}

func containsStatus(statuses []ServerStatus, id string) bool {
	for _, status := range statuses {
		if status.Server.ID == id {
			return true
		}
	}
	return false
}

func testExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

type fakeProcess struct {
	done    chan error
	stopped bool
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{done: make(chan error, 1)}
}

func (p *fakeProcess) Done() <-chan error {
	return p.done
}

func (p *fakeProcess) Stop(context.Context) error {
	p.stopped = true
	p.exit(nil)
	return nil
}

func (p *fakeProcess) exit(err error) {
	select {
	case p.done <- err:
	default:
	}
}
