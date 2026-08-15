package mcpairlock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApproveExecutablePersistsServerDerivedFingerprint(t *testing.T) {
	root := t.TempDir()
	command := testExecutable(t)
	expected := executableFingerprintForTest(t, command)
	probes := 0
	launches := 0
	manager := NewManager(root,
		WithDefinitions([]ServerDefinition{executableApprovalDefinition(command)}),
		WithProbe(func(context.Context, string, []string, time.Duration) ProbeResult {
			probes++
			return ProbeResult{}
		}),
		WithLauncher(func(context.Context, ServerDefinition, time.Duration) (ProcessHandle, error) {
			launches++
			return nil, errors.New("launcher must not run during approval")
		}),
	)

	attestation, err := manager.ApproveExecutable(context.Background(), "terraform", expected)
	if err != nil {
		t.Fatalf("ApproveExecutable: %v", err)
	}
	if attestation.ServerID != "terraform" || attestation.LaunchSource != LaunchSourceExplicitDefinition {
		t.Fatalf("unexpected approval identity: %+v", attestation)
	}
	if attestation.Fingerprint != expected {
		t.Fatalf("unexpected approval fingerprint: %+v", attestation.Fingerprint)
	}
	if attestation.ApprovedAt.IsZero() || attestation.ApprovedAt.Location() != time.UTC {
		t.Fatalf("unexpected approval timestamp: %v", attestation.ApprovedAt)
	}
	if probes != 0 || launches != 0 {
		t.Fatalf("approval executed external processes: probes=%d launches=%d", probes, launches)
	}

	store, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := store.Get(attestation.ServerID, attestation.LaunchSource)
	if !ok || stored != attestation {
		t.Fatalf("stored approval = (%+v, %t), want %+v", stored, ok, attestation)
	}
}

func TestApproveExecutableFailsClosed(t *testing.T) {
	command := testExecutable(t)
	expected := executableFingerprintForTest(t, command)

	t.Run("unknown server", func(t *testing.T) {
		manager := NewManager(t.TempDir(), WithDefinitions(nil))
		if _, err := manager.ApproveExecutable(context.Background(), "unknown", expected); !errors.Is(err, ErrUnknownServer) {
			t.Fatalf("ApproveExecutable error = %v, want ErrUnknownServer", err)
		}
	})

	t.Run("invalid expected fingerprint", func(t *testing.T) {
		manager := NewManager(t.TempDir(), WithDefinitions([]ServerDefinition{executableApprovalDefinition(command)}))
		if _, err := manager.ApproveExecutable(context.Background(), "terraform", ExecutableFingerprint{}); !errors.Is(err, ErrInvalidExecutableFingerprint) {
			t.Fatalf("ApproveExecutable error = %v, want ErrInvalidExecutableFingerprint", err)
		}
	})

	t.Run("storage not configured", func(t *testing.T) {
		manager := NewManager("", WithDefinitions([]ServerDefinition{executableApprovalDefinition(command)}))
		if _, err := manager.ApproveExecutable(context.Background(), "terraform", expected); !errors.Is(err, ErrExecutableApprovalUnavailable) {
			t.Fatalf("ApproveExecutable error = %v, want ErrExecutableApprovalUnavailable", err)
		}
	})

	t.Run("blocked definition", func(t *testing.T) {
		root := t.TempDir()
		definition := executableApprovalDefinition(command)
		definition.Trusted = false
		manager := NewManager(root, WithDefinitions([]ServerDefinition{definition}))
		if _, err := manager.ApproveExecutable(context.Background(), "terraform", expected); !errors.Is(err, ErrExecutableApprovalUnavailable) {
			t.Fatalf("ApproveExecutable error = %v, want ErrExecutableApprovalUnavailable", err)
		}
		assertApprovalStoreNotCreated(t, root)
	})

	t.Run("missing executable", func(t *testing.T) {
		root := t.TempDir()
		manager := NewManager(root, WithDefinitions([]ServerDefinition{executableApprovalDefinition(filepath.Join(root, "missing"))}))
		if _, err := manager.ApproveExecutable(context.Background(), "terraform", expected); !errors.Is(err, ErrExecutableApprovalUnavailable) {
			t.Fatalf("ApproveExecutable error = %v, want ErrExecutableApprovalUnavailable", err)
		}
		assertApprovalStoreNotCreated(t, root)
	})

	t.Run("invalid store", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, ".iac-studio", attestationStoreFileName)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		want := []byte("{")
		if err := os.WriteFile(path, want, 0o600); err != nil {
			t.Fatal(err)
		}
		manager := NewManager(root, WithDefinitions([]ServerDefinition{executableApprovalDefinition(command)}))
		if _, err := manager.ApproveExecutable(context.Background(), "terraform", expected); !errors.Is(err, ErrExecutableApprovalUnavailable) {
			t.Fatalf("ApproveExecutable error = %v, want ErrExecutableApprovalUnavailable", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("invalid store was overwritten: %q", got)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		root := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		manager := NewManager(root, WithDefinitions([]ServerDefinition{executableApprovalDefinition(command)}))
		if _, err := manager.ApproveExecutable(ctx, "terraform", expected); !errors.Is(err, context.Canceled) {
			t.Fatalf("ApproveExecutable error = %v, want context.Canceled", err)
		}
		assertApprovalStoreNotCreated(t, root)
	})
}

func TestApproveExecutableRejectsChangedFingerprintBeforePersistence(t *testing.T) {
	root := t.TempDir()
	command := testExecutable(t)
	manager := NewManager(root, WithDefinitions([]ServerDefinition{executableApprovalDefinition(command)}))
	expected := executableFingerprintForTest(t, command)
	replacement := "0"
	if expected.Digest[0] == '0' {
		replacement = "1"
	}
	expected.Digest = replacement + expected.Digest[1:]

	if _, err := manager.ApproveExecutable(context.Background(), "terraform", expected); !errors.Is(err, ErrExecutableFingerprintMismatch) {
		t.Fatalf("ApproveExecutable error = %v, want ErrExecutableFingerprintMismatch", err)
	}
	assertApprovalStoreNotCreated(t, root)
}

func TestExecutableApprovalPersistenceErrorIsDiagnosticAndSanitized(t *testing.T) {
	invalid := executableApprovalPersistenceError(fmt.Errorf("%w: store changed since it was loaded", ErrInvalidAttestationStore))
	if !strings.Contains(invalid.Error(), "invalid, changed, or full") {
		t.Fatalf("invalid store error = %q", invalid)
	}

	secretPath := "/private/credentials/mcp-airlock-attestations.json"
	pathErr := executableApprovalPersistenceError(&os.PathError{Op: "rename", Path: secretPath, Err: os.ErrPermission})
	if strings.Contains(pathErr.Error(), secretPath) {
		t.Fatalf("persistence error disclosed store path: %q", pathErr)
	}
	if !errors.Is(pathErr, ErrExecutableApprovalUnavailable) {
		t.Fatalf("persistence error = %v, want ErrExecutableApprovalUnavailable", pathErr)
	}
}

func executableApprovalDefinition(command string) ServerDefinition {
	return ServerDefinition{
		ID:              "terraform",
		Name:            "Terraform",
		Command:         command,
		Transport:       "stdio",
		Trusted:         true,
		ReadOnlyDefault: true,
		CredentialMode:  "none",
	}
}

func assertApprovalStoreNotCreated(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, ".iac-studio", attestationStoreFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("approval store unexpectedly exists: %v", err)
	}
}

func executableFingerprintForTest(t *testing.T, path string) ExecutableFingerprint {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	return ExecutableFingerprint{Algorithm: "sha256", Digest: hex.EncodeToString(digest[:])}
}
