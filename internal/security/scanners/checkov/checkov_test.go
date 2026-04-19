package checkov

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// fakeBinary writes a POSIX shell script that echoes stdout and exits with
// the given code. Skipped on Windows — the whole adapter shells out, so the
// tests are inherently POSIX-only on our CI.
func fakeBinary(t *testing.T, stdout string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-scripted fake binary not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "checkov")
	// Single-quoted heredoc delimiter → body emitted verbatim, no expansion.
	script := "#!/usr/bin/env bash\ncat <<'IAC_EOF'\n" + stdout + "\nIAC_EOF\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	return path
}

// TestCheckovNotAvailable — the binary-missing path must not crash the
// multi-scanner pass; Available() returns false and Result.Error explains.
func TestCheckovNotAvailable(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = "/nonexistent/checkov-please-dont-exist"

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Available {
		t.Error("Available() should be false when binary is missing")
	}
	if res.Error == "" {
		t.Error("Result.Error should explain the missing binary")
	}
}

// TestCheckovParsesArrayOutput verifies the dual-shape decoder handles the
// array form Checkov emits when multiple frameworks run, mapping each
// failed_check to a Finding with the right severity and framework.
func TestCheckovParsesArrayOutput(t *testing.T) {
	body := `[
      {
        "check_type": "terraform",
        "results": {
          "failed_checks": [
            {
              "check_id": "CKV_AWS_18",
              "check_name": "Ensure the S3 bucket has access logging enabled",
              "severity": "MEDIUM",
              "guideline": "https://docs.bridgecrew.io/docs/s3_13",
              "file_path": "/demo/main.tf",
              "file_line_range": [1, 6],
              "resource": "aws_s3_bucket.data",
              "description": "logging is required"
            },
            {
              "check_id": "CKV_AWS_20",
              "check_name": "Ensure the S3 bucket does not allow public access",
              "severity": "HIGH",
              "resource": "aws_s3_bucket.data"
            }
          ]
        }
      }
    ]`
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, body, 0)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !res.Available {
		t.Fatalf("available should be true with fake binary: %+v", res)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
	var sawHigh, sawMedium bool
	for _, f := range res.Findings {
		if f.Framework != "Checkov" {
			t.Errorf("framework should be Checkov, got %q", f.Framework)
		}
		switch f.Severity {
		case scanners.SeverityHigh:
			sawHigh = true
		case scanners.SeverityMedium:
			sawMedium = true
		}
		if len(f.Resources) == 0 {
			t.Errorf("finding should carry resource address: %+v", f)
		}
	}
	if !sawHigh || !sawMedium {
		t.Errorf("severity map missed expected inputs: %+v", res.Findings)
	}
}

// TestCheckovParsesSingleObjectOutput — when only one framework runs,
// Checkov emits a bare object rather than an array. The decoder must
// handle both shapes.
func TestCheckovParsesSingleObjectOutput(t *testing.T) {
	body := `{
      "check_type": "terraform",
      "results": {
        "failed_checks": [
          {
            "check_id": "CKV_AWS_21",
            "check_name": "Ensure all data stored in the S3 bucket is securely encrypted at rest",
            "severity": "CRITICAL",
            "resource": "aws_s3_bucket.data"
          }
        ]
      }
    }`
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, body, 0)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(res.Findings))
	}
	if res.Findings[0].Severity != scanners.SeverityCritical {
		t.Errorf("severity should be critical, got %q", res.Findings[0].Severity)
	}
}

// TestCheckovAnsibleShortCircuit — Ansible-only projects return a clean
// empty Result instead of running Checkov unnecessarily.
func TestCheckovAnsibleShortCircuit(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	// Fake that would crash if actually invoked — ensures we short-circuit
	// before exec.
	Binary = fakeBinary(t, "should not be called", 99)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir(), Tool: "ansible"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Available {
		t.Error("Available should be true — binary exists even though we skip")
	}
	if len(res.Findings) != 0 {
		t.Errorf("Ansible project should produce no Checkov findings, got %+v", res.Findings)
	}
}

// TestCheckovRequiresProjectDir — the adapter surfaces a clear message when
// called without a project directory.
func TestCheckovRequiresProjectDir(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "[]", 0)

	res, _ := New().Scan(context.Background(), scanners.ScanInput{})
	if res.Error == "" {
		t.Error("Result.Error should explain missing project dir")
	}
}

// TestCheckovMalformedOutput — when stdout isn't JSON, surface the decode
// error on Result.Error instead of crashing.
func TestCheckovMalformedOutput(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "not json at all", 0)

	_, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err == nil {
		t.Error("malformed JSON should surface as an error")
	}
}

// TestCheckovNormaliseSeverity pins the uppercase → lowercase mapping so a
// future adapter edit can't silently drop severity classifications.
func TestCheckovNormaliseSeverity(t *testing.T) {
	cases := map[string]string{
		"CRITICAL": scanners.SeverityCritical,
		"High":     scanners.SeverityHigh,
		"medium":   scanners.SeverityMedium,
		"LOW":      scanners.SeverityLow,
		"":         scanners.SeverityInfo, // empty severity → info, never blocking
		"weird":    scanners.SeverityInfo,
	}
	for in, want := range cases {
		if got := normaliseSeverity(in); got != want {
			t.Errorf("normaliseSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}
