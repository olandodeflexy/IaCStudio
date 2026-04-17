package scaffold

import (
	"os"
	"path/filepath"
	"runtime"
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

// TestLayeredTerraformRejectsUnsafeInputs exercises the conservative
// character-set validator on the free-form string inputs that flow into HCL
// strings and cloud resource names.
func TestLayeredTerraformRejectsUnsafeInputs(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
	}{
		{"quote in project_name", map[string]any{"project_name": `acme"; hack`}},
		{"newline in project_name", map[string]any{"project_name": "acme\nrm -rf"}},
		{"uppercase project_name", map[string]any{"project_name": "ACME"}},
		{"empty project_name", map[string]any{"project_name": ""}},
		{"two-char project_name below S3 floor", map[string]any{"project_name": "ab"}},
		{"bad state_bucket", map[string]any{"project_name": "acme", "state_bucket": `bad";drop`}},
		{"hyphen state_bucket on azurerm", map[string]any{
			"project_name": "acme", "backend": "azurerm", "state_bucket": "acme-tfstate",
		}},
		{"too-long state_bucket on azurerm", map[string]any{
			"project_name": "acme", "backend": "azurerm", "state_bucket": "acmethisisabsolutelywaytoolongforazure",
		}},
		{"quote in owner_tag", map[string]any{"project_name": "acme", "owner_tag": `ops" injected`}},
		{"newline in owner_tag", map[string]any{"project_name": "acme", "owner_tag": "ops\nrm"}},
		{"quote in cost_center_tag", map[string]any{"project_name": "acme", "cost_center_tag": `shared"`}},
	}
	bp := &LayeredTerraformBlueprint{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := bp.Render(tc.values); err == nil {
				t.Fatalf("expected validation error, got nil")
			}
		})
	}
}

// TestWriteRejectsDuplicatePaths verifies Write fails fast when the same
// cleaned path appears twice in the same batch — without this, the second
// WriteFile would silently clobber the first.
func TestWriteRejectsDuplicatePaths(t *testing.T) {
	dir := t.TempDir()
	err := Write(dir, []File{
		{Path: "a.tf", Content: []byte("first")},
		{Path: "a.tf", Content: []byte("second")},
	})
	if err == nil {
		t.Fatal("expected duplicate-path error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate paths; got: %v", err)
	}
}

// TestLayeredTerraformNonAWSOutputsDoNotReferenceAWS guards against issue #6:
// for GCP/Azure renders, module outputs.tf must not reference aws_* resources
// (they don't exist in the corresponding main.tf skeletons, so `terraform
// validate` would fail).
func TestLayeredTerraformNonAWSOutputsDoNotReferenceAWS(t *testing.T) {
	for _, cloud := range []string{"gcp", "azure"} {
		t.Run(cloud, func(t *testing.T) {
			bp := &LayeredTerraformBlueprint{}
			files, err := bp.Render(map[string]any{
				"project_name": "acme",
				"cloud":        cloud,
			})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, f := range files {
				if !strings.HasPrefix(f.Path, "modules/") || !strings.HasSuffix(f.Path, "/outputs.tf") {
					continue
				}
				if strings.Contains(string(f.Content), "aws_") {
					t.Errorf("%s (%s) still references aws_ resources:\n%s", f.Path, cloud, f.Content)
				}
			}
		})
	}
}

// TestLayeredTerraformValidateScriptInitsEnvRoots guards against issue #5:
// the generated validate.sh must terraform init each environment root (with
// -backend=false) before running terraform validate, otherwise the script
// fails on a fresh checkout.
func TestLayeredTerraformValidateScriptInitsEnvRoots(t *testing.T) {
	bp := &LayeredTerraformBlueprint{}
	files, err := bp.Render(map[string]any{"project_name": "acme"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var script string
	for _, f := range files {
		if f.Path == "scripts/validate.sh" {
			script = string(f.Content)
			break
		}
	}
	if script == "" {
		t.Fatal("scripts/validate.sh missing from render output")
	}
	// The env loop must init before validate. We look for the ordered substring
	// so either -backend=false or -input=false reordering is allowed as long as
	// init precedes validate within the env loop.
	envLoop := "for env in environments/"
	if !strings.Contains(script, envLoop) {
		t.Fatal("env loop not found in validate.sh")
	}
	loopBody := script[strings.Index(script, envLoop):]
	if !strings.Contains(loopBody, "terraform init -backend=false") {
		t.Errorf("env loop in validate.sh does not terraform init before validate:\n%s", script)
	}
}

// TestLayeredTerraformBackendVariants exercises the three supported remote
// state backends to make sure each produces a syntactically plausible block.
func TestLayeredTerraformBackendVariants(t *testing.T) {
	cases := []struct {
		backend     string
		needle      string
		stateBucket string // override default when the backend has stricter rules
	}{
		{backend: "s3", needle: `backend "s3"`},
		{backend: "gcs", needle: `backend "gcs"`},
		// Azure storage account names must be 3-24 lowercase alnum, no hyphens.
		{backend: "azurerm", needle: `backend "azurerm"`, stateBucket: "acmestate01"},
	}
	for _, tc := range cases {
		t.Run(tc.backend, func(t *testing.T) {
			bp := &LayeredTerraformBlueprint{}
			values := map[string]any{
				"project_name": "acme",
				"cloud":        "aws",
				"backend":      tc.backend,
			}
			if tc.stateBucket != "" {
				values["state_bucket"] = tc.stateBucket
			}
			files, err := bp.Render(values)
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
	// POSIX executable bit isn't represented the same way on Windows, where
	// chmod is largely a no-op — skip this assertion there rather than fail.
	if runtime.GOOS == "windows" {
		return
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("script %s missing executable bit; mode=%v", sh, info.Mode().Perm())
	}
}
