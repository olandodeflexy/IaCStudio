package kics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// fakeBinary writes a shell script that receives KICS-style flags, writes a
// pre-specified results.json into the -o output directory, and exits with
// the KICS convention (50 when findings exist, 0 when clean). The stdout
// argument is unused by KICS itself — we pass it via a synthetic file write
// path instead.
func fakeBinary(t *testing.T, resultsJSON string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-scripted fake binary not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kics")
	// The fake parses its own CLI to find the value after `-o`, writes
	// results.json there, then exits. Keeps the fake faithful to real KICS
	// behaviour without needing us to mock the flag parser in Go.
	script := fmt.Sprintf(`#!/usr/bin/env bash
outdir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) outdir="$2"; shift 2;;
    *)  shift;;
  esac
done
mkdir -p "$outdir"
cat > "$outdir/results.json" <<'IAC_EOF'
%s
IAC_EOF
exit %d
`, resultsJSON, exitCode)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	return path
}

func TestKICSNotAvailable(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = "/nonexistent/kics"

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Available || res.Error == "" {
		t.Errorf("expected unavailable + Error, got %+v", res)
	}
}

// TestKICSParsesQueriesAndFiles — each query.files[] entry becomes a
// Finding. KICS has a many-to-many shape (one query, many files) that the
// adapter flattens correctly.
func TestKICSParsesQueriesAndFiles(t *testing.T) {
	body := `{
      "queries": [
        {
          "query_name": "S3 Bucket ACL Allows Read to All Users",
          "query_id": "38c5ee0d-7f22-4260-ab72-5073048df100",
          "severity": "HIGH",
          "category": "Access Control",
          "description": "S3 bucket ACL must not grant public read",
          "files": [
            {
              "file_name": "main.tf",
              "line": 5,
              "resource_type": "aws_s3_bucket",
              "resource_name": "data",
              "expected_value": "acl should not be 'public-read'",
              "actual_value": "acl = 'public-read'"
            },
            {
              "file_name": "logs.tf",
              "line": 3,
              "resource_type": "aws_s3_bucket",
              "resource_name": "access_logs",
              "expected_value": "acl should not be 'public-read'",
              "actual_value": "acl = 'public-read'"
            }
          ]
        },
        {
          "query_name": "RDS instance without encryption",
          "query_id": "abc-123",
          "severity": "MEDIUM",
          "files": [
            {
              "file_name": "database.tf",
              "resource_type": "aws_db_instance",
              "resource_name": "primary"
            }
          ]
        }
      ]
    }`
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, body, 50) // KICS exits 50 when vulnerabilities found

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 3 {
		t.Fatalf("want 3 findings (2 from first query + 1 from second), got %d: %+v", len(res.Findings), res.Findings)
	}
	var sawHigh, sawMedium bool
	for _, f := range res.Findings {
		if f.Framework != "KICS" {
			t.Errorf("framework should be KICS, got %q", f.Framework)
		}
		switch f.Severity {
		case scanners.SeverityHigh:
			sawHigh = true
		case scanners.SeverityMedium:
			sawMedium = true
		}
	}
	if !sawHigh || !sawMedium {
		t.Errorf("severity map missed expected inputs: %+v", res.Findings)
	}
	// Remediation should be composed from expected/actual when present.
	for _, f := range res.Findings {
		if f.Severity == scanners.SeverityHigh && f.Remediation == "" {
			t.Errorf("HIGH findings had expected/actual values — remediation should be populated: %+v", f)
		}
	}
}

// TestKICSNoResultsFile — if KICS crashed before writing results.json, the
// adapter should surface a clear error instead of pretending the scan passed.
func TestKICSNoResultsFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-scripted fake binary not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kics")
	// A fake that exits non-zero without writing results.json.
	script := "#!/usr/bin/env bash\necho 'boom' >&2\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = path

	_, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err == nil {
		t.Error("missing results.json should surface as an error")
	}
}

func TestKICSAnsibleShortCircuit(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "{}", 0)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir(), Tool: "ansible"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("Ansible project should short-circuit, got %+v", res.Findings)
	}
}

// TestKICSNormaliseSeverity — KICS has no CRITICAL; HIGH is its top tier.
// We map it to "high" (not "critical") so IsBlocking still gates, but the
// unified display reflects the tool's own hierarchy.
func TestKICSNormaliseSeverity(t *testing.T) {
	cases := map[string]string{
		"HIGH":   scanners.SeverityHigh,
		"Medium": scanners.SeverityMedium,
		"low":    scanners.SeverityLow,
		"":       scanners.SeverityInfo,
		"INFO":   scanners.SeverityInfo,
		"weird":  scanners.SeverityInfo,
	}
	for in, want := range cases {
		if got := normaliseSeverity(in); got != want {
			t.Errorf("normaliseSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}
