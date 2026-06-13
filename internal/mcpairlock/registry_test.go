package mcpairlock

import (
	"context"
	"errors"
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

func containsStatus(statuses []ServerStatus, id string) bool {
	for _, status := range statuses {
		if status.Server.ID == id {
			return true
		}
	}
	return false
}
