package crossguard

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
)

func fakePulumiBinary(t *testing.T, stdout string, exitCode int, logPath string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-scripted fake binary not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pulumi")
	logLine := ""
	if logPath != "" {
		logLine = "printf '%s\\n' \"$PWD $*\" > " + shellQuote(logPath) + "\n"
	}
	script := "#!/usr/bin/env bash\n" +
		logLine +
		"cat <<'IAC_EOF'\n" + stdout + "\nIAC_EOF\n" +
		"exit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pulumi: %v", err)
	}
	return path
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func scaffoldPulumiProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Pulumi.yaml"), []byte("name: demo\nruntime: nodejs\n"), 0o644); err != nil {
		t.Fatalf("write Pulumi.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Pulumi.dev.yaml"), []byte("config: {}\n"), 0o644); err != nil {
		t.Fatalf("write stack yaml: %v", err)
	}
	packDir := filepath.Join(root, "policies", "crossguard")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir policy pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "PulumiPolicy.yaml"), []byte("name: demo-policy\nruntime: nodejs\n"), 0o644); err != nil {
		t.Fatalf("write policy pack: %v", err)
	}
	return root
}

func TestCrossGuardEngineNotAvailable(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = "/nonexistent/pulumi-please-dont-exist"

	res, err := New().Evaluate(context.Background(), engines.EvalInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Available {
		t.Error("Available should be false when pulumi is missing")
	}
	if res.Error == "" {
		t.Error("expected missing-binary error")
	}
}

func TestCrossGuardParsesPulumiPolicyViolations(t *testing.T) {
	output := `Previewing update (dev):

Policy Violations:
    [mandatory]  aws-typescript v0.0.1  s3-bucket-prefix (my-bucket: aws:s3/bucket:Bucket)
    Ensures S3 buckets use the required naming prefix.
    S3 bucket must use 'mycompany-' prefix. Current prefix: 'wrongprefix-'
    [advisory]  aws-typescript v0.0.1  required-tags (my-bucket: aws:s3/bucket:Bucket)
    Resource should define standard tags.
`
	logPath := filepath.Join(t.TempDir(), "pulumi.log")
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakePulumiBinary(t, output, 1, logPath)

	root := scaffoldPulumiProject(t)
	res, err := New().Evaluate(context.Background(), engines.EvalInput{ProjectDir: root})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.Available {
		t.Fatalf("expected available result: %+v", res)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
	if res.Findings[0].PolicyID != "s3-bucket-prefix" || res.Findings[0].Severity != engines.SeverityError {
		t.Errorf("mandatory violation parsed wrong: %+v", res.Findings[0])
	}
	if res.Findings[1].PolicyID != "required-tags" || res.Findings[1].Severity != engines.SeverityWarning {
		t.Errorf("advisory violation parsed wrong: %+v", res.Findings[1])
	}
	if res.Findings[0].Resource != "my-bucket: aws:s3/bucket:Bucket" {
		t.Errorf("resource context missing: %+v", res.Findings[0])
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake pulumi log: %v", err)
	}
	if got := string(logged); !strings.Contains(got, "preview") ||
		!strings.Contains(got, "--policy-pack") ||
		!strings.Contains(got, "--stack dev") {
		t.Fatalf("pulumi invocation missing expected args: %s", got)
	}
}

func TestCrossGuardFindsRootPolicyPackFromLayeredEnv(t *testing.T) {
	output := `Policy Violations:
    [mandatory]  iac-studio v0.0.1  no-public-buckets (seed: aws:s3/bucket:Bucket)
    Public buckets are not allowed.
`
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakePulumiBinary(t, output, 1, "")

	root := t.TempDir()
	projectRoot := filepath.Join(root, "demo")
	envDir := filepath.Join(projectRoot, "environments", "dev")
	packDir := filepath.Join(projectRoot, "policies", "crossguard")
	for _, dir := range []string{envDir, packDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".iac-studio.json"), []byte(`{"tool":"pulumi","layout":"layered-v1"}`), 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "Pulumi.yaml"), []byte("name: demo-dev\nruntime: nodejs\n"), 0o644); err != nil {
		t.Fatalf("write Pulumi.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "Pulumi.dev.yaml"), []byte("config: {}\n"), 0o644); err != nil {
		t.Fatalf("write stack yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "PulumiPolicy.yaml"), []byte("name: root-policy\nruntime: nodejs\n"), 0o644); err != nil {
		t.Fatalf("write policy pack: %v", err)
	}

	res, err := New().Evaluate(context.Background(), engines.EvalInput{ProjectDir: envDir})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("want one finding from root policy pack, got %+v", res)
	}
	if res.Findings[0].PolicyFile != packDir {
		t.Errorf("finding should point at root policy pack %q, got %q", packDir, res.Findings[0].PolicyFile)
	}
}

func TestCrossGuardQuietWhenNoPolicyPacks(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakePulumiBinary(t, "", 0, "")

	root := t.TempDir()
	res, err := New().Evaluate(context.Background(), engines.EvalInput{ProjectDir: root})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Findings) != 0 || res.Error != "" {
		t.Fatalf("expected quiet empty result, got %+v", res)
	}
}
