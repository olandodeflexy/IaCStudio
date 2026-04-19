package scanners

import (
	"context"
	"errors"
	"testing"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// stubScanner is a controllable Scanner for the RunAll / MergeFindings tests.
type stubScanner struct {
	name      string
	available bool
	findings  []Finding
	err       error
}

func (s *stubScanner) Name() string   { return s.name }
func (s *stubScanner) Available() bool { return s.available }
func (s *stubScanner) Scan(context.Context, ScanInput) (Result, error) {
	return Result{Scanner: s.name, Available: s.available, Findings: s.findings}, s.err
}

func TestIsBlocking(t *testing.T) {
	if !IsBlocking(SeverityCritical) || !IsBlocking(SeverityHigh) {
		t.Error("critical and high must block")
	}
	if IsBlocking(SeverityMedium) || IsBlocking(SeverityLow) || IsBlocking(SeverityInfo) {
		t.Error("medium/low/info must NOT block — the threshold is opinionated on purpose")
	}
}

func TestResultHasBlocking(t *testing.T) {
	r := Result{Findings: []Finding{{Severity: SeverityMedium}, {Severity: SeverityLow}}}
	if r.HasBlocking() {
		t.Error("no high/critical findings, should not block")
	}
	r.Findings = append(r.Findings, Finding{Severity: SeverityHigh})
	if !r.HasBlocking() {
		t.Error("a high finding should block")
	}
}

// TestRunAllPreservesErrors — a scanner that returns an error still produces
// a Result with that error on it, so UI can render partial output.
func TestRunAllPreservesErrors(t *testing.T) {
	a := &stubScanner{name: "a", available: true, findings: []Finding{{ID: "a1", Severity: SeverityMedium}}}
	b := &stubScanner{name: "b", available: true, err: errors.New("boom")}
	c := &stubScanner{name: "c", available: false}

	results := RunAll(context.Background(), []Scanner{a, b, c}, ScanInput{})
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	if results[0].Scanner != "a" || len(results[0].Findings) != 1 {
		t.Errorf("scanner a result wrong: %+v", results[0])
	}
	if results[1].Scanner != "b" || results[1].Error == "" {
		t.Errorf("scanner b should surface its error, got: %+v", results[1])
	}
	if results[2].Scanner != "c" || results[2].Available {
		t.Errorf("scanner c should report available=false, got: %+v", results[2])
	}
}

// TestMergeFindingsSortsBySeverity verifies critical comes before high comes
// before medium, regardless of scanner ordering.
func TestMergeFindingsSortsBySeverity(t *testing.T) {
	results := []Result{
		{Scanner: "z", Findings: []Finding{{ID: "z-info", Severity: SeverityInfo}}},
		{Scanner: "a", Findings: []Finding{
			{ID: "a-medium", Severity: SeverityMedium},
			{ID: "a-critical", Severity: SeverityCritical},
			{ID: "a-high", Severity: SeverityHigh},
		}},
	}
	merged := MergeFindings(results)
	want := []string{SeverityCritical, SeverityHigh, SeverityMedium, SeverityInfo}
	for i, s := range want {
		if merged[i].Severity != s {
			t.Errorf("at index %d want severity %q, got %q (%+v)", i, s, merged[i].Severity, merged[i])
		}
	}
}

// TestMergeFindingsTieBreakersAreDeterministic — two findings with identical
// severity + scanner + id + title should still sort deterministically via
// resource key, so CI output doesn't flap.
func TestMergeFindingsTieBreakersAreDeterministic(t *testing.T) {
	results := []Result{
		{Scanner: "s", Findings: []Finding{
			{ID: "dup", Severity: SeverityHigh, Title: "same", Resources: []string{"z-res"}},
			{ID: "dup", Severity: SeverityHigh, Title: "same", Resources: []string{"a-res"}},
		}},
	}
	merged := MergeFindings(results)
	if merged[0].Resources[0] != "a-res" {
		t.Errorf("resource tie-breaker should sort a-res first, got %+v", merged)
	}
}

// TestGraphScannerEmptyProjectIsQuiet — an empty project produces a clean
// Result, matching the "no noise for fresh projects" contract.
func TestGraphScannerEmptyProjectIsQuiet(t *testing.T) {
	s := NewGraph()
	res, err := s.Scan(context.Background(), ScanInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name() != "graph" || !s.Available() {
		t.Errorf("graph scanner metadata wrong")
	}
	if len(res.Findings) != 0 {
		t.Errorf("empty input should yield zero findings, got %+v", res.Findings)
	}
}

// TestGraphScannerFlagsExposedResource end-to-ends the wrap: a resource that
// the legacy graph scanner considers exposed should still surface as a
// scanners.Finding with Scanner="graph".
func TestGraphScannerFlagsExposedResource(t *testing.T) {
	// A classic bad pattern: an S3 bucket with public-read ACL.
	s := NewGraph()
	res, err := s.Scan(context.Background(), ScanInput{
		Resources: []parser.Resource{
			{
				ID:   "r1",
				Type: "aws_s3_bucket",
				Name: "public_data",
				Properties: map[string]any{
					"acl": "public-read",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) == 0 {
		t.Fatal("public-read S3 bucket should produce at least one finding")
	}
	if res.Scanner != "graph" {
		t.Errorf("wrong scanner name on result: %q", res.Scanner)
	}
}
