package engines

import (
	"context"
	"errors"
	"testing"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// stubEngine is a controllable PolicyEngine for tests.
type stubEngine struct {
	name      string
	available bool
	findings  []Finding
	err       error
}

func (s *stubEngine) Name() string   { return s.name }
func (s *stubEngine) Available() bool { return s.available }
func (s *stubEngine) Evaluate(context.Context, EvalInput) (Result, error) {
	return Result{Engine: s.name, Available: s.available, Findings: s.findings}, s.err
}

func TestSeverityIsBlocking(t *testing.T) {
	if !SeverityError.IsBlocking() {
		t.Error("error severity must block")
	}
	if SeverityWarning.IsBlocking() || SeverityInfo.IsBlocking() {
		t.Error("warning/info must not block")
	}
}

func TestResultHasBlocking(t *testing.T) {
	r := Result{Findings: []Finding{
		{Severity: SeverityWarning},
		{Severity: SeverityInfo},
	}}
	if r.HasBlocking() {
		t.Error("only warnings present, should not block")
	}
	r.Findings = append(r.Findings, Finding{Severity: SeverityError})
	if !r.HasBlocking() {
		t.Error("an error finding should block")
	}
}

// TestRunAllPreservesEngineErrors confirms that an engine returning an error
// still yields a Result so the UI can show "engine X failed" alongside the
// findings from other engines, instead of one bad engine blanking the whole
// multi-engine response.
func TestRunAllPreservesEngineErrors(t *testing.T) {
	a := &stubEngine{name: "a", available: true, findings: []Finding{
		{PolicyID: "a1", Severity: SeverityWarning, Message: "warn"},
	}}
	b := &stubEngine{name: "b", available: true, err: errors.New("kaboom")}
	c := &stubEngine{name: "c", available: false}

	results := RunAll(context.Background(), []PolicyEngine{a, b, c}, EvalInput{})
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	if results[0].Engine != "a" || len(results[0].Findings) != 1 {
		t.Errorf("engine a result wrong: %+v", results[0])
	}
	if results[1].Engine != "b" || results[1].Error == "" {
		t.Errorf("engine b should surface its error, got: %+v", results[1])
	}
	if results[2].Engine != "c" || results[2].Available {
		t.Errorf("engine c should report available=false, got: %+v", results[2])
	}
}

// TestMergeFindingsSortsBlockingFirst guarantees the UI gets a stable,
// severity-ranked feed regardless of engine ordering.
func TestMergeFindingsSortsBlockingFirst(t *testing.T) {
	results := []Result{
		{Engine: "z", Findings: []Finding{{Engine: "z", PolicyID: "info-1", Severity: SeverityInfo}}},
		{Engine: "a", Findings: []Finding{
			{Engine: "a", PolicyID: "warn-1", Severity: SeverityWarning},
			{Engine: "a", PolicyID: "err-1", Severity: SeverityError},
		}},
	}
	merged := MergeFindings(results)
	if len(merged) != 3 {
		t.Fatalf("want 3 merged findings, got %d", len(merged))
	}
	if merged[0].Severity != SeverityError {
		t.Errorf("error finding must come first, got %+v", merged[0])
	}
	if merged[1].Severity != SeverityWarning {
		t.Errorf("warning second, got %+v", merged[1])
	}
	if merged[2].Severity != SeverityInfo {
		t.Errorf("info last, got %+v", merged[2])
	}
}

// TestBuiltinEngineEmptyInput verifies the builtin engine returns a clean
// empty Result when handed nothing to evaluate, instead of erroring.
func TestBuiltinEngineEmptyInput(t *testing.T) {
	e := NewBuiltin()
	res, err := e.Evaluate(context.Background(), EvalInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Engine != "builtin" || !res.Available {
		t.Errorf("builtin always available, got %+v", res)
	}
	if len(res.Findings) != 0 {
		t.Errorf("empty input should produce zero findings, got %d", len(res.Findings))
	}
}

// TestBuiltinEngineCatchesUntaggedResource exercises the wrap end-to-end:
// a resource that violates the built-in tag-required rule should produce a
// Finding with engine="builtin" and the expected severity.
func TestBuiltinEngineCatchesUntaggedResource(t *testing.T) {
	e := NewBuiltin()
	in := EvalInput{
		Resources: []parser.Resource{
			{
				ID:   "r1",
				Type: "aws_s3_bucket",
				Name: "data",
				// No tags → triggers tag-required (error severity).
				Properties: map[string]any{},
			},
		},
	}
	res, err := e.Evaluate(context.Background(), in)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Findings) == 0 {
		t.Fatal("expected at least one finding from default rules")
	}
	var sawBuiltin bool
	for _, f := range res.Findings {
		if f.Engine != "builtin" {
			t.Errorf("all findings should have engine=builtin, got %q", f.Engine)
		}
		if f.PolicyID == "" || f.Message == "" {
			t.Errorf("finding missing required fields: %+v", f)
		}
		sawBuiltin = true
	}
	if !sawBuiltin {
		t.Error("never saw a builtin finding")
	}
}
