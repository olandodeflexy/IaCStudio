package sentinel

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
)

// fakeBinary produces a shell script that echoes stdout and exits with the
// given code — used to simulate `sentinel apply` pass/fail behaviour.
func fakeBinary(t *testing.T, stdout string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-scripted fake binary not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sentinel")
	script := "#!/usr/bin/env bash\ncat <<'IAC_EOF'\n" + stdout + "\nIAC_EOF\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	return path
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

func scaffoldProject(t *testing.T, policyBody string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, PoliciesDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, PoliciesDir, "example.sentinel"), []byte(policyBody), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return root
}

// TestSentinelEngineNotAvailable — graceful "missing binary" path.
func TestSentinelEngineNotAvailable(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = "/nonexistent/sentinel-please-dont-exist"

	res, err := New().Evaluate(context.Background(), engines.EvalInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Available {
		t.Error("Available() should be false when binary is missing")
	}
	if res.Error == "" {
		t.Error("expected an Error explaining the missing binary")
	}
}

// TestSentinelEngineFailingPolicyBecomesFinding — exit code != 0 → one
// error-severity Finding per failing .sentinel file.
func TestSentinelEngineFailingPolicyBecomesFinding(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "Result: false\n  Description: tags missing", 1)

	root := scaffoldProject(t, `# policy body intentionally minimal for the test harness
main = rule { false }`)
	res, err := New().Evaluate(context.Background(), engines.EvalInput{
		ProjectDir: root,
		PlanJSON:   []byte(`{"resource_changes":[]}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.Available {
		t.Errorf("Available should be true with a fake binary on PATH: %+v", res)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.Engine != "sentinel" || f.Severity != engines.SeverityError {
		t.Errorf("finding shape wrong: %+v", f)
	}
	if f.PolicyFile == "" {
		t.Error("finding should record source policy path")
	}
}

// TestSentinelEnginePassingPolicyIsQuiet — exit 0 emits no Finding.
func TestSentinelEnginePassingPolicyIsQuiet(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "Result: true", 0)

	root := scaffoldProject(t, `main = rule { true }`)
	res, err := New().Evaluate(context.Background(), engines.EvalInput{
		ProjectDir: root,
		PlanJSON:   []byte(`{"resource_changes":[]}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("passing policy should produce no findings, got %+v", res.Findings)
	}
}

// TestSentinelEngineRequiresPlanJSON — consistent with OPA/Conftest.
func TestSentinelEngineRequiresPlanJSON(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "", 0)

	root := scaffoldProject(t, `main = rule { true }`)
	res, _ := New().Evaluate(context.Background(), engines.EvalInput{ProjectDir: root})
	if res.Error == "" {
		t.Error("expected an Error explaining plan JSON requirement")
	}
}

// TestSentinelEngineQuietWhenNoPolicies — no .sentinel files → empty result.
func TestSentinelEngineQuietWhenNoPolicies(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "", 0)

	res, _ := New().Evaluate(context.Background(), engines.EvalInput{
		ProjectDir: t.TempDir(),
		PlanJSON:   []byte(`{}`),
	})
	if len(res.Findings) != 0 || res.Error != "" {
		t.Errorf("expected clean empty result, got %+v", res)
	}
}
