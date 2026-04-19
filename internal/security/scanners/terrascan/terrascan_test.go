package terrascan

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

func fakeBinary(t *testing.T, stdout string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-scripted fake binary not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "terrascan")
	script := "#!/usr/bin/env bash\ncat <<'IAC_EOF'\n" + stdout + "\nIAC_EOF\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	return path
}

func TestTerrascanNotAvailable(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = "/nonexistent/terrascan"

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Available || res.Error == "" {
		t.Errorf("expected unavailable + Error, got %+v", res)
	}
}

// TestTerrascanParsesViolations covers the core mapping: each violation
// becomes a Finding with the right severity, framework="Terrascan", and
// resource wiring (ResourceType.ResourceName).
func TestTerrascanParsesViolations(t *testing.T) {
	body := `{
      "results": {
        "violations": [
          {
            "rule_name": "s3EnforceUserACL",
            "description": "Ensure S3 buckets have ACLs set",
            "rule_id": "AC_AWS_0207",
            "severity": "HIGH",
            "category": "Identity and Access Management",
            "resource_name": "data",
            "resource_type": "aws_s3_bucket",
            "file": "main.tf",
            "line": 5
          },
          {
            "rule_name": "noLoggingForRDS",
            "description": "Ensure RDS backups are enabled",
            "rule_id": "AC_AWS_0053",
            "severity": "MEDIUM",
            "category": "Logging",
            "resource_name": "primary",
            "resource_type": "aws_db_instance",
            "file": "database.tf",
            "line": 12
          }
        ]
      }
    }`
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	// Terrascan exits 3 when violations exist — use that exit code so the
	// non-zero-with-stdout success path is exercised.
	Binary = fakeBinary(t, body, 3)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
	for _, f := range res.Findings {
		if f.Framework != "Terrascan" {
			t.Errorf("framework should be Terrascan, got %q", f.Framework)
		}
		if !filepath.IsAbs(f.Resources[0]) && len(f.Resources[0]) == 0 {
			t.Errorf("resource address should be populated: %+v", f)
		}
	}
	if res.Findings[0].Resources[0] != "aws_s3_bucket.data" {
		t.Errorf("first finding resource should be the composed TYPE.NAME, got %q", res.Findings[0].Resources[0])
	}
}

// TestTerrascanEmptyViolations — a clean scan (empty violations array)
// returns no findings and no error.
func TestTerrascanEmptyViolations(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, `{"results":{"violations":[]}}`, 0)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 0 || res.Error != "" {
		t.Errorf("clean scan should yield empty findings, got %+v", res)
	}
}

func TestTerrascanAnsibleShortCircuit(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "should not be called", 99)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir(), Tool: "ansible"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("Ansible project should produce no Terrascan findings, got %+v", res.Findings)
	}
}

func TestTerrascanNormaliseSeverity(t *testing.T) {
	cases := map[string]string{
		"HIGH":    scanners.SeverityHigh,
		"Medium":  scanners.SeverityMedium,
		"low":     scanners.SeverityLow,
		"":        scanners.SeverityInfo,
		"weird":   scanners.SeverityInfo,
		"CRITICAL": scanners.SeverityInfo, // Terrascan doesn't emit CRITICAL; stays info until/unless we learn otherwise
	}
	for in, want := range cases {
		if got := normaliseSeverity(in); got != want {
			t.Errorf("normaliseSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}
