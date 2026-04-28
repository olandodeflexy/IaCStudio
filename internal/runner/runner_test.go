package runner

import (
	"testing"
)

func TestDetectTools(t *testing.T) {
	r := New()
	tools := r.DetectTools()

	if len(tools) != 4 {
		t.Errorf("DetectTools returned %d tools, want 4", len(tools))
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

	for _, expected := range []string{"Terraform", "OpenTofu", "Ansible", "Pulumi"} {
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
		args := r.buildArgs("terraform", tt.command, "")
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
	args := r.buildArgs("opentofu", "plan", "")
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
		args := r.buildArgs("ansible", tt.command, "")
		if len(args) == 0 {
			t.Errorf("buildArgs(ansible, %s) returned empty", tt.command)
			continue
		}
		if args[0] != tt.want {
			t.Errorf("buildArgs(ansible, %s)[0] = %s, want %s", tt.command, args[0], tt.want)
		}
	}
}

func TestBuildArgs_Pulumi(t *testing.T) {
	r := New()

	cases := []struct {
		command    string
		wantBinary string
		// wantHasYes asserts destructive commands carry --yes so the
		// CLI doesn't hang on its own interactive confirmation (the
		// server-side approval gate handles that instead).
		wantHasYes bool
	}{
		{"init", "npm", false},     // npm install, not pulumi stack init
		{"plan", "pulumi", false},  // preview
		{"preview", "pulumi", false},
		{"apply", "pulumi", true},
		{"up", "pulumi", true},
		{"destroy", "pulumi", true},
		{"refresh", "pulumi", true},
		{"validate", "npx", false}, // tsc --noEmit
	}
	for _, tc := range cases {
		args := r.buildArgs("pulumi", tc.command, "")
		if len(args) == 0 {
			t.Errorf("buildArgs(pulumi, %s) returned empty", tc.command)
			continue
		}
		if args[0] != tc.wantBinary {
			t.Errorf("buildArgs(pulumi, %s)[0] = %s, want %s", tc.command, args[0], tc.wantBinary)
		}
		if tc.wantHasYes {
			found := false
			for _, a := range args {
				if a == "--yes" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("buildArgs(pulumi, %s) missing --yes flag: %v", tc.command, args)
			}
		}
	}
}

func TestBuildArgs_Unknown(t *testing.T) {
	r := New()
	args := r.buildArgs("unknown_tool", "init", "")
	if args != nil {
		t.Errorf("Unknown tool should return nil, got %v", args)
	}
}

func TestBuildArgs_UnknownCommand(t *testing.T) {
	r := New()
	args := r.buildArgs("terraform", "unknown_command", "")
	if args != nil {
		t.Errorf("Unknown command should return nil, got %v", args)
	}
}

// TestBuildArgs_PulumiPassesStack pins the env→--stack plumbing.
// Without this, an env-rebased Pulumi run would inherit whichever
// stack is workspace-selected in environments/<env>/, which lets a
// preview against dev hand-off to an apply against whatever was last
// `pulumi stack select`'d — wrong-environment mutation risk.
func TestBuildArgs_PulumiPassesStack(t *testing.T) {
	r := New()
	cases := []string{"plan", "preview", "apply", "up", "destroy", "refresh"}
	for _, cmd := range cases {
		args := r.buildArgs("pulumi", cmd, "dev")
		hasStack := false
		for i, a := range args {
			if a == "--stack" && i+1 < len(args) && args[i+1] == "dev" {
				hasStack = true
				break
			}
		}
		if !hasStack {
			t.Errorf("buildArgs(pulumi, %s, env=dev) missing --stack dev: %v", cmd, args)
		}
	}
	// Empty env → no --stack flag (flat layout).
	args := r.buildArgs("pulumi", "preview", "")
	for _, a := range args {
		if a == "--stack" {
			t.Errorf("empty env should not emit --stack: %v", args)
		}
	}
	// init uses npm, no --stack regardless of env.
	args = r.buildArgs("pulumi", "init", "dev")
	for _, a := range args {
		if a == "--stack" {
			t.Errorf("init should not carry --stack: %v", args)
		}
	}
}

// TestRequiresApproval_CoversPulumiUp ensures Pulumi's native 'up'
// verb lands on the approval gate just like terraform/ansible's
// 'apply'. A regression here would let pulumi up skip plan review.
func TestRequiresApproval_CoversPulumiUp(t *testing.T) {
	sr := NewSafeRunner(DefaultSafetyConfig())
	cases := map[string]bool{
		"plan":    false,
		"preview": false,
		"apply":   true,
		"up":      true,
		"destroy": true,
		"refresh": true, // state-mutating — gated alongside apply/destroy
	}
	for cmd, want := range cases {
		if got := sr.RequiresApproval(cmd); got != want {
			t.Errorf("RequiresApproval(%q) = %v, want %v", cmd, got, want)
		}
	}
}
