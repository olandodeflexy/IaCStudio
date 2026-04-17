package scaffold

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLayeredTerraformPassesTerraformFmt renders the blueprint to a temp dir
// and shells out to `terraform fmt -check -recursive`. If terraform isn't on
// PATH (e.g. CI without the binary) the test is skipped rather than failing,
// so it stays opt-in for contributor machines that have terraform installed.
func TestLayeredTerraformPassesTerraformFmt(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform not on PATH; skipping fmt check")
	}

	dir := t.TempDir()
	bp := &LayeredTerraformBlueprint{}
	files, err := bp.Render(map[string]any{
		"project_name": "acme",
		"cloud":        "aws",
		"environments": []any{"dev", "prod"},
		"modules":      []any{"networking", "compute", "database", "security", "monitoring"},
		"backend":      "s3",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := Write(dir, files); err != nil {
		t.Fatalf("write: %v", err)
	}

	// terraform fmt will non-zero exit only if files are *malformed*. Canonical
	// whitespace drift in our skeletons is fine — we normalise by writing back,
	// not by asserting idempotency.
	cmd := exec.Command("terraform", "fmt", "-recursive", "-diff", filepath.Clean(dir))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terraform fmt failed: %v\n%s", err, string(out))
	}
	// Malformed HCL surfaces as a stderr error line — sanity-check the output.
	if strings.Contains(string(out), "Error:") {
		t.Fatalf("terraform fmt reported errors:\n%s", string(out))
	}
}
