// Package kics shells out to the KICS CLI against the project.
//
// KICS (Keeping Infrastructure as Code Secure) is Checkmarx's open-source
// IaC scanner — broad rule coverage across Terraform, CloudFormation,
// Kubernetes manifests, Dockerfile, and more. Rule coverage overlaps with
// Checkov and Trivy but often catches distinct issues, so running all
// three in a multi-scanner pass is the intended use case.
//
// Runs:
//
//	kics scan -p <projectDir> -o <tmpdir> --report-formats json
//
// KICS writes the report to a file (no stdout option for json), so we
// stage a tempdir, let it write, then read + parse results.json.
package kics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// Binary is the name (or full path) of the kics CLI. Overridable for tests.
var Binary = "kics"

type kicsScanner struct{}

// New constructs the KICS scanner.
func New() scanners.Scanner { return &kicsScanner{} }

func (k *kicsScanner) Name() string { return "kics" }

func (k *kicsScanner) Available() bool {
	_, err := exec.LookPath(Binary)
	return err == nil
}

// kicsReport mirrors the subset of KICS's results.json we consume. Findings
// are nested under queries[].files[] — one query can fire on multiple files.
type kicsReport struct {
	Queries []kicsQuery `json:"queries"`
}

type kicsQuery struct {
	QueryName  string     `json:"query_name"`
	QueryID    string     `json:"query_id"`
	Severity   string     `json:"severity"` // HIGH|MEDIUM|LOW|INFO
	Category   string     `json:"category"`
	CISDescription string `json:"cis_description_title"`
	Description string    `json:"description"`
	Files      []kicsFile `json:"files"`
}

type kicsFile struct {
	FileName       string `json:"file_name"`
	Line           int    `json:"line"`
	ResourceType   string `json:"resource_type"`
	ResourceName   string `json:"resource_name"`
	IssueType      string `json:"issue_type"`
	ExpectedValue  string `json:"expected_value"`
	ActualValue    string `json:"actual_value"`
}

func (k *kicsScanner) Scan(ctx context.Context, in scanners.ScanInput) (scanners.Result, error) {
	res := scanners.Result{Scanner: k.Name()}
	if !k.Available() {
		res.Error = "kics CLI not found on PATH — install from https://kics.io to enable this scanner"
		return res, nil
	}
	res.Available = true

	if in.ProjectDir == "" {
		res.Error = "kics scanner requires a project directory"
		return res, nil
	}
	if in.Tool == "ansible" {
		return res, nil
	}

	// KICS writes its JSON report to a file; stage a tempdir we can clean
	// up unconditionally on return.
	tmp, err := os.MkdirTemp("", "kics-report-*")
	if err != nil {
		res.Error = fmt.Sprintf("kics tempdir: %v", err)
		return res, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cmd := exec.CommandContext(ctx, Binary,
		"scan",
		"-p", in.ProjectDir,
		"-o", tmp,
		"--report-formats", "json",
		"--silent",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// KICS exits 50 when it finds issues — that's the documented
	// "vulnerabilities found" code, not a runtime error. We distinguish
	// by checking whether the results file exists.
	_ = cmd.Run()

	reportPath := filepath.Join(tmp, "results.json")
	data, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		// No results file means the run genuinely failed — surface stderr
		// so the user sees KICS's actual complaint.
		res.Error = formatExecError(readErr, stderr.Bytes())
		return res, readErr
	}

	var report kicsReport
	if err := json.Unmarshal(bytes.TrimSpace(data), &report); err != nil {
		res.Error = fmt.Sprintf("kics results.json not valid JSON: %v", err)
		return res, err
	}
	for _, q := range report.Queries {
		for _, f := range q.Files {
			resource := f.ResourceType + "." + f.ResourceName
			if f.ResourceName == "" {
				resource = f.FileName
			}
			res.Findings = append(res.Findings, scanners.Finding{
				ID:          q.QueryID,
				Severity:    normaliseSeverity(q.Severity),
				Category:    firstNonEmpty(strings.ToLower(q.Category), "compliance"),
				Framework:   "KICS",
				Title:       q.QueryName,
				Description: firstNonEmpty(q.CISDescription, q.Description),
				Resources:   []string{resource},
				Remediation: formatKICSRemediation(f),
			})
		}
	}
	return res, nil
}

// formatKICSRemediation composes a readable hint from expected/actual values
// when present. KICS doesn't have a free-text Remediation field the way
// Checkov/Trivy do; the expected-vs-actual pair is the closest surrogate.
func formatKICSRemediation(f kicsFile) string {
	if f.ExpectedValue != "" && f.ActualValue != "" {
		return fmt.Sprintf("expected %s, got %s", f.ExpectedValue, f.ActualValue)
	}
	if f.ExpectedValue != "" {
		return "expected " + f.ExpectedValue
	}
	return ""
}

// normaliseSeverity: KICS emits HIGH / MEDIUM / LOW / INFO. Note there's no
// CRITICAL in KICS's scheme — its HIGH already covers what other tools call
// critical, so we map HIGH → "high" rather than "critical" to stay consistent
// with the IsBlocking threshold.
func normaliseSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high":
		return scanners.SeverityHigh
	case "medium":
		return scanners.SeverityMedium
	case "low":
		return scanners.SeverityLow
	}
	return scanners.SeverityInfo
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func formatExecError(readErr error, stderr []byte) string {
	if len(stderr) > 0 {
		return fmt.Sprintf("kics: %s", strings.TrimSpace(string(stderr)))
	}
	return fmt.Sprintf("kics: no results file written (%v)", readErr)
}
