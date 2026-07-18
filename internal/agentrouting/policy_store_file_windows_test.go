//go:build windows

package agentrouting

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPolicyStoreDataFileAllowsAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "policies.json")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile(destination): %v", err)
	}

	reader, err := openPolicyStoreDataFile(destination)
	if err != nil {
		t.Fatalf("openPolicyStoreDataFile(): %v", err)
	}
	defer reader.Close()

	source := filepath.Join(dir, "replacement.json")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile(source): %v", err)
	}
	if err := replacePolicyStoreFile(source, destination); err != nil {
		t.Fatalf("replacePolicyStoreFile() with open reader: %v", err)
	}

	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination): %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("destination contents = %q, want new", data)
	}
}
