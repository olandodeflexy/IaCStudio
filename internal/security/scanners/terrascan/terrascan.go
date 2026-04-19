// Package terrascan shells out to the Terrascan CLI against the project.
//
// Terrascan is a CNCF sandbox project focused on compliance checks — CIS
// benchmarks, NIST, PCI, HIPAA, SOC2. Its rule set complements Checkov's
// and Trivy's, so users running all three get broader coverage than any
// single tool provides.
//
// Runs:
//
//	terrascan scan -d <projectDir> -o json --silent
//
// and normalises violations into the unified Finding shape.
package terrascan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// Binary is the name (or full path) of the terrascan CLI. Overridable for tests.
var Binary = "terrascan"

type terrascanScanner struct{}

// New constructs the Terrascan scanner.
func New() scanners.Scanner { return &terrascanScanner{} }

func (t *terrascanScanner) Name() string { return "terrascan" }

func (t *terrascanScanner) Available() bool {
	_, err := exec.LookPath(Binary)
	return err == nil
}

// terrascanOutput mirrors the relevant subset of `terrascan scan -o json`.
// The CLI emits {"results": {"violations": [...]}} — we only consume the
// violations array.
type terrascanOutput struct {
	Results struct {
		Violations []terrascanViolation `json:"violations"`
	} `json:"results"`
}

type terrascanViolation struct {
	RuleName    string `json:"rule_name"`
	Description string `json:"description"`
	RuleID      string `json:"rule_id"`
	Severity    string `json:"severity"` // HIGH|MEDIUM|LOW in Terrascan
	Category    string `json:"category"`
	ResourceName string `json:"resource_name"`
	ResourceType string `json:"resource_type"`
	File        string `json:"file"`
	LineNumber  int    `json:"line"`
}

func (t *terrascanScanner) Scan(ctx context.Context, in scanners.ScanInput) (scanners.Result, error) {
	res := scanners.Result{Scanner: t.Name()}
	if !t.Available() {
		res.Error = "terrascan CLI not found on PATH — install from https://runterrascan.io to enable this scanner"
		return res, nil
	}
	res.Available = true

	if in.ProjectDir == "" {
		res.Error = "terrascan scanner requires a project directory"
		return res, nil
	}
	if in.Tool == "ansible" {
		// Terrascan does support Ansible (iac-type k8s/ansible), but running
		// the default Terraform scan against a pure-Ansible project is
		// noise — skip for now and revisit when we add iac-type flag
		// plumbing.
		return res, nil
	}

	cmd := exec.CommandContext(ctx, Binary,
		"scan",
		"-d", in.ProjectDir,
		"-o", "json",
		"--silent",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Terrascan exits non-zero when violations exist. Only treat non-zero
	// exit with no JSON body as a real failure.
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		res.Error = formatExecError("terrascan", err, stderr.Bytes())
		return res, err
	}

	trimmed := bytes.TrimSpace(stdout.Bytes())
	if len(trimmed) == 0 {
		return res, nil
	}
	var out terrascanOutput
	if err := json.Unmarshal(trimmed, &out); err != nil {
		res.Error = fmt.Sprintf("terrascan output not valid JSON: %v", err)
		return res, err
	}
	for _, v := range out.Results.Violations {
		resource := v.ResourceType + "." + v.ResourceName
		if v.ResourceName == "" {
			resource = v.File
		}
		res.Findings = append(res.Findings, scanners.Finding{
			ID:          v.RuleID,
			Severity:    normaliseSeverity(v.Severity),
			Category:    firstNonEmpty(strings.ToLower(v.Category), "compliance"),
			Framework:   "Terrascan",
			Title:       v.RuleName,
			Description: v.Description,
			Resources:   []string{resource},
		})
	}
	return res, nil
}

// normaliseSeverity: Terrascan only emits HIGH/MEDIUM/LOW. Map to the
// scanner-world vocabulary; treat anything unrecognised as "info" so a
// missing severity never blocks apply.
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

// firstNonEmpty returns the first non-blank string in order.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// formatExecError surfaces the explicit stderr buffer when available so
// callers see Terrascan's actual complaint rather than a bare exit code.
func formatExecError(tool string, err error, stderr []byte) string {
	if len(stderr) > 0 {
		return fmt.Sprintf("%s: %s", tool, strings.TrimSpace(string(stderr)))
	}
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Sprintf("%s: %s", tool, strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Sprintf("%s: %v", tool, err)
}
