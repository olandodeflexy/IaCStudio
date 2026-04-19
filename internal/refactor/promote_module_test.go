package refactor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldProject builds a tiny one-file Terraform project for the
// refactor tests — three resources, all in main.tf.
func scaffoldProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed main.tf: %v", err)
	}
	return dir
}

// TestPromoteToModuleGoldenPath covers the end-to-end refactor: two
// resources are extracted into a new module, the original file loses them,
// and the root's main.tf gains a module call.
func TestPromoteToModuleGoldenPath(t *testing.T) {
	dir := scaffoldProject(t, `resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_subnet" "public_1" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.1.0/24"
}

resource "aws_s3_bucket" "logs" {
  bucket = "app-logs"
}
`)

	result, err := PromoteToModule(PromoteRequest{
		ProjectDir:  dir,
		ModuleName:  "networking",
		ResourceIDs: []string{"aws_vpc.main", "aws_subnet.public_1"},
	})
	if err != nil {
		t.Fatalf("PromoteToModule: %v", err)
	}

	// Result shape
	if !strings.HasSuffix(result.ModulePath, "modules/networking") {
		t.Errorf("ModulePath wrong: %q", result.ModulePath)
	}
	if len(result.ResourcesMoved) != 2 {
		t.Errorf("want 2 moved resources, got %+v", result.ResourcesMoved)
	}
	if len(result.OutputsCreated) != 2 {
		t.Errorf("want 2 outputs created, got %+v", result.OutputsCreated)
	}

	// Module directory structure
	mod := filepath.Join(dir, "modules", "networking")
	for _, name := range []string{"main.tf", "variables.tf", "outputs.tf"} {
		if _, err := os.Stat(filepath.Join(mod, name)); err != nil {
			t.Errorf("expected module file %s: %v", name, err)
		}
	}

	// Module's main.tf contains the moved resources
	modMain, _ := os.ReadFile(filepath.Join(mod, "main.tf"))
	for _, needle := range []string{`resource "aws_vpc" "main"`, `resource "aws_subnet" "public_1"`} {
		if !strings.Contains(string(modMain), needle) {
			t.Errorf("module main.tf missing %q\n---\n%s", needle, modMain)
		}
	}

	// Module's outputs.tf exposes both resources
	outs, _ := os.ReadFile(filepath.Join(mod, "outputs.tf"))
	for _, needle := range []string{`output "main"`, `output "public_1"`, `aws_vpc.main`, `aws_subnet.public_1`} {
		if !strings.Contains(string(outs), needle) {
			t.Errorf("outputs.tf missing %q\n---\n%s", needle, outs)
		}
	}

	// Root main.tf no longer contains the moved resources but keeps the S3 bucket,
	// and carries a module call.
	rootMain, _ := os.ReadFile(filepath.Join(dir, "main.tf"))
	if strings.Contains(string(rootMain), `resource "aws_vpc" "main"`) {
		t.Error("root should no longer contain aws_vpc.main")
	}
	if strings.Contains(string(rootMain), `resource "aws_subnet" "public_1"`) {
		t.Error("root should no longer contain aws_subnet.public_1")
	}
	if !strings.Contains(string(rootMain), `resource "aws_s3_bucket" "logs"`) {
		t.Error("root should still contain unrelated aws_s3_bucket.logs")
	}
	if !strings.Contains(string(rootMain), `module "networking"`) {
		t.Errorf("root should gain a module call, got:\n%s", rootMain)
	}
	if !strings.Contains(string(rootMain), `source = "./modules/networking"`) {
		t.Errorf("module call should reference local path:\n%s", rootMain)
	}
}

// TestPromoteToModuleRejectsBadName — module name must be a lowercase
// identifier. Hyphens, uppercase, and digits-first are all refused.
func TestPromoteToModuleRejectsBadName(t *testing.T) {
	dir := scaffoldProject(t, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`)
	cases := []string{"Networking", "net-working", "9networking", "net/working", ""}
	for _, name := range cases {
		_, err := PromoteToModule(PromoteRequest{
			ProjectDir:  dir,
			ModuleName:  name,
			ResourceIDs: []string{"aws_vpc.main"},
		})
		if err == nil {
			t.Errorf("name %q should be refused", name)
		}
	}
}

// TestPromoteToModuleRejectsEmptySelection — no resource IDs is a clear
// user error, not a silent no-op.
func TestPromoteToModuleRejectsEmptySelection(t *testing.T) {
	dir := scaffoldProject(t, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`)
	_, err := PromoteToModule(PromoteRequest{
		ProjectDir:  dir,
		ModuleName:  "networking",
		ResourceIDs: nil,
	})
	if err == nil {
		t.Error("empty selection should be refused")
	}
}

// TestPromoteToModuleRejectsMissingResource — a resource ID that doesn't
// exist in the project is surfaced clearly so the UI can display it.
func TestPromoteToModuleRejectsMissingResource(t *testing.T) {
	dir := scaffoldProject(t, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`)
	_, err := PromoteToModule(PromoteRequest{
		ProjectDir:  dir,
		ModuleName:  "networking",
		ResourceIDs: []string{"aws_vpc.main", "aws_subnet.ghost"},
	})
	if err == nil || !strings.Contains(err.Error(), "aws_subnet.ghost") {
		t.Errorf("missing resource should be named in the error, got: %v", err)
	}
}

// TestPromoteToModuleRefusesExistingTarget — don't overwrite an existing
// modules/<name>/ directory. Users resolve the collision manually.
func TestPromoteToModuleRefusesExistingTarget(t *testing.T) {
	dir := scaffoldProject(t, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`)
	// Pre-create the target so the refactor must refuse.
	if err := os.MkdirAll(filepath.Join(dir, "modules", "networking"), 0o755); err != nil {
		t.Fatalf("pre-create: %v", err)
	}
	_, err := PromoteToModule(PromoteRequest{
		ProjectDir:  dir,
		ModuleName:  "networking",
		ResourceIDs: []string{"aws_vpc.main"},
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("should refuse existing target dir, got: %v", err)
	}
	// The original main.tf must still contain the resource — the refactor
	// must be atomic in the "all or nothing" sense that a failed promotion
	// doesn't mutate source state.
	body, _ := os.ReadFile(filepath.Join(dir, "main.tf"))
	if !strings.Contains(string(body), `resource "aws_vpc" "main"`) {
		t.Errorf("failed refactor must not mutate sources: %s", body)
	}
}
