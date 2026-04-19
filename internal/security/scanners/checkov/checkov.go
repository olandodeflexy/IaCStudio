// Package checkov shells out to the Checkov CLI against the project directory.
//
// Checkov is one of the most widely deployed IaC security scanners in CI
// pipelines. It ships hundreds of built-in policies covering CIS / NIST /
// PCI / HIPAA / SOC2 plus custom policy support. The adapter runs
//
//	checkov -d <projectDir> -o json --quiet --skip-path ...
//
// and normalises the failed_checks array into the unified Finding shape.
// A graceful "not installed" path returns an informative Error so the
// multi-scanner pass keeps working for users who only have the built-in
// graph scanner.
package checkov

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// Binary is the name (or full path) of the checkov CLI. Overridable for
// tests that stand in a shell-scripted fake on disk.
var Binary = "checkov"

type checkovScanner struct{}

// New constructs the Checkov scanner. Available() probes the binary at Scan
// time so a fresh install during server runtime is picked up on the next
// scan.
func New() scanners.Scanner { return &checkovScanner{} }

func (c *checkovScanner) Name() string { return "checkov" }

func (c *checkovScanner) Available() bool {
	_, err := exec.LookPath(Binary)
	return err == nil
}

// checkovReport mirrors the relevant subset of Checkov's --output json shape.
// Checkov emits either a single object (when only one framework runs) or an
// array of one object per framework. We always normalise to the array form.
type checkovReport struct {
	CheckType string        `json:"check_type"`
	Results   checkovCheckset `json:"results"`
}

type checkovCheckset struct {
	FailedChecks []checkovCheck `json:"failed_checks"`
	// passed_checks / parsing_errors / skipped_checks intentionally not
	// modeled — findings are the only thing we surface today.
}

type checkovCheck struct {
	CheckID      string          `json:"check_id"`
	CheckName    string          `json:"check_name"`
	Severity     string          `json:"severity"` // uppercase in Checkov output
	Guideline    string          `json:"guideline"`
	FilePath     string          `json:"file_path"`
	FileLineRange []int          `json:"file_line_range"`
	Resource     string          `json:"resource"` // e.g., "aws_s3_bucket.data"
	Description  string          `json:"description,omitempty"`
	BCCheckID    string          `json:"bc_check_id,omitempty"`
	Details      json.RawMessage `json:"details,omitempty"`
}

func (c *checkovScanner) Scan(ctx context.Context, in scanners.ScanInput) (scanners.Result, error) {
	res := scanners.Result{Scanner: c.Name()}
	if !c.Available() {
		res.Error = "checkov CLI not found on PATH — install from https://www.checkov.io to enable this scanner"
		return res, nil
	}
	res.Available = true

	if in.ProjectDir == "" {
		res.Error = "checkov scanner requires a project directory"
		return res, nil
	}
	// Short-circuit for Ansible-only projects. Checkov supports Ansible but
	// that isn't the primary use case here, and running Checkov against a
	// pure-Ansible project produces noise without findings. Tool is
	// informational — we still scan when empty.
	if in.Tool == "ansible" {
		return res, nil
	}

	cmd := exec.CommandContext(ctx, Binary,
		"-d", in.ProjectDir,
		"--output", "json",
		"--quiet",                        // drop framing banners
		"--soft-fail",                    // exit 0 even when findings exist
		"--framework", "terraform_plan,terraform",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		res.Error = formatExecError(err, stderr.Bytes())
		return res, err
	}

	reports, err := decodeReports(stdout.Bytes())
	if err != nil {
		res.Error = fmt.Sprintf("checkov output not valid JSON: %v", err)
		return res, err
	}
	for _, rep := range reports {
		for _, check := range rep.Results.FailedChecks {
			res.Findings = append(res.Findings, scanners.Finding{
				ID:          check.CheckID,
				Severity:    normaliseSeverity(check.Severity),
				Category:    "compliance",
				Framework:   "Checkov",
				Title:       check.CheckName,
				Description: check.Description,
				Resources:   []string{check.Resource},
				Remediation: check.Guideline,
			})
		}
	}
	return res, nil
}

// decodeReports accepts Checkov's dual output shape (single object OR array)
// and always returns an array, so downstream code has one case to handle.
func decodeReports(data []byte) ([]checkovReport, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	// Array form.
	if trimmed[0] == '[' {
		var out []checkovReport
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	// Single-object form.
	var single checkovReport
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return nil, err
	}
	return []checkovReport{single}, nil
}

// normaliseSeverity maps Checkov's uppercase labels to the scanner-world
// lowercase vocabulary defined in scanners/scanners.go. "CRITICAL" → "critical".
// Unknown values fall through to "info" so a missing severity never blocks
// apply accidentally.
func normaliseSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return scanners.SeverityCritical
	case "high":
		return scanners.SeverityHigh
	case "medium":
		return scanners.SeverityMedium
	case "low":
		return scanners.SeverityLow
	}
	return scanners.SeverityInfo
}

// formatExecError prefers the explicit stderr buffer so users see Checkov's
// actual complaint instead of a bare exit code. ExitError.Stderr is used as
// a last-resort fallback.
func formatExecError(err error, stderr []byte) string {
	if len(stderr) > 0 {
		return fmt.Sprintf("checkov: %s", strings.TrimSpace(string(stderr)))
	}
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Sprintf("checkov: %s", strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Sprintf("checkov: %v", err)
}
