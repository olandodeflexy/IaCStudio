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

	// Build the expected paths programmatically from the same envs/modules
	// list passed to Render, so adding an env or module in the blueprint
	// doesn't silently slip past this regression check.
	envs := []string{"dev", "prod"}
	modules := []string{"networking", "compute", "database", "security", "monitoring"}
	envFiles := []string{"main.tf", "variables.tf", "outputs.tf", "terraform.tfvars", "backend.tf"}
	moduleFiles := []string{"main.tf", "variables.tf", "outputs.tf", "versions.tf", "README.md"}

	want := []string{
		"README.md",
		".gitignore",
		".iac-studio.json",
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
	for _, env := range envs {
		for _, name := range envFiles {
			want = append(want, "environments/"+env+"/"+name)
		}
	}
	for _, mod := range modules {
		for _, name := range moduleFiles {
			want = append(want, "modules/"+mod+"/"+name)
		}
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

// TestLayeredTerraformAzureDefaultStateBucket confirms that omitting
// state_bucket with backend="azurerm" still yields a valid Azure storage
// account name (3-24 lowercase alnum) instead of the hyphenated generic
// default that would fail validation.
func TestLayeredTerraformAzureDefaultStateBucket(t *testing.T) {
	bp := &LayeredTerraformBlueprint{}
	_, err := bp.Render(map[string]any{
		"project_name": "acme-corp",
		"backend":      "azurerm",
		// state_bucket intentionally omitted — the default must be Azure-safe.
	})
	if err != nil {
		t.Fatalf("default state_bucket should be Azure-safe, got: %v", err)
	}

	if got := defaultStateBucket("acme-corp", "azurerm"); !azureStorageAccountRE.MatchString(got) {
		t.Errorf("defaultStateBucket(acme-corp, azurerm) = %q, not Azure-valid", got)
	}
	if got := defaultStateBucket("acme-corp", "azurerm"); strings.Contains(got, "-") {
		t.Errorf("Azure default must not contain hyphens, got %q", got)
	}
	if got := defaultStateBucket("this-is-a-very-long-project-name", "azurerm"); len(got) > 24 {
		t.Errorf("Azure default must be ≤24 chars, got %d (%q)", len(got), got)
	}
	if got := defaultStateBucket("acme", "s3"); got != "acme-tfstate" {
		t.Errorf("s3 default should keep hyphen form, got %q", got)
	}
}

// TestLayeredTerraformRejectsPathTraversalInEnvsModules prevents env/module
// names that would escape the project directory or break HCL.
func TestLayeredTerraformRejectsPathTraversalInEnvsModules(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
	}{
		{"slash in env", map[string]any{"project_name": "acme", "environments": []any{"../scripts"}}},
		{"dot in env", map[string]any{"project_name": "acme", "environments": []any{".hidden"}}},
		{"space in env", map[string]any{"project_name": "acme", "environments": []any{"dev env"}}},
		{"slash in module", map[string]any{"project_name": "acme", "modules": []any{"net/work"}}},
		{"uppercase module", map[string]any{"project_name": "acme", "modules": []any{"Networking"}}},
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

// TestLayeredTerraformBackendNoneSkipsStateBucketValidation confirms that
// selecting backend="none" disables state_bucket validation entirely — the
// value isn't rendered, so rejecting it would be user-hostile.
func TestLayeredTerraformBackendNoneSkipsStateBucketValidation(t *testing.T) {
	bp := &LayeredTerraformBlueprint{}
	_, err := bp.Render(map[string]any{
		"project_name": "acme",
		"backend":      "none",
		// An otherwise-invalid value that would normally be rejected:
		"state_bucket": `bad"; value`,
	})
	if err != nil {
		t.Fatalf("backend=none should skip state_bucket validation, got: %v", err)
	}
}

// TestLayeredTerraformRejectsUnsafeStateRegion guards against a state_region
// value that would break HCL (e.g. an injected quote) making it into the
// rendered backend.tf.
func TestLayeredTerraformRejectsUnsafeStateRegion(t *testing.T) {
	bp := &LayeredTerraformBlueprint{}
	_, err := bp.Render(map[string]any{
		"project_name": "acme",
		"state_region": `us-east-1"; evil = "x`,
	})
	if err == nil {
		t.Fatal("expected state_region validation error, got nil")
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
