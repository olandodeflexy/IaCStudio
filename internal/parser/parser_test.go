package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHCLParser_ParseFile(t *testing.T) {
	dir := t.TempDir()
	tfFile := filepath.Join(dir, "main.tf")

	content := `
provider "aws" {
  region = "us-east-1"
}

resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
}

resource "aws_subnet" "public" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.1.0/24"
}
`
	if err := os.WriteFile(tfFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	p := &HCLParser{}
	resources, err := p.ParseFile(tfFile)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("Expected 2 resources, got %d", len(resources))
	}

	// Verify VPC
	vpc := resources[0]
	if vpc.Type != "aws_vpc" {
		t.Errorf("First resource type = %s, want aws_vpc", vpc.Type)
	}
	if vpc.Name != "main" {
		t.Errorf("First resource name = %s, want main", vpc.Name)
	}
	if vpc.ID != "aws_vpc.main" {
		t.Errorf("First resource ID = %s, want aws_vpc.main", vpc.ID)
	}
	if vpc.File != tfFile {
		t.Errorf("File = %s, want %s", vpc.File, tfFile)
	}

	// Verify subnet
	subnet := resources[1]
	if subnet.Type != "aws_subnet" {
		t.Errorf("Second resource type = %s, want aws_subnet", subnet.Type)
	}
	if subnet.Name != "public" {
		t.Errorf("Second resource name = %s, want public", subnet.Name)
	}
}

func TestHCLParser_PreservesMultilineRawExpressions(t *testing.T) {
	dir := t.TempDir()
	tfFile := filepath.Join(dir, "main.tf")

	content := `resource "aws_instance" "web" {
  ami           = data.aws_ami.ubuntu.id
  instance_type = "t3.micro"
  tags = {
    Environment = var.environment
    Name        = "web"
  }
}
`
	if err := os.WriteFile(tfFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	p := &HCLParser{}
	resources, err := p.ParseFile(tfFile)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("Expected 1 resource, got %d", len(resources))
	}

	tags, ok := resources[0].Properties["tags"].(string)
	if !ok {
		t.Fatalf("tags should be preserved as raw HCL, got %#v", resources[0].Properties["tags"])
	}
	for _, want := range []string{"{", "Environment = var.environment", `Name        = "web"`, "}"} {
		if !strings.Contains(tags, want) {
			t.Errorf("raw tags expression missing %q:\n%s", want, tags)
		}
	}
}

func TestHCLParser_ParseDir(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`,
		"compute.tf": `
resource "aws_instance" "web" {
  ami           = "ami-12345"
  instance_type = "t3.micro"
}

resource "aws_instance" "api" {
  ami           = "ami-12345"
  instance_type = "t3.small"
}
`,
		"not_terraform.txt": `This should be ignored`,
	}

	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	p := &HCLParser{}
	resources, err := p.ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir failed: %v", err)
	}

	if len(resources) != 3 {
		t.Fatalf("Expected 3 resources, got %d", len(resources))
	}
}

func TestHCLParser_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "empty.tf")
	if err := os.WriteFile(tf, []byte(""), 0644); err != nil {
		t.Fatalf("seed empty.tf: %v", err)
	}

	p := &HCLParser{}
	resources, err := p.ParseFile(tf)
	if err != nil {
		t.Fatalf("ParseFile failed on empty file: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("Expected 0 resources from empty file, got %d", len(resources))
	}
}

func TestHCLParser_ProviderOnly(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "providers.tf")
	if err := os.WriteFile(tf, []byte(`provider "aws" { region = "us-east-1" }`), 0644); err != nil {
		t.Fatalf("seed providers.tf: %v", err)
	}

	p := &HCLParser{}
	resources, err := p.ParseFile(tf)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("Provider blocks should not be parsed as resources, got %d", len(resources))
	}
}

func TestYAMLParser_ParseFile(t *testing.T) {
	dir := t.TempDir()
	ymlFile := filepath.Join(dir, "site.yml")

	content := `---
- name: Web Server Setup
  hosts: all
  become: true
  tasks:
    - name: Install nginx
      apt:
        name: nginx
        state: present
    - name: Start nginx
      service:
        name: nginx
        state: started
        enabled: true
`
	if err := os.WriteFile(ymlFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	p := &YAMLParser{}
	resources, err := p.ParseFile(ymlFile)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("Expected 2 resources, got %d", len(resources))
	}

	// Verify first task
	if resources[0].Type != "apt" {
		t.Errorf("First task type = %s, want apt", resources[0].Type)
	}
	if resources[0].Name != "Install nginx" {
		t.Errorf("First task name = %s, want 'Install nginx'", resources[0].Name)
	}

	// Verify second task
	if resources[1].Type != "service" {
		t.Errorf("Second task type = %s, want service", resources[1].Type)
	}
}

func TestYAMLParser_ParseDir(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"site.yml": `---
- name: Main
  hosts: all
  tasks:
    - name: Install packages
      apt:
        name: curl
        state: present
`,
		"deploy.yaml": `---
- name: Deploy
  hosts: web
  tasks:
    - name: Copy app
      copy:
        src: /local/app
        dest: /remote/app
`,
		"readme.txt": `Not YAML`,
	}

	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	p := &YAMLParser{}
	resources, err := p.ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir failed: %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("Expected 2 resources, got %d", len(resources))
	}
}

func TestYAMLParser_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	ymlFile := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(ymlFile, []byte(`not: [valid: yaml: {`), 0644); err != nil {
		t.Fatalf("seed bad.yml: %v", err)
	}

	p := &YAMLParser{}
	// Parser is allowed to either return an error or degrade to an empty
	// result — we only check it does not panic. No assertion on err.
	_, _ = p.ParseFile(ymlFile)
}

func TestForTool_Parser(t *testing.T) {
	tests := []struct {
		tool string
	}{
		{"terraform"},
		{"opentofu"},
		{"ansible"},
		{"unknown"},
	}

	for _, tt := range tests {
		p := ForTool(tt.tool)
		if p == nil {
			t.Errorf("ForTool(%q) returned nil", tt.tool)
		}
	}
}

// TestHCLParser_Module verifies module blocks parse into structured Module
// entries: source + version pulled out explicitly, every other attribute
// landing in Inputs, and the raw block NOT also appearing in PreservedBlocks
// (modules should be structured-only when they parse cleanly).
func TestHCLParser_ModuleStructured(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "main.tf")
	body := `module "networking" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  cidr = "10.0.0.0/16"
  tags = {
    Environment = "prod"
  }
}

module "local_compute" {
  source = "./modules/compute"

  subnet_ids = module.networking.private_subnets
  instance_count = 3
}
`
	if err := os.WriteFile(tf, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	p := &HCLParser{}
	result, err := p.ParseFileFull(tf)
	if err != nil {
		t.Fatalf("ParseFileFull: %v", err)
	}

	if len(result.Modules) != 2 {
		t.Fatalf("want 2 modules, got %d: %+v", len(result.Modules), result.Modules)
	}

	// Find each by name so the test isn't order-sensitive.
	byName := map[string]Module{}
	for _, m := range result.Modules {
		byName[m.Name] = m
	}

	vpc := byName["networking"]
	if vpc.Source != "terraform-aws-modules/vpc/aws" {
		t.Errorf("source wrong: %q", vpc.Source)
	}
	if vpc.Version != "~> 5.0" {
		t.Errorf("version wrong: %q", vpc.Version)
	}
	if _, ok := vpc.Inputs["cidr"]; !ok {
		t.Errorf("static input 'cidr' missing: %+v", vpc.Inputs)
	}
	if _, ok := vpc.Inputs["source"]; ok {
		t.Errorf("source/version must not leak into Inputs: %+v", vpc.Inputs)
	}
	if vpc.ID != "module.networking" {
		t.Errorf("ID = %q, want module.networking", vpc.ID)
	}

	local := byName["local_compute"]
	if local.Source != "./modules/compute" {
		t.Errorf("local source wrong: %q", local.Source)
	}
	if local.Version != "" {
		t.Errorf("local modules have no version constraint, got %q", local.Version)
	}
	// subnet_ids is a reference — should round-trip as raw HCL text.
	raw, ok := local.Inputs["subnet_ids"].(string)
	if !ok || raw != "module.networking.private_subnets" {
		t.Errorf("reference input should be raw HCL, got %#v", local.Inputs["subnet_ids"])
	}

	// Structured modules should NOT also appear as PreservedBlocks — that
	// would cause the generator to emit each module twice on sync.
	for _, pb := range result.PreservedBlocks {
		if pb.Type == "module" {
			t.Errorf("module block still in PreservedBlocks — structured parse should replace preservation: %+v", pb)
		}
	}
}

// TestHCLParser_ModuleMalformedFallsBack confirms that a malformed module
// block (wrong number of labels) falls back to PreservedBlock so the
// generator can round-trip even pathological input.
func TestHCLParser_ModuleMalformedFallsBack(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "bad.tf")
	body := `module "extra" "labels" {
  source = "./nope"
}
`
	if err := os.WriteFile(tf, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p := &HCLParser{}
	result, err := p.ParseFileFull(tf)
	if err != nil {
		t.Fatalf("ParseFileFull: %v", err)
	}
	if len(result.Modules) != 0 {
		t.Errorf("malformed module should not produce structured entry: %+v", result.Modules)
	}
	foundPreserved := false
	for _, pb := range result.PreservedBlocks {
		if pb.Type == "module" {
			foundPreserved = true
		}
	}
	if !foundPreserved {
		t.Error("malformed module should fall back to PreservedBlock")
	}
}

// TestInspectLocalModuleBasic covers the golden path: a module with a mix
// of typed-default, reference-default, and sensitive variables, plus
// outputs that reference resources by expression.
func TestInspectLocalModuleBasic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(`
variable "cidr_block" {
  description = "Primary VPC CIDR"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  type    = list(string)
  default = ["us-east-1a", "us-east-1b"]
}

variable "env" {
  description = "Environment name (dev | staging | prod)"
  type        = string
  # no default → required input
}

variable "secret_key" {
  type      = string
  sensitive = true
}
`), 0o644); err != nil {
		t.Fatalf("seed variables.tf: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "outputs.tf"), []byte(`
output "vpc_id" {
  description = "The VPC's identifier"
  value       = aws_vpc.this.id
}

output "connection_string" {
  value     = "postgres://${aws_db_instance.primary.endpoint}/prod"
  sensitive = true
}
`), 0o644); err != nil {
		t.Fatalf("seed outputs.tf: %v", err)
	}

	iface, err := InspectLocalModule(dir)
	if err != nil {
		t.Fatalf("InspectLocalModule: %v", err)
	}
	if iface.SourceDir != dir {
		t.Errorf("SourceDir = %q, want %q", iface.SourceDir, dir)
	}
	if len(iface.Variables) != 4 {
		t.Fatalf("want 4 variables, got %d: %+v", len(iface.Variables), iface.Variables)
	}
	if len(iface.Outputs) != 2 {
		t.Fatalf("want 2 outputs, got %d: %+v", len(iface.Outputs), iface.Outputs)
	}

	// Index by name for assertions independent of file walk order.
	byVar := map[string]ModuleVariable{}
	for _, v := range iface.Variables {
		byVar[v.Name] = v
	}
	if byVar["cidr_block"].Default != "10.0.0.0/16" {
		t.Errorf("cidr_block default wrong: %#v", byVar["cidr_block"].Default)
	}
	if !byVar["cidr_block"].HasDefault {
		t.Errorf("cidr_block HasDefault should be true")
	}
	if byVar["env"].HasDefault {
		t.Errorf("env has no default; HasDefault should be false: %+v", byVar["env"])
	}
	if !byVar["secret_key"].Sensitive {
		t.Errorf("secret_key should be marked sensitive")
	}
	if byVar["availability_zones"].Type != "list(string)" {
		t.Errorf("type expression should survive as raw HCL, got %q", byVar["availability_zones"].Type)
	}

	byOut := map[string]ModuleOutput{}
	for _, o := range iface.Outputs {
		byOut[o.Name] = o
	}
	if byOut["vpc_id"].Value != "aws_vpc.this.id" {
		t.Errorf("vpc_id value should be raw HCL, got %q", byOut["vpc_id"].Value)
	}
	if !byOut["connection_string"].Sensitive {
		t.Errorf("connection_string should be marked sensitive")
	}
}

// TestInspectLocalModuleIgnoresNoise — non-.tf files, override files, and
// subdirectories are all skipped.
func TestInspectLocalModuleIgnoresNoise(t *testing.T) {
	dir := t.TempDir()
	// Valid declaration.
	_ = os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(`variable "x" {}`), 0o644)
	// Override file — skipped.
	_ = os.WriteFile(filepath.Join(dir, "variables_override.tf"), []byte(`variable "x" { default = "overridden" }`), 0o644)
	// Non-HCL noise.
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# docs"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte("{}"), 0o644)
	// Subdirectory with its own variables.tf — should NOT be recursed into.
	sub := filepath.Join(dir, "nested")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "variables.tf"), []byte(`variable "nested" {}`), 0o644)

	iface, err := InspectLocalModule(dir)
	if err != nil {
		t.Fatalf("InspectLocalModule: %v", err)
	}
	if len(iface.Variables) != 1 || iface.Variables[0].Name != "x" {
		t.Errorf("only the top-level non-override variable 'x' should parse, got %+v", iface.Variables)
	}
}

// TestInspectLocalModuleEmpty — a directory with no .tf files is a valid
// minimal module (zero-interface). Returns an empty interface, no error.
func TestInspectLocalModuleEmpty(t *testing.T) {
	iface, err := InspectLocalModule(t.TempDir())
	if err != nil {
		t.Fatalf("empty module dir should not error: %v", err)
	}
	if len(iface.Variables) != 0 || len(iface.Outputs) != 0 {
		t.Errorf("empty module should yield empty interface, got %+v", iface)
	}
}

// TestInspectLocalModuleBadHCL surfaces parse errors instead of returning
// partial data.
func TestInspectLocalModuleBadHCL(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(`variable { unbalanced = `), 0o644)
	_, err := InspectLocalModule(dir)
	if err == nil {
		t.Error("malformed HCL should surface as an error")
	}
}
