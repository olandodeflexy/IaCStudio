package mcpairlock

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEvaluateVersionConstraint(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		constraint string
		observed   string
		satisfied  bool
	}{
		{name: "minimum met", output: "terraform-mcp-server v1.4.2", constraint: ">= 1.4.0", observed: "1.4.2", satisfied: true},
		{name: "minimum not met", output: "terraform-mcp-server 1.3.9", constraint: ">=1.4.0", observed: "1.3.9", satisfied: false},
		{name: "exact precedence ignores observed build", output: "version: 2.0.0+build.7", constraint: "= 2.0.0", observed: "2.0.0+build.7", satisfied: true},
		{name: "exact build pin matches", output: "version: 2.0.0+build.7", constraint: "= 2.0.0+build.7", observed: "2.0.0+build.7", satisfied: true},
		{name: "exact build pin detects drift", output: "version: 2.0.0+build.8", constraint: "= 2.0.0+build.7", observed: "2.0.0+build.8", satisfied: false},
		{name: "prerelease precedes release", output: "version 2.0.0-rc.1", constraint: ">= 2.0.0", observed: "2.0.0-rc.1", satisfied: false},
		{name: "valid hyphenated prerelease", output: "version 2.0.0-alpha--preview", constraint: ">= 2.0.0-alpha", observed: "2.0.0-alpha--preview", satisfied: true},
		{name: "repeated version", output: "terraform-mcp-server v1.4.2 (version 1.4.2)", constraint: ">= 1.4.0", observed: "1.4.2", satisfied: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation, err := evaluateVersionConstraint(test.output, test.constraint)
			if err != nil {
				t.Fatalf("evaluateVersionConstraint: %v", err)
			}
			if evaluation.Observed != test.observed || evaluation.Satisfied != test.satisfied {
				t.Fatalf("evaluation = %+v, want observed %q satisfied %t", evaluation, test.observed, test.satisfied)
			}
		})
	}
}

func TestEvaluateVersionConstraintRejectsMissingVersion(t *testing.T) {
	_, err := evaluateVersionConstraint("terraform-mcp-server development build", ">= 1.0.0")

	if !errors.Is(err, errVersionNotFound) {
		t.Fatalf("expected errVersionNotFound, got %v", err)
	}
}

func TestEvaluateVersionConstraintRejectsAmbiguousVersions(t *testing.T) {
	_, err := evaluateVersionConstraint("build 2026.8.16, terraform-mcp-server 1.4.2", ">= 1.4.0")

	if !errors.Is(err, errVersionAmbiguous) {
		t.Fatalf("expected errVersionAmbiguous, got %v", err)
	}
}

func TestEvaluateVersionConstraintRejectsOversizedOutput(t *testing.T) {
	output := "terraform-mcp-server 1.4.2\n" + strings.Repeat("x", maxVersionProbeBytes)

	_, err := evaluateVersionConstraint(output, ">= 1.4.0")
	if !errors.Is(err, errVersionOutputTooLarge) {
		t.Fatalf("expected errVersionOutputTooLarge, got %v", err)
	}
}

func TestCheckFailsClosedForOutdatedVersion(t *testing.T) {
	manager := versionTestManager(t, ">= 1.4.0", "terraform-mcp-server v1.3.9")

	status, err := manager.Check(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.Ready || status.State != "outdated" || status.ObservedVersion != "1.3.9" {
		t.Fatalf("expected outdated status, got %+v", status)
	}
	if !hasCheck(status.Checks, "health_probe", "pass") || !hasCheck(status.Checks, "version_policy", "error") {
		t.Fatalf("expected successful probe and failed version policy, got %+v", status.Checks)
	}
}

func TestCheckAcceptsVersionThatMeetsPolicy(t *testing.T) {
	manager := versionTestManager(t, ">= 1.4.0", "terraform-mcp-server v1.4.2")

	status, err := manager.Check(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !status.Ready || status.State != "ready" || status.ObservedVersion != "1.4.2" {
		t.Fatalf("expected ready status, got %+v", status)
	}
	if !hasCheck(status.Checks, "version_policy", "pass") {
		t.Fatalf("expected successful version policy, got %+v", status.Checks)
	}
}

func TestCheckFailsClosedWhenVersionIsMissing(t *testing.T) {
	manager := versionTestManager(t, ">= 1.4.0", "terraform-mcp-server development build")

	status, err := manager.Check(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.Ready || status.State != "version_unknown" || status.ObservedVersion != "" {
		t.Fatalf("expected unverifiable version status, got %+v", status)
	}
	if !hasCheck(status.Checks, "version_policy", "error") {
		t.Fatalf("expected failed version policy, got %+v", status.Checks)
	}
	for _, check := range status.Checks {
		if check.Name == "version_policy" && check.Message != "probe output did not provide one unambiguous valid semantic version" {
			t.Fatalf("unexpected version policy message: %q", check.Message)
		}
	}
}

func TestInvalidVersionConstraintDoesNotExecuteProbe(t *testing.T) {
	probes := 0
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:                "terraform",
			Name:              "Terraform",
			Command:           testExecutable(t),
			HealthCheckArgs:   []string{"--version"},
			VersionConstraint: "^1.4.0",
			Trusted:           true,
			ReadOnlyDefault:   true,
			CredentialMode:    "none",
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
	if probes != 0 || status.State != "invalid_config" {
		t.Fatalf("invalid policy must fail before probing, got probes=%d status=%+v", probes, status)
	}
}

func TestCheckFailsClosedWhenVersionConstraintHasNoProbe(t *testing.T) {
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:                "terraform",
			Name:              "Terraform",
			Command:           testExecutable(t),
			VersionConstraint: ">= 1.4.0",
			Trusted:           true,
			ReadOnlyDefault:   true,
			CredentialMode:    "none",
		}}),
	)

	status, err := manager.Check(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.Ready || status.State != "version_unknown" || status.ObservedVersion != "" {
		t.Fatalf("expected version_unknown when no probe args are configured, got %+v", status)
	}
	if !hasCheck(status.Checks, "version_policy", "error") {
		t.Fatalf("expected failed version policy check, got %+v", status.Checks)
	}
}

func TestCheckPreservesVersionFailureForRunningServer(t *testing.T) {
	handle := newFakeProcess()
	manager := NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:                "terraform",
			Name:              "Terraform",
			Command:           testExecutable(t),
			HealthCheckArgs:   []string{"--version"},
			VersionConstraint: ">= 1.4.0",
			Trusted:           true,
			ReadOnlyDefault:   true,
			CredentialMode:    "none",
		}}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			return ProbeResult{Output: "terraform-mcp-server 1.3.9"}
		}),
		WithLauncher(func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
			return handle, nil
		}),
	)
	t.Cleanup(func() { _ = manager.Close() })

	started, err := manager.Start(context.Background(), "terraform")
	if err != nil || !started.Running {
		t.Fatalf("Start: status=%+v err=%v", started, err)
	}
	status, err := manager.Check(context.Background(), "terraform")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !status.Running || status.Ready || status.State != "outdated" {
		t.Fatalf("expected running process with failed readiness, got %+v", status)
	}
	if !hasCheck(status.Checks, "version_policy", "error") || !hasCheck(status.Checks, "lifecycle", "warn") {
		t.Fatalf("expected version failure and lifecycle warning, got %+v", status.Checks)
	}
}

func versionTestManager(t *testing.T, constraint, output string) *Manager {
	t.Helper()
	return NewManager(t.TempDir(),
		WithDefinitions([]ServerDefinition{{
			ID:                "terraform",
			Name:              "Terraform",
			Command:           testExecutable(t),
			HealthCheckArgs:   []string{"--version"},
			VersionConstraint: constraint,
			Trusted:           true,
			ReadOnlyDefault:   true,
			CredentialMode:    "none",
		}}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			return ProbeResult{Output: output}
		}),
	)
}
