package runner

import (
	"testing"
)

func TestDetectTools(t *testing.T) {
	r := New()
	tools := r.DetectTools()

	if len(tools) != 3 {
		t.Errorf("DetectTools returned %d tools, want 3", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Name == "" {
			t.Error("Tool has empty name")
		}
		if tool.Binary == "" {
			t.Error("Tool has empty binary path")
		}
	}

	for _, expected := range []string{"Terraform", "OpenTofu", "Ansible"} {
		if !names[expected] {
			t.Errorf("Missing tool: %s", expected)
		}
	}
}

func TestBuildArgs_Terraform(t *testing.T) {
	r := New()

	tests := []struct {
		command string
		want    string // first arg
	}{
		{"init", "terraform"},
		{"plan", "terraform"},
		{"apply", "terraform"},
		{"validate", "terraform"},
	}

	for _, tt := range tests {
		args := r.buildArgs("terraform", tt.command)
		if len(args) == 0 {
			t.Errorf("buildArgs(terraform, %s) returned empty", tt.command)
			continue
		}
		if args[0] != tt.want {
			t.Errorf("buildArgs(terraform, %s)[0] = %s, want %s", tt.command, args[0], tt.want)
		}
	}
}

func TestBuildArgs_OpenTofu(t *testing.T) {
	r := New()
	args := r.buildArgs("opentofu", "plan")
	if len(args) == 0 || args[0] != "tofu" {
		t.Errorf("OpenTofu should use 'tofu' binary, got %v", args)
	}
}

func TestBuildArgs_Ansible(t *testing.T) {
	r := New()

	tests := []struct {
		command string
		want    string
	}{
		{"check", "ansible-playbook"},
		{"playbook", "ansible-playbook"},
		{"syntax", "ansible-playbook"},
	}

	for _, tt := range tests {
		args := r.buildArgs("ansible", tt.command)
		if len(args) == 0 {
			t.Errorf("buildArgs(ansible, %s) returned empty", tt.command)
			continue
		}
		if args[0] != tt.want {
			t.Errorf("buildArgs(ansible, %s)[0] = %s, want %s", tt.command, args[0], tt.want)
		}
	}
}

func TestBuildArgs_Unknown(t *testing.T) {
	r := New()
	args := r.buildArgs("unknown_tool", "init")
	if args != nil {
		t.Errorf("Unknown tool should return nil, got %v", args)
	}
}

func TestBuildArgs_UnknownCommand(t *testing.T) {
	r := New()
	args := r.buildArgs("terraform", "unknown_command")
	if args != nil {
		t.Errorf("Unknown command should return nil, got %v", args)
	}
}
