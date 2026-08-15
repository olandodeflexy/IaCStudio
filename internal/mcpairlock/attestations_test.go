package mcpairlock

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecutableAttestationStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	store, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatalf("NewExecutableAttestationStore: %v", err)
	}
	terraform := testAttestation("terraform-official", LaunchSourceRegistry, "ab")
	aws := testAttestation("aws-official", LaunchSourceExplicitDefinition, "cd")
	if err := store.Save(terraform); err != nil {
		t.Fatalf("Save terraform: %v", err)
	}
	if err := store.Save(aws); err != nil {
		t.Fatalf("Save aws: %v", err)
	}

	path := filepath.Join(root, ".iac-studio", attestationStoreFileName)
	assertPrivateAttestationPath(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	awsIndex := strings.Index(string(data), `"server_id": "aws-official"`)
	terraformIndex := strings.Index(string(data), `"server_id": "terraform-official"`)
	if awsIndex < 0 || terraformIndex < 0 || awsIndex > terraformIndex {
		t.Fatalf("attestations are not persisted deterministically: %s", data)
	}

	reloaded, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := reloaded.Get(terraform.ServerID, terraform.LaunchSource)
	if !ok || got != terraform {
		t.Fatalf("Get() = (%+v, %t), want %+v", got, ok, terraform)
	}
	if _, ok := reloaded.Get(terraform.ServerID, LaunchSourceEnvironmentOverride); ok {
		t.Fatal("attestation leaked across launch provenance")
	}
}

func TestExecutableAttestationStoreReplacesExactKey(t *testing.T) {
	root := t.TempDir()
	store, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	original := testAttestation("terraform-official", LaunchSourceRegistry, "ab")
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	updated := testAttestation("terraform-official", LaunchSourceRegistry, "ef")
	updated.ApprovedAt = original.ApprovedAt.Add(time.Minute)
	if err := store.Save(updated); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(updated.ServerID, updated.LaunchSource)
	if !ok || got != updated {
		t.Fatalf("Get() = (%+v, %t), want %+v", got, ok, updated)
	}
}

func TestExecutableAttestationStoreRejectsInvalidSnapshots(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	record := `{"server_id":"terraform-official","launch_source":"registry","fingerprint":{"algorithm":"sha256","digest":"` + digest + `"},"approved_at":"2026-08-15T12:00:00Z"}`
	tests := []struct {
		name string
		data string
	}{
		{name: "unknown field", data: `{"version":1,"attestations":[],"extra":true}`},
		{name: "unsupported version", data: `{"version":2,"attestations":[]}`},
		{name: "missing attestations", data: `{"version":1}`},
		{name: "null attestations", data: `{"version":1,"attestations":null}`},
		{name: "trailing data", data: `{"version":1,"attestations":[]} {}`},
		{name: "duplicate key", data: `{"version":1,"attestations":[` + record + `,` + record + `]}`},
		{name: "unsupported launch source", data: strings.Replace(`{"version":1,"attestations":[`+record+`]}`, `"registry"`, `"unknown"`, 1)},
		{name: "unsupported algorithm", data: strings.Replace(`{"version":1,"attestations":[`+record+`]}`, `"sha256"`, `"sha1"`, 1)},
		{name: "uppercase digest", data: strings.Replace(`{"version":1,"attestations":[`+record+`]}`, digest, strings.ToUpper(digest), 1)},
		{name: "missing timestamp", data: strings.Replace(`{"version":1,"attestations":[`+record+`]}`, `"approved_at":"2026-08-15T12:00:00Z"`, `"approved_at":null`, 1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeAttestationSnapshot(t, root, []byte(test.data))
			_, err := NewExecutableAttestationStore(root)
			if !errors.Is(err, ErrInvalidAttestationStore) {
				t.Fatalf("error = %v, want ErrInvalidAttestationStore", err)
			}
		})
	}
}

func TestExecutableAttestationStoreRejectsOversizedAndSymlinkedFiles(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		root := t.TempDir()
		writeAttestationSnapshot(t, root, []byte(strings.Repeat("x", maxAttestationStoreBytes+1)))
		_, err := NewExecutableAttestationStore(root)
		if !errors.Is(err, ErrInvalidAttestationStore) {
			t.Fatalf("error = %v, want ErrInvalidAttestationStore", err)
		}
	})

	t.Run("symlinked", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target.json")
		if err := os.WriteFile(target, []byte(`{"version":1,"attestations":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, ".iac-studio")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, attestationStoreFileName)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		_, err := NewExecutableAttestationStore(root)
		if !errors.Is(err, ErrInvalidAttestationStore) {
			t.Fatalf("error = %v, want ErrInvalidAttestationStore", err)
		}
	})
}

func TestExecutableAttestationStoreSaveFailurePreservesState(t *testing.T) {
	root := t.TempDir()
	store, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	original := testAttestation("terraform-official", LaunchSourceRegistry, "ab")
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	originalPath := store.path
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blocker, attestationStoreFileName)
	updated := testAttestation("terraform-official", LaunchSourceRegistry, "cd")
	if err := store.Save(updated); err == nil {
		t.Fatal("expected Save to fail")
	}
	got, ok := store.Get(original.ServerID, original.LaunchSource)
	if !ok || got != original {
		t.Fatalf("failed save changed in-memory state: (%+v, %t)", got, ok)
	}

	store.path = originalPath
	reloaded, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok = reloaded.Get(original.ServerID, original.LaunchSource)
	if !ok || got != original {
		t.Fatalf("failed save changed durable state: (%+v, %t)", got, ok)
	}
}

func TestExecutableAttestationStoreRejectsStaleWriter(t *testing.T) {
	root := t.TempDir()
	first, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	original := testAttestation("terraform-official", LaunchSourceRegistry, "ab")
	if err := first.Save(original); err != nil {
		t.Fatal(err)
	}
	if err := stale.Save(testAttestation("aws-official", LaunchSourceRegistry, "cd")); !errors.Is(err, ErrInvalidAttestationStore) {
		t.Fatalf("stale Save error = %v, want ErrInvalidAttestationStore", err)
	}

	reloaded, err := NewExecutableAttestationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(original.ServerID, original.LaunchSource); !ok {
		t.Fatal("stale writer replaced the durable attestation")
	}
	if _, ok := reloaded.Get("aws-official", LaunchSourceRegistry); ok {
		t.Fatal("stale writer persisted an unvalidated merge")
	}
}

func TestExecutableAttestationStoreRequiresPath(t *testing.T) {
	for _, path := range []string{"", "   "} {
		if _, err := NewExecutableAttestationStore(path); !errors.Is(err, ErrAttestationStorePathRequired) {
			t.Fatalf("NewExecutableAttestationStore(%q) error = %v", path, err)
		}
	}
	store := &ExecutableAttestationStore{}
	if err := store.Save(testAttestation("terraform-official", LaunchSourceRegistry, "ab")); !errors.Is(err, ErrAttestationStorePathRequired) {
		t.Fatalf("zero-value Save error = %v", err)
	}
}

func testAttestation(serverID, launchSource, digestPair string) ExecutableAttestation {
	return ExecutableAttestation{
		ServerID:     serverID,
		LaunchSource: launchSource,
		Fingerprint: ExecutableFingerprint{
			Algorithm: "sha256",
			Digest:    strings.Repeat(digestPair, 32),
		},
		ApprovedAt: time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC),
	}
}

func writeAttestationSnapshot(t *testing.T, root string, data []byte) {
	t.Helper()
	dir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, attestationStoreFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertPrivateAttestationPath(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", fileInfo.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %o, want 700", dirInfo.Mode().Perm())
	}
}
