package pulumi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectNameFromDir_DefaultsWhenBasenameSanitizesEmpty(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "___")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	if got := ProjectNameFromDir(dir); got != "iac-studio" {
		t.Fatalf("ProjectNameFromDir() = %q, want iac-studio", got)
	}
}

func TestProjectNameFromDir_PrefixesLeadingDigit(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "123-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	if got := ProjectNameFromDir(dir); got != "iac-123-demo" {
		t.Fatalf("ProjectNameFromDir() = %q, want iac-123-demo", got)
	}
}
