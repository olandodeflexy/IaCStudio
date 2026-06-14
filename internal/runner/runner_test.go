package runner

import (
	"strings"
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
		{"init", "npm", false},    // npm install, not pulumi stack init
		{"plan", "pulumi", false}, // preview
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

func TestMergeEnvOverridesBase(t *testing.T) {
	got := mergeEnv(
		[]string{"PATH=/usr/bin", "AWS_PROFILE=default", "BROKEN", "EMPTYKEY"},
		map[string]string{
			"AWS_PROFILE":   "prod-admin",
			"AWS_REGION":    "us-east-1",
			"GOOGLE_REGION": "",
		},
	)

	values := map[string]string{}
	for _, entry := range got {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("env entry missing separator: %q", entry)
		}
		values[key] = value
	}
	if values["PATH"] != "/usr/bin" {
		t.Fatalf("PATH should be preserved: %#v", values)
	}
	if values["AWS_PROFILE"] != "prod-admin" {
		t.Fatalf("AWS_PROFILE should be overridden: %#v", values)
	}
	if values["AWS_REGION"] != "us-east-1" {
		t.Fatalf("AWS_REGION should be added: %#v", values)
	}
	if values["GOOGLE_REGION"] != "" {
		t.Fatalf("empty override value should be preserved: %#v", values)
	}
}

func TestScopedCommandEnvDropsAmbientCloudCredentials(t *testing.T) {
	got := envValues(scopedCommandEnv(
		[]string{
			"PATH=/usr/bin",
			"HOME=/home/alice",
			"AWS_PROFILE=ambient-admin",
			"AWS_ACCESS_KEY_ID=ambient-key",
			"AWS_SECRET_ACCESS_KEY=ambient-secret",
			"AWS_SESSION_TOKEN=ambient-session",
			"AWS_EC2_METADATA_DISABLED=false",
			"ARM_CLIENT_SECRET=ambient-azure-secret",
			"GOOGLE_APPLICATION_CREDENTIALS=/tmp/ambient-gcp.json",
			"TFE_TOKEN=ambient-tfe-token",
			"TF_TOKEN_app_terraform_io=ambient-tf-token",
			"UNRELATED_SECRET=ambient",
		},
		map[string]string{
			"AWS_ACCESS_KEY_ID":     "selected-key",
			"AWS_SECRET_ACCESS_KEY": "selected-secret",
			"AWS_REGION":            "us-east-1",
		},
	))

	if got["PATH"] != "/usr/bin" || got["HOME"] != "/home/alice" {
		t.Fatalf("minimal OS env should be preserved: %#v", got)
	}
	if got["AWS_ACCESS_KEY_ID"] != "selected-key" || got["AWS_SECRET_ACCESS_KEY"] != "selected-secret" {
		t.Fatalf("selected connection credentials should be present: %#v", got)
	}
	if got["AWS_REGION"] != "us-east-1" {
		t.Fatalf("selected connection region should be present: %#v", got)
	}
	if got["AWS_EC2_METADATA_DISABLED"] != "true" {
		t.Fatalf("scoped env should disable AWS metadata fallback: %#v", got)
	}
	for _, key := range []string{
		"AWS_PROFILE",
		"AWS_SESSION_TOKEN",
		"ARM_CLIENT_SECRET",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"TFE_TOKEN",
		"TF_TOKEN_app_terraform_io",
		"UNRELATED_SECRET",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("scoped env leaked ambient %s: %#v", key, got)
		}
	}
}

func TestScopedCommandEnvAllowsSelectedProfileWithoutAmbientStaticKeys(t *testing.T) {
	got := envValues(scopedCommandEnv(
		[]string{
			"PATH=/usr/bin",
			"HOME=/home/alice",
			"AWS_ACCESS_KEY_ID=ambient-key",
			"AWS_SECRET_ACCESS_KEY=ambient-secret",
			"AWS_SESSION_TOKEN=ambient-session",
			"AWS_SHARED_CREDENTIALS_FILE=/tmp/wrong-credentials",
			"AWS_CONFIG_FILE=/tmp/wrong-config",
		},
		map[string]string{
			"AWS_PROFILE":         "prod-sso",
			"AWS_SDK_LOAD_CONFIG": "1",
			"AWS_DEFAULT_REGION":  "eu-west-1",
		},
	))

	if got["AWS_PROFILE"] != "prod-sso" || got["AWS_SDK_LOAD_CONFIG"] != "1" {
		t.Fatalf("selected profile connection should be present: %#v", got)
	}
	if got["AWS_DEFAULT_REGION"] != "eu-west-1" {
		t.Fatalf("selected profile region should be present: %#v", got)
	}
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_SHARED_CREDENTIALS_FILE",
		"AWS_CONFIG_FILE",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("selected profile run leaked ambient %s: %#v", key, got)
		}
	}
}

func TestScopedCommandEnvKeepsSelectedProviderOnly(t *testing.T) {
	got := envValues(scopedCommandEnv(
		[]string{
			"PATH=/usr/bin",
			"AWS_ACCESS_KEY_ID=ambient-key",
			"ARM_CLIENT_SECRET=ambient-azure-secret",
			"GOOGLE_CREDENTIALS=ambient-gcp-json",
			"CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=/tmp/ambient-gcp.json",
		},
		map[string]string{
			"ARM_CLIENT_ID":       "selected-client",
			"ARM_CLIENT_SECRET":   "selected-secret",
			"ARM_TENANT_ID":       "selected-tenant",
			"ARM_SUBSCRIPTION_ID": "selected-subscription",
		},
	))

	if got["ARM_CLIENT_SECRET"] != "selected-secret" || got["ARM_CLIENT_ID"] != "selected-client" {
		t.Fatalf("selected Azure credentials should be present: %#v", got)
	}
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID",
		"GOOGLE_CREDENTIALS",
		"CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("selected Azure run leaked ambient %s: %#v", key, got)
		}
	}

	got = envValues(scopedCommandEnv(
		[]string{
			"PATH=/usr/bin",
			"AWS_ACCESS_KEY_ID=ambient-key",
			"ARM_CLIENT_SECRET=ambient-azure-secret",
			"GOOGLE_APPLICATION_CREDENTIALS=/tmp/ambient-gcp.json",
		},
		map[string]string{
			"GOOGLE_CREDENTIALS":    `{"type":"service_account"}`,
			"GOOGLE_CLOUD_PROJECT":  "selected-project",
			"CLOUDSDK_CORE_PROJECT": "selected-project",
		},
	))

	if got["GOOGLE_CREDENTIALS"] != `{"type":"service_account"}` || got["GOOGLE_CLOUD_PROJECT"] != "selected-project" {
		t.Fatalf("selected GCP credentials should be present: %#v", got)
	}
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID",
		"ARM_CLIENT_SECRET",
		"GOOGLE_APPLICATION_CREDENTIALS",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("selected GCP run leaked ambient %s: %#v", key, got)
		}
	}
}

func envValues(entries []string) map[string]string {
	values := map[string]string{}
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
