package scaffold

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestRegistryListAndGet verifies the package-level default registry exposes
// the bundled layered-terraform blueprint.
func TestRegistryListAndGet(t *testing.T) {
	if _, ok := Default.Get("layered-terraform"); !ok {
		t.Fatal("layered-terraform blueprint not registered in Default registry")
	}

	ids := make([]string, 0)
	for _, bp := range Default.List() {
		ids = append(ids, bp.ID())
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		t.Fatal("expected at least one registered blueprint")
	}
}

// TestLayeredTerraformRendersExpectedTree asserts the layered blueprint
// produces every file the canonical layered layout requires.
func TestLayeredTerraformRendersExpectedTree(t *testing.T) {
	bp := &LayeredTerraformBlueprint{}
	values := map[string]any{
		"project_name": "acme",
		"cloud":        "aws",
		"environments": []any{"dev", "prod"},
		"modules":      []any{"networking", "compute", "database", "security", "monitoring"},
		"backend":      "s3",
		"state_bucket": "acme-tfstate",
		"state_region": "us-east-1",
		"owner_tag":    "platform",
	}
	files, err := bp.Render(values)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	got := map[string]File{}
	for _, f := range files {
		got[f.Path] = f
	}

	want := []string{
		"README.md",
		".gitignore",
		".iac-studio.json",
		"environments/dev/main.tf",
		"environments/dev/variables.tf",
		"environments/dev/outputs.tf",
		"environments/dev/terraform.tfvars",
		"environments/dev/backend.tf",
		"environments/prod/main.tf",
		"environments/prod/backend.tf",
		"modules/networking/main.tf",
		"modules/networking/variables.tf",
		"modules/networking/outputs.tf",
		"modules/networking/versions.tf",
		"modules/networking/README.md",
		"modules/compute/main.tf",
		"modules/database/main.tf",
		"modules/security/main.tf",
		"modules/monitoring/main.tf",
		"policies/sentinel/cost-control.sentinel",
		"policies/sentinel/security-baseline.sentinel",
		"policies/sentinel/naming-conventions.sentinel",
		"policies/opa/resource-tagging.rego",
		"policies/opa/network-rules.rego",
		"policies/opa/compliance.rego",
		"scripts/init.sh",
		"scripts/plan.sh",
		"scripts/apply.sh",
		"scripts/destroy.sh",
		"scripts/validate.sh",
	}

	for _, path := range want {
		if _, ok := got[path]; !ok {
			t.Errorf("expected file missing from render output: %s", path)
		}
	}

	// Scripts must be marked executable so Write sets the +x bit.
	for path, f := range got {
		if strings.HasPrefix(path, "scripts/") && !f.Executable {
			t.Errorf("script %s should be executable", path)
		}
	}
}

// TestLayeredTerraformRequiresProjectName confirms the renderer rejects a
// blank project name rather than emitting garbage HCL.
func TestLayeredTerraformRequiresProjectName(t *testing.T) {
	bp := &LayeredTerraformBlueprint{}
	_, err := bp.Render(map[string]any{})
	if err == nil {
		t.Fatal("expected error when project_name is missing")
	}
}

// TestLayeredTerraformBackendVariants exercises the three supported remote
// state backends to make sure each produces a syntactically plausible block.
func TestLayeredTerraformBackendVariants(t *testing.T) {
	cases := []struct {
		backend string
		needle  string
	}{
		{"s3", `backend "s3"`},
		{"gcs", `backend "gcs"`},
		{"azurerm", `backend "azurerm"`},
	}
	for _, tc := range cases {
		t.Run(tc.backend, func(t *testing.T) {
			bp := &LayeredTerraformBlueprint{}
			files, err := bp.Render(map[string]any{
				"project_name": "acme",
				"cloud":        "aws",
				"backend":      tc.backend,
			})
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			var backendBody string
			for _, f := range files {
				if f.Path == "environments/dev/backend.tf" {
					backendBody = string(f.Content)
					break
				}
			}
			if !strings.Contains(backendBody, tc.needle) {
				t.Errorf("backend.tf for %s missing %q; got:\n%s", tc.backend, tc.needle, backendBody)
			}
		})
	}
}

// TestWriteRefusesOverwrite guards against clobbering user work in a non-empty
// target directory — existing files cause Write to fail cleanly.
func TestWriteRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "README.md")
	if err := os.WriteFile(existing, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seeding existing file: %v", err)
	}

	files := []File{
		{Path: "README.md", Content: []byte("new content")},
	}
	err := Write(dir, files)
	if err == nil {
		t.Fatal("expected Write to refuse overwriting existing files")
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Errorf("error should name the conflicting path; got: %v", err)
	}
	// Original content must be preserved.
	b, _ := os.ReadFile(existing)
	if string(b) != "keep me" {
		t.Errorf("existing file was clobbered; got %q", string(b))
	}
}

// TestWriteMaterialisesTree confirms Write creates nested directories and sets
// the executable bit for files that request it.
func TestWriteMaterialisesTree(t *testing.T) {
	dir := t.TempDir()
	files := []File{
		{Path: "nested/dir/file.tf", Content: []byte("terraform {}")},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
	}
	if err := Write(dir, files); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	tf := filepath.Join(dir, "nested/dir/file.tf")
	if _, err := os.Stat(tf); err != nil {
		t.Errorf("nested file not written: %v", err)
	}

	sh := filepath.Join(dir, "scripts/run.sh")
	info, err := os.Stat(sh)
	if err != nil {
		t.Fatalf("script not written: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("script %s missing executable bit; mode=%v", sh, info.Mode().Perm())
	}
}
