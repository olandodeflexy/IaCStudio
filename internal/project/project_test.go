package project

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSavePreservesLayeredMetadata(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	initial := []byte(`{
  "layout": "layered-v1",
  "tool": "terraform",
  "cloud": "aws",
  "environments": ["dev", "prod"],
  "environment_tools": {"dev": "pulumi", "prod": "terraform"},
  "modules": ["networking", "compute"],
  "tags": {"ManagedBy": "iac-studio"}
}`)
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio.json"), initial, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	manager := NewManager(root)
	state, err := manager.Load("demo")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if state.Name != "demo" {
		t.Fatalf("expected loaded state name to be filled, got %q", state.Name)
	}
	if state.Path != projectDir {
		t.Fatalf("expected loaded state path to be filled, got %q", state.Path)
	}
	if state.Layout != "layered-v1" {
		t.Fatalf("expected layered layout, got %q", state.Layout)
	}
	if state.EnvTools["dev"] != "pulumi" || state.EnvTools["prod"] != "terraform" {
		t.Fatalf("expected environment tool map to be preserved, got %+v", state.EnvTools)
	}
	if !bytes.Contains(state.Modules, []byte(`"networking"`)) {
		t.Fatalf("expected raw module metadata to be preserved, got %s", string(state.Modules))
	}

	state.Resources = []Node{{ID: "aws_vpc.main", Type: "aws_vpc", Name: "main"}}
	if err := manager.Save("demo", state); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(projectDir, ".iac-studio.json"))
	if err != nil {
		t.Fatalf("read saved state: %v", err)
	}
	for _, needle := range [][]byte{
		[]byte(`"layout": "layered-v1"`),
		[]byte(`"environments": [`),
		[]byte(`"environment_tools": {`),
		[]byte(`"networking"`),
		[]byte(`"tags": {`),
	} {
		if !bytes.Contains(saved, needle) {
			t.Fatalf("saved state lost metadata %s:\n%s", string(needle), string(saved))
		}
	}
}
