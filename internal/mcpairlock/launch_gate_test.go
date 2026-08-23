package mcpairlock

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStartBlocksUnapprovedExecutable(t *testing.T) {
	launches := 0
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{executableApprovalDefinition(testExecutable(t))}),
		WithLauncher(func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
			launches++
			return newFakeProcess(), nil
		}),
	)

	status, err := manager.Start(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if status.State != "blocked" || status.Ready || status.Running {
		t.Fatalf("expected blocked launch, got %+v", status)
	}
	if status.ExecutableAttestation != ExecutableAttestationApprovalRequired {
		t.Fatalf("attestation = %q, want %q", status.ExecutableAttestation, ExecutableAttestationApprovalRequired)
	}
	if launches != 0 || !hasCheck(status.Checks, "start", "error") {
		t.Fatalf("launches=%d checks=%+v", launches, status.Checks)
	}
}

func TestStartBlocksOutdatedVersionBeforeLaunching(t *testing.T) {
	launches := 0
	manager := launchGateManager(t, "terraform-mcp-server 1.3.9", func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
		launches++
		return newFakeProcess(), nil
	})
	approveExecutableForLaunchTest(t, manager, "terraform")

	status, err := manager.Start(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if status.State != "outdated" || status.Ready || status.Running {
		t.Fatalf("expected outdated launch block, got %+v", status)
	}
	if status.ObservedVersion != "1.3.9" || launches != 0 {
		t.Fatalf("version=%q launches=%d", status.ObservedVersion, launches)
	}
	if !hasCheck(status.Checks, "version_policy", "error") || !hasCheck(status.Checks, "start", "error") {
		t.Fatalf("expected version and start failures, got %+v", status.Checks)
	}
}

func TestStartLaunchesApprovedCurrentVersion(t *testing.T) {
	launches := 0
	var launched ServerDefinition
	manager := launchGateManager(t, "terraform-mcp-server 1.4.1", func(_ context.Context, definition ServerDefinition, _ time.Duration) (ProcessHandle, error) {
		launches++
		launched = definition
		return newFakeProcess(), nil
	})
	t.Cleanup(func() { _ = manager.Close() })
	approveExecutableForLaunchTest(t, manager, "terraform")

	status, err := manager.Start(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if status.State != "running" || !status.Ready || !status.Running {
		t.Fatalf("expected running server, got %+v", status)
	}
	if status.ObservedVersion != "1.4.1" || status.ExecutableAttestation != ExecutableAttestationApproved {
		t.Fatalf("unexpected launch evidence: %+v", status)
	}
	resolved, err := resolveExecutable(status.Server.Command)
	if err != nil {
		t.Fatalf("resolve executable: %v", err)
	}
	if launches != 1 || launched.Command != resolved {
		t.Fatalf("launches=%d command=%q want %q", launches, launched.Command, resolved)
	}
}

func TestStartAlreadyRunningSkipsPreflight(t *testing.T) {
	probes := 0
	launches := 0
	handle := newFakeProcess()
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{launchGateDefinition(t)}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			probes++
			return ProbeResult{Output: "terraform-mcp-server 1.4.1"}
		}),
		WithLauncher(func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
			launches++
			return handle, nil
		}),
	)
	t.Cleanup(func() { _ = manager.Close() })
	approveExecutableForLaunchTest(t, manager, "terraform")

	if status, err := manager.Start(context.Background(), "terraform"); err != nil || !status.Running {
		t.Fatalf("first Start: status=%+v err=%v", status, err)
	}
	status, err := manager.Start(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if !status.Running || status.State != "running" || !hasCheck(status.Checks, "start", "pass") {
		t.Fatalf("expected already-running status, got %+v", status)
	}
	if probes != 1 || launches != 1 {
		t.Fatalf("probes=%d launches=%d, want one each", probes, launches)
	}
}

func TestStartRevalidatesApprovalAfterHealthProbe(t *testing.T) {
	root := t.TempDir()
	launches := 0
	var store *ExecutableAttestationStore
	var changed ExecutableAttestation
	manager := NewManager(root,
		WithDefinitions([]ServerDefinition{launchGateDefinition(t)}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			if err := store.Save(changed); err != nil {
				return ProbeResult{Err: err}
			}
			return ProbeResult{Output: "terraform-mcp-server 1.4.1"}
		}),
		WithLauncher(func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
			launches++
			return newFakeProcess(), nil
		}),
	)
	approved, err := manager.ApproveExecutable(context.Background(), "terraform", executableFingerprintForTest(t, testExecutable(t)))
	if err != nil {
		t.Fatalf("ApproveExecutable: %v", err)
	}
	store, err = NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatalf("NewExecutableAttestationStore: %v", err)
	}
	changed = approved
	changed.Fingerprint.Digest = strings.Repeat("0", 64)
	if changed.Fingerprint.Digest == approved.Fingerprint.Digest {
		changed.Fingerprint.Digest = strings.Repeat("1", 64)
	}

	status, err := manager.Start(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if status.State != "blocked" || status.Ready || status.Running || launches != 0 {
		t.Fatalf("expected revalidation block, launches=%d status=%+v", launches, status)
	}
	if status.ExecutableAttestation != ExecutableAttestationChanged {
		t.Fatalf("attestation = %q, want %q", status.ExecutableAttestation, ExecutableAttestationChanged)
	}
	if !hasCheck(status.Checks, "executable_attestation", "error") || !hasCheck(status.Checks, "start", "error") {
		t.Fatalf("expected revalidation failures, got %+v", status.Checks)
	}
	if !hasCheck(status.Checks, "executable_fingerprint", "pass") || hasCheck(status.Checks, "executable_fingerprint", "error") {
		t.Fatalf("expected matching fingerprint evidence, got %+v", status.Checks)
	}
}

func launchGateManager(t *testing.T, probeOutput string, launcher LauncherFunc) *Manager {
	t.Helper()
	return NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{launchGateDefinition(t)}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			return ProbeResult{Output: probeOutput}
		}),
		WithLauncher(launcher),
	)
}

func launchGateDefinition(t *testing.T) ServerDefinition {
	t.Helper()
	definition := executableApprovalDefinition(testExecutable(t))
	definition.HealthCheckArgs = []string{"--version"}
	definition.VersionConstraint = ">= 1.4.0"
	return definition
}
