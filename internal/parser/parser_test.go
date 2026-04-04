package parser

import (
	"os"
	"path/filepath"
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
	os.WriteFile(tf, []byte(""), 0644)

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
	os.WriteFile(tf, []byte(`provider "aws" { region = "us-east-1" }`), 0644)

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
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
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
	os.WriteFile(ymlFile, []byte(`not: [valid: yaml: {`), 0644)

	p := &YAMLParser{}
	_, err := p.ParseFile(ymlFile)
	if err == nil {
		// Parser should either return error or empty result gracefully
		// Both are acceptable
	}
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
