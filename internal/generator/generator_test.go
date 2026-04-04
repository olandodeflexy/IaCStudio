package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/parser"
)

func TestHCLGenerator_Generate(t *testing.T) {
	gen := &HCLGenerator{tool: "terraform"}

	resources := []parser.Resource{
		{
			ID:   "1",
			Type: "aws_vpc",
			Name: "main",
			Properties: map[string]interface{}{
				"cidr_block":         "10.0.0.0/16",
				"enable_dns_support": true,
			},
		},
		{
			ID:   "2",
			Type: "aws_instance",
			Name: "web",
			Properties: map[string]interface{}{
				"ami":           "ami-0c55b159cbfafe1f0",
				"instance_type": "t3.micro",
			},
		},
	}

	code, err := gen.Generate(resources)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify provider block
	if !strings.Contains(code, `provider "aws"`) {
		t.Error("Missing provider block")
	}

	// Verify resources
	if !strings.Contains(code, `resource "aws_vpc" "main"`) {
		t.Error("Missing VPC resource")
	}
	if !strings.Contains(code, `resource "aws_instance" "web"`) {
		t.Error("Missing instance resource")
	}

	// Verify properties
	if !strings.Contains(code, `cidr_block = "10.0.0.0/16"`) {
		t.Error("Missing cidr_block property")
	}
	if !strings.Contains(code, `enable_dns_support = true`) {
		t.Error("Missing boolean property")
	}
}

func TestHCLGenerator_GenerateEmpty(t *testing.T) {
	gen := &HCLGenerator{tool: "terraform"}
	code, err := gen.Generate([]parser.Resource{})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if !strings.Contains(code, "provider") {
		t.Error("Empty generation should still include provider")
	}
}

func TestYAMLGenerator_Generate(t *testing.T) {
	gen := &YAMLGenerator{}

	resources := []parser.Resource{
		{
			ID:   "1",
			Type: "apt",
			Name: "Install nginx",
			Properties: map[string]interface{}{
				"name":         "nginx",
				"state":        "present",
				"update_cache": true,
			},
		},
	}

	code, err := gen.Generate(resources)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if !strings.Contains(code, "- name: Install nginx") {
		t.Error("Missing task name")
	}
	if !strings.Contains(code, "apt:") {
		t.Error("Missing module name")
	}
	if !strings.Contains(code, "update_cache: yes") {
		t.Error("Missing boolean property")
	}
}

func TestHCLGenerator_WriteScaffold(t *testing.T) {
	dir := t.TempDir()
	gen := &HCLGenerator{tool: "terraform"}

	if err := gen.WriteScaffold(dir); err != nil {
		t.Fatalf("WriteScaffold failed: %v", err)
	}

	expectedFiles := []string{"main.tf", "variables.tf", "outputs.tf", ".gitignore"}
	for _, f := range expectedFiles {
		path := filepath.Join(dir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Missing scaffold file: %s", f)
		}
	}
}

func TestYAMLGenerator_WriteScaffold(t *testing.T) {
	dir := t.TempDir()
	gen := &YAMLGenerator{}

	if err := gen.WriteScaffold(dir); err != nil {
		t.Fatalf("WriteScaffold failed: %v", err)
	}

	expectedFiles := []string{"site.yml", "inventory.ini", "ansible.cfg", ".gitignore"}
	for _, f := range expectedFiles {
		path := filepath.Join(dir, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Missing scaffold file: %s", f)
		}
	}

	// Check roles directory
	rolesDir := filepath.Join(dir, "roles")
	if info, err := os.Stat(rolesDir); err != nil || !info.IsDir() {
		t.Error("Missing roles/ directory")
	}
}

func TestForTool(t *testing.T) {
	tests := []struct {
		tool     string
		wantExt  string
	}{
		{"terraform", ".tf"},
		{"opentofu", ".tf"},
		{"ansible", ".yml"},
		{"unknown", ".tf"}, // defaults to HCL
	}

	for _, tt := range tests {
		gen := ForTool(tt.tool)
		if gen.FileExtension() != tt.wantExt {
			t.Errorf("ForTool(%q) extension = %q, want %q", tt.tool, gen.FileExtension(), tt.wantExt)
		}
	}
}
