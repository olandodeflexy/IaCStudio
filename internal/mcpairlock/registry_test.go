package mcpairlock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
		if status.Server.LaunchSource != LaunchSourceRegistry {
			t.Fatalf("built-in server launch source = %q, want %q", status.Server.LaunchSource, LaunchSourceRegistry)
		}
		if strings.Contains(status.Server.InstallHint, "IAC_STUDIO_MCP_") {
			t.Fatalf("built-in install hint recommends blocked environment overrides: %q", status.Server.InstallHint)
		}
	}
}

func TestEnvironmentCommandOverrideDoesNotInheritRegistryTrust(t *testing.T) {
	t.Setenv("IAC_STUDIO_MCP_TERRAFORM_OFFICIAL_COMMAND", testExecutable(t))
	launches := 0
	manager := NewManager(t.TempDir(), WithLauncher(func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
		launches++
		return newFakeProcess(), nil
	}))

	status, err := manager.Start(context.Background(), "terraform-official")

	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if status.State != "blocked" || status.Server.Trusted {
		t.Fatalf("environment override must be blocked, got %+v", status)
	}
	if launches != 0 {
		t.Fatalf("blocked environment override launched %d processes", launches)
	}
	if status.Server.LaunchSource != LaunchSourceEnvironmentOverride {
		t.Fatalf("launch source = %q, want %q", status.Server.LaunchSource, LaunchSourceEnvironmentOverride)
	}
	if !hasCheck(status.Checks, "launch_provenance", "error") {
		t.Fatalf("expected failed launch provenance check, got %+v", status.Checks)
	}
}

func TestEnvironmentArgumentsOverrideDoesNotInheritRegistryTrust(t *testing.T) {
	t.Setenv("IAC_STUDIO_MCP_TERRAFORM_OFFICIAL_ARGS", "--transport stdio")
	manager := NewManager(t.TempDir())

	status := statusByID(t, manager.List(context.Background()), "terraform-official")

	if status.State != "blocked" || status.Server.Trusted {
		t.Fatalf("environment arguments override must be blocked, got %+v", status)
	}
	if status.Server.LaunchSource != LaunchSourceEnvironmentOverride {
		t.Fatalf("launch source = %q, want %q", status.Server.LaunchSource, LaunchSourceEnvironmentOverride)
	}
}

func TestEnvironmentHealthArgumentsOverrideDoesNotRunProbe(t *testing.T) {
	t.Setenv("IAC_STUDIO_MCP_TERRAFORM_OFFICIAL_HEALTH_ARGS", "dangerous subcommand")
	probes := 0
	manager := NewManager(t.TempDir(), WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
		probes++
		return ProbeResult{}
	}))

	status, err := manager.Check(context.Background(), "terraform-official")

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.State != "blocked" || status.Server.Trusted {
		t.Fatalf("environment health arguments override must be blocked, got %+v", status)
	}
	if status.Server.LaunchSource != LaunchSourceEnvironmentOverride {
		t.Fatalf("launch source = %q, want %q", status.Server.LaunchSource, LaunchSourceEnvironmentOverride)
	}
	if probes != 0 {
		t.Fatalf("blocked health arguments override invoked %d probes", probes)
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
	if !hasCheck(status.Checks, "launch_provenance", "pass") {
		t.Fatalf("expected explicit launch provenance check, got %+v", status.Checks)
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
	wantCommand, err := resolveExecutable("go")
	if err != nil {
		t.Fatal(err)
	}
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
			if command != wantCommand || strings.Join(args, " ") != "version" {
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

func TestCheckRejectsProbeOutputOverflow(t *testing.T) {
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         testExecutable(t),
			HealthCheckArgs: []string{"--version"},
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			return ProbeResult{Output: "discarded diagnostic output", OutputOverflow: true}
		}),
	)

	status, err := manager.Check(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.Ready || status.State != "output_too_large" {
		t.Fatalf("expected output_too_large status, got %+v", status)
	}
	if !hasCheck(status.Checks, "health_probe", "error") {
		t.Fatalf("expected failed health probe, got %+v", status.Checks)
	}
}

func TestBoundedProbeOutputCapsStorageWithoutShortWrites(t *testing.T) {
	output := newBoundedProbeOutput(4)

	written, err := output.Write([]byte("abcdef"))
	if err != nil || written != 6 {
		t.Fatalf("Write: written=%d err=%v", written, err)
	}
	captured, overflow := output.snapshot()
	if captured != "abcd" || !overflow {
		t.Fatalf("snapshot = %q overflow=%t, want %q true", captured, overflow, "abcd")
	}
}

func TestDefaultProbeBoundsOutputDuringExecution(t *testing.T) {
	result := defaultProbe(
		context.Background(),
		testExecutable(t),
		[]string{"-test.run=^TestProbeOutputHelperProcess$", "--", "mcp-probe-output-helper"},
		5*time.Second,
	)

	if result.Err != nil || result.TimedOut {
		t.Fatalf("defaultProbe: err=%v timed_out=%t", result.Err, result.TimedOut)
	}
	if !result.OutputOverflow {
		t.Fatal("expected probe output overflow")
	}
	if result.Output != strings.Repeat("x", maxProbeOutputBytes) {
		t.Fatalf("captured output length = %d, want %d", len(result.Output), maxProbeOutputBytes)
	}
}

func TestProbeOutputHelperProcess(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "mcp-probe-output-helper" {
		return
	}
	_, _ = os.Stdout.WriteString(strings.Repeat("x", maxProbeOutputBytes+1))
}

func TestCheckReportsExecutableFingerprint(t *testing.T) {
	root := t.TempDir()
	command := testExecutable(t)
	contents, err := os.ReadFile(command)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(contents)
	store, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ExecutableAttestation{
		ServerID:     "terraform",
		LaunchSource: LaunchSourceExplicitDefinition,
		Fingerprint: ExecutableFingerprint{
			Algorithm: "sha256",
			Digest:    hex.EncodeToString(want[:]),
		},
		ApprovedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(root,
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         command,
			HealthCheckArgs: []string{"version"},
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			return ProbeResult{}
		}),
	)

	status, err := manager.Check(context.Background(), "terraform")

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.ExecutableFingerprint == nil {
		t.Fatalf("expected executable fingerprint, got %+v", status)
	}
	if status.ExecutableFingerprint.Algorithm != "sha256" || status.ExecutableFingerprint.Digest != hex.EncodeToString(want[:]) {
		t.Fatalf("fingerprint = %+v, want sha256:%x", status.ExecutableFingerprint, want)
	}
	if !hasCheck(status.Checks, "executable_fingerprint", "pass") {
		t.Fatalf("expected executable fingerprint check, got %+v", status.Checks)
	}
	if status.ExecutableAttestation != ExecutableAttestationApproved || !hasCheck(status.Checks, "executable_attestation", "pass") {
		t.Fatalf("expected approved executable attestation, got %+v", status)
	}
}

func TestCheckWarnsWhenAttestationStorageIsNotConfigured(t *testing.T) {
	manager := NewManager("",
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         testExecutable(t),
			HealthCheckArgs: []string{"version"},
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			return ProbeResult{}
		}),
	)

	status, err := manager.Check(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.ExecutableAttestation != ExecutableAttestationApprovalRequired {
		t.Fatalf("attestation verdict = %q, want %q", status.ExecutableAttestation, ExecutableAttestationApprovalRequired)
	}
	if !hasCheck(status.Checks, "executable_attestation", "warn") {
		t.Fatalf("expected unavailable storage warning, got %+v", status.Checks)
	}
}

func TestCheckRejectsOversizedExecutableBeforeProbe(t *testing.T) {
	name := "oversized-mcp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	command := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(command, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(command, maxExecutableFingerprintBytes+1); err != nil {
		t.Fatal(err)
	}
	probes := 0
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         command,
			HealthCheckArgs: []string{"version"},
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			probes++
			return ProbeResult{}
		}),
	)

	status, err := manager.Check(context.Background(), "terraform")

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.State != "fingerprint_failed" || status.ExecutableFingerprint != nil {
		t.Fatalf("expected fingerprint failure, got %+v", status)
	}
	if probes != 0 {
		t.Fatalf("fingerprint failure invoked %d probes", probes)
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
	assertUniqueCheckNames(t, status)

	listed := manager.List(context.Background())
	if len(listed) != 1 || !listed[0].Running {
		t.Fatalf("expected running status in list, got %+v", listed)
	}
	assertUniqueCheckNames(t, listed[0])

	status, err = manager.Stop(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if status.Running || status.State != "stopped" || !handle.stopped {
		t.Fatalf("expected stopped status, got status=%+v stopped=%v", status, handle.stopped)
	}
	if status.LastExitReason != "stopped by user" {
		t.Fatalf("expected user stop exit reason, got %+v", status)
	}
	assertUniqueCheckNames(t, status)
	for _, check := range status.Checks {
		if check.Name == "lifecycle" && check.Status == "warn" {
			t.Fatalf("successful stop returned lifecycle warning: %+v", status.Checks)
		}
	}
}

func TestStartDoesNotBlockLifecycleStatusDuringLaunch(t *testing.T) {
	launcherEntered := make(chan struct{})
	releaseLauncher := make(chan struct{})
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
			close(launcherEntered)
			<-releaseLauncher
			return newFakeProcess(), nil
		}),
	)

	startDone := make(chan error, 1)
	go func() {
		_, err := manager.Start(context.Background(), "terraform")
		startDone <- err
	}()
	select {
	case <-launcherEntered:
	case <-time.After(250 * time.Millisecond):
		close(releaseLauncher)
		t.Fatalf("timed out waiting for launcher to start")
	}

	statusesDone := make(chan []ServerStatus, 1)
	go func() {
		statusesDone <- manager.List(context.Background())
	}()

	select {
	case statuses := <-statusesDone:
		if len(statuses) != 1 || statuses[0].State != "starting" {
			t.Fatalf("expected starting status during launch, got %+v", statuses)
		}
		assertUniqueCheckNames(t, statuses[0])
	case <-time.After(250 * time.Millisecond):
		close(releaseLauncher)
		t.Fatalf("List blocked behind Start launcher")
	}

	close(releaseLauncher)
	if err := <-startDone; err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestConcurrentStopClaimsProcessOnce(t *testing.T) {
	handle := newBlockingStopProcess()
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

	firstStopDone := make(chan ServerStatus, 1)
	go func() {
		status, err := manager.Stop(context.Background(), "terraform")
		if err != nil {
			t.Errorf("Stop: %v", err)
		}
		firstStopDone <- status
	}()
	select {
	case <-handle.stopEntered:
	case <-time.After(250 * time.Millisecond):
		close(handle.releaseStop)
		t.Fatalf("timed out waiting for stop to enter ProcessHandle.Stop")
	}

	secondStatus, err := manager.Stop(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if secondStatus.State != "stopping" {
		t.Fatalf("expected concurrent stop to report stopping, got %+v", secondStatus)
	}
	assertUniqueCheckNames(t, secondStatus)
	if calls := handle.stopCalls.Load(); calls != 1 {
		t.Fatalf("expected one ProcessHandle.Stop call, got %d", calls)
	}

	close(handle.releaseStop)
	firstStatus := <-firstStopDone
	if firstStatus.State != "stopped" || firstStatus.Running {
		t.Fatalf("expected first stop to complete, got %+v", firstStatus)
	}
	assertUniqueCheckNames(t, firstStatus)
}

func TestStopAfterProcessExitUsesDistinctStopCheck(t *testing.T) {
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
	handle.exit(errors.New("boom"))

	status, err := manager.Stop(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if status.State != "exited" || status.LastExitReason != "boom" {
		t.Fatalf("expected exited status from prior process exit, got %+v", status)
	}
	assertUniqueCheckNames(t, status)
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

func statusByID(t *testing.T, statuses []ServerStatus, id string) ServerStatus {
	t.Helper()
	for _, status := range statuses {
		if status.Server.ID == id {
			return status
		}
	}
	t.Fatalf("status %q not found in %+v", id, statuses)
	return ServerStatus{}
}

func assertUniqueCheckNames(t *testing.T, status ServerStatus) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, check := range status.Checks {
		if _, ok := seen[check.Name]; ok {
			t.Fatalf("duplicate check name %q in %+v", check.Name, status.Checks)
		}
		seen[check.Name] = struct{}{}
	}
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

type blockingStopProcess struct {
	done        chan error
	stopEntered chan struct{}
	releaseStop chan struct{}
	stopCalls   atomic.Int32
	once        sync.Once
}

func newBlockingStopProcess() *blockingStopProcess {
	return &blockingStopProcess{
		done:        make(chan error, 1),
		stopEntered: make(chan struct{}),
		releaseStop: make(chan struct{}),
	}
}

func (p *blockingStopProcess) Done() <-chan error {
	return p.done
}

func (p *blockingStopProcess) Stop(ctx context.Context) error {
	p.stopCalls.Add(1)
	p.once.Do(func() { close(p.stopEntered) })
	select {
	case <-p.releaseStop:
		p.exit(nil)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *blockingStopProcess) exit(err error) {
	select {
	case p.done <- err:
	default:
	}
}
