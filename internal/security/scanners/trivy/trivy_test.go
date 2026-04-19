package trivy

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
	path := filepath.Join(dir, "trivy")
	// Single-quoted heredoc delimiter → body emitted verbatim.
	script := "#!/usr/bin/env bash\ncat <<'IAC_EOF'\n" + stdout + "\nIAC_EOF\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	return path
}

// TestTrivyNotAvailable — missing binary returns a clean, informative Result
// rather than crashing the multi-scanner pass.
func TestTrivyNotAvailable(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = "/nonexistent/trivy-please-dont-exist"

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

// TestTrivyParsesMisconfigurations covers the core mapping: each
// misconfiguration becomes a Finding with the right severity, framework,
// and resource wiring.
func TestTrivyParsesMisconfigurations(t *testing.T) {
	body := `{
      "ArtifactName": ".",
      "ArtifactType": "filesystem",
      "Results": [
        {
          "Target": "main.tf",
          "Class": "config",
          "Type": "terraform",
          "Misconfigurations": [
            {
              "Type": "Terraform Security Check",
              "ID": "AVD-AWS-0088",
              "AVDID": "AVD-AWS-0088",
              "Title": "S3 Data should be versioned",
              "Description": "Bucket does not have versioning enabled.",
              "Resolution": "Enable versioning",
              "Severity": "MEDIUM",
              "PrimaryURL": "https://avd.aquasec.com/misconfig/avd-aws-0088",
              "CauseMetadata": {
                "Resource": "aws_s3_bucket.data",
                "Provider": "aws",
                "Service": "s3"
              }
            },
            {
              "ID": "AVD-AWS-0132",
              "AVDID": "AVD-AWS-0132",
              "Title": "S3 encryption should use Customer Managed Keys",
              "Severity": "HIGH",
              "CauseMetadata": {
                "Resource": "aws_s3_bucket.data"
              }
            }
          ]
        }
      ]
    }`
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, body, 0)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
	var sawHigh, sawMedium bool
	for _, f := range res.Findings {
		if f.Framework != "Trivy" {
			t.Errorf("framework should be Trivy, got %q", f.Framework)
		}
		if len(f.Resources) == 0 || f.Resources[0] == "" {
			t.Errorf("resource should be populated: %+v", f)
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
}

// TestTrivyMissingResourceFallsBackToTarget — when CauseMetadata.Resource is
// empty, the Finding should still carry a useful address (the file path) so
// the UI can deep-link.
func TestTrivyMissingResourceFallsBackToTarget(t *testing.T) {
	body := `{
      "Results": [
        {
          "Target": "modules/networking/main.tf",
          "Misconfigurations": [
            {"ID": "AVD-X", "AVDID": "AVD-X", "Title": "Generic check", "Severity": "LOW"}
          ]
        }
      ]
    }`
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, body, 0)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Resources[0] != "modules/networking/main.tf" {
		t.Errorf("fallback to Target failed: %+v", res.Findings)
	}
}

// TestTrivyEmptyOutputIsQuiet — Trivy emits empty stdout when there are no
// findings; the adapter treats that as a clean no-op.
func TestTrivyEmptyOutputIsQuiet(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "", 0)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 0 || res.Error != "" {
		t.Errorf("empty output should yield clean empty result, got %+v", res)
	}
}

// TestTrivyAnsibleShortCircuit — Trivy config doesn't target Ansible; skip.
func TestTrivyAnsibleShortCircuit(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "should not be called", 99)

	res, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir(), Tool: "ansible"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("Ansible project should produce no Trivy findings, got %+v", res.Findings)
	}
}

// TestTrivyRequiresProjectDir — match the Checkov adapter's contract on
// missing project directory.
func TestTrivyRequiresProjectDir(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "{}", 0)

	res, _ := New().Scan(context.Background(), scanners.ScanInput{})
	if res.Error == "" {
		t.Error("Result.Error should explain missing project dir")
	}
}

// TestTrivyMalformedOutput — non-JSON stdout surfaces as an error rather than
// being swallowed into an empty Findings list.
func TestTrivyMalformedOutput(t *testing.T) {
	orig := Binary
	t.Cleanup(func() { Binary = orig })
	Binary = fakeBinary(t, "not json at all", 0)

	_, err := New().Scan(context.Background(), scanners.ScanInput{ProjectDir: t.TempDir()})
	if err == nil {
		t.Error("malformed JSON should surface as an error")
	}
}
