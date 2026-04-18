package conftest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
)

// fakeBinary writes a shell script that echoes the given stdout and exits
// with the given code. Used to stand in for the real conftest CLI in tests.
func fakeBinary(t *testing.T, stdout string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-scripted fake binary not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "conftest")
	// The 'IAC_EOF' delimiter is single-quoted, so the heredoc body is
	// emitted verbatim — no shell expansion or quoting required on stdout.
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

// scaffoldProject seeds a minimal policies/opa/ tree so Evaluate's existence
// check passes.
func scaffoldProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, PoliciesDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, PoliciesDir, "policy.rego"), []byte("package terraform\n\ndeny[\"x\"] { true }"), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return root
}

// TestConftestEngineNotAvailable — when the binary is missing we surface a
// clear Error rather than crashing the multi-engine run.
func TestConftestEngineNotAvailable(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = "/nonexistent/conftest-please-dont-exist"

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

// TestConftestEngineParsesJSONOutput round-trips a realistic conftest JSON
// report — two failures + one warning — into typed Findings.
func TestConftestEngineParsesJSONOutput(t *testing.T) {
	report := []conftestReport{{
		Filename:  "/tmp/tfplan.json",
		Namespace: "terraform.tags",
		Failures: []conftestRuleResult{
			{Msg: "missing Owner tag"},
			{Msg: "missing CostCenter tag"},
		},
		Warnings: []conftestRuleResult{
			{Msg: "bucket name not PascalCase"},
		},
	}}
	raw, _ := json.Marshal(report)

	orig := Binary
	t.Cleanup(func() { Binary = orig })
	// conftest exits 1 when there are findings — mirror that so the adapter's
	// exit-code handling is exercised end-to-end.
	Binary = fakeBinary(t, string(raw), 1)

	root := scaffoldProject(t)
	res, err := New().Evaluate(context.Background(), engines.EvalInput{
		ProjectDir: root,
		PlanJSON:   []byte(`{"format_version":"1.0"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.Available {
		t.Errorf("Available should be true with a real binary: %+v", res)
	}
	if len(res.Findings) != 3 {
		t.Fatalf("want 3 findings, got %d: %+v", len(res.Findings), res.Findings)
	}

	severities := map[engines.Severity]int{}
	for _, f := range res.Findings {
		severities[f.Severity]++
		if f.Engine != "conftest" {
			t.Errorf("finding engine wrong: %+v", f)
		}
		if f.PolicyFile != "/tmp/tfplan.json" {
			t.Errorf("finding should carry filename from report: %+v", f)
		}
	}
	if severities[engines.SeverityError] != 2 || severities[engines.SeverityWarning] != 1 {
		t.Errorf("severity split wrong: %v", severities)
	}
}

// TestConftestEngineRequiresPlanJSON — same contract as the embedded OPA
// adapter: without a plan, tell the user to run terraform plan.
func TestConftestEngineRequiresPlanJSON(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "[]", 0)

	root := scaffoldProject(t)
	res, _ := New().Evaluate(context.Background(), engines.EvalInput{ProjectDir: root})
	if res.Error == "" {
		t.Error("expected an Error explaining plan JSON requirement")
	}
}

// TestConftestEngineQuietWhenNoPolicies — new projects with no policies/opa/
// shouldn't produce noise in the Policy Studio.
func TestConftestEngineQuietWhenNoPolicies(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "[]", 0)

	res, _ := New().Evaluate(context.Background(), engines.EvalInput{
		ProjectDir: t.TempDir(),
		PlanJSON:   []byte(`{}`),
	})
	if len(res.Findings) != 0 || res.Error != "" {
		t.Errorf("expected clean empty result, got %+v", res)
	}
}
