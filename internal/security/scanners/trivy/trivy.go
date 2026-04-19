// Package trivy shells out to the Trivy CLI in config-scan mode against the
// project directory. Trivy absorbed tfsec in 2023, so a single adapter now
// covers both toolchains — users who had tfsec installed should install
// Trivy instead; the rule IDs are largely unchanged (AVD-xxx).
//
// The adapter runs:
//
//	trivy config <projectDir> --format json --quiet --severity CRITICAL,HIGH,MEDIUM,LOW,UNKNOWN
//
// and normalises the Misconfigurations array into the unified Finding shape.
package trivy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// Binary is the name (or full path) of the trivy CLI. Overridable for tests.
var Binary = "trivy"

type trivyScanner struct{}

// New constructs the Trivy scanner. Available() probes the binary per Scan
// call so a fresh install during server runtime is picked up immediately.
func New() scanners.Scanner { return &trivyScanner{} }

func (t *trivyScanner) Name() string { return "trivy" }

func (t *trivyScanner) Available() bool {
	_, err := exec.LookPath(Binary)
	return err == nil
}

// trivyReport models the subset of Trivy's JSON output we consume. Trivy
// emits a top-level object with ArtifactName / ArtifactType and a Results
// array; each entry carries a Misconfigurations array for IaC findings.
type trivyReport struct {
	Results []trivyResult `json:"Results"`
}

type trivyResult struct {
	Target            string                    `json:"Target"` // e.g., "main.tf"
	Class             string                    `json:"Class"`  // "config" for IaC
	Type              string                    `json:"Type"`   // "terraform", "cloudformation", etc.
	Misconfigurations []trivyMisconfiguration   `json:"Misconfigurations"`
}

type trivyMisconfiguration struct {
	Type           string `json:"Type"`
	ID             string `json:"ID"`       // e.g., "AVD-AWS-0088"
	AVDID          string `json:"AVDID"`    // same as ID on recent versions
	Title          string `json:"Title"`
	Description    string `json:"Description"`
	Message        string `json:"Message"`
	Resolution     string `json:"Resolution"`
	Severity       string `json:"Severity"` // uppercase
	PrimaryURL     string `json:"PrimaryURL"`
	CauseMetadata  struct {
		Resource string `json:"Resource"`
		Provider string `json:"Provider"`
		Service  string `json:"Service"`
	} `json:"CauseMetadata"`
}

func (t *trivyScanner) Scan(ctx context.Context, in scanners.ScanInput) (scanners.Result, error) {
	res := scanners.Result{Scanner: t.Name()}
	if !t.Available() {
		res.Error = "trivy CLI not found on PATH — install from https://trivy.dev to enable this scanner (replaces tfsec)"
		return res, nil
	}
	res.Available = true

	if in.ProjectDir == "" {
		res.Error = "trivy scanner requires a project directory"
		return res, nil
	}
	if in.Tool == "ansible" {
		// Trivy config doesn't target Ansible playbooks — short-circuit
		// quietly so the multi-scanner pass doesn't emit noise.
		return res, nil
	}

	cmd := exec.CommandContext(ctx, Binary,
		"config",
		in.ProjectDir,
		"--format", "json",
		"--quiet",
		"--severity", "CRITICAL,HIGH,MEDIUM,LOW,UNKNOWN",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Trivy exits 0 even with findings (unlike Checkov's default). An actual
	// error is a non-zero exit with no JSON body.
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		res.Error = formatExecError(err, stderr.Bytes())
		return res, err
	}

	var report trivyReport
	trimmed := bytes.TrimSpace(stdout.Bytes())
	if len(trimmed) == 0 {
		// No findings → Trivy can emit empty stdout. Treat as clean.
		return res, nil
	}
	if err := json.Unmarshal(trimmed, &report); err != nil {
		res.Error = fmt.Sprintf("trivy output not valid JSON: %v", err)
		return res, err
	}
	for _, r := range report.Results {
		for _, m := range r.Misconfigurations {
			id := m.AVDID
			if id == "" {
				id = m.ID
			}
			resource := m.CauseMetadata.Resource
			if resource == "" {
				// Fall back to the target file so the UI can at least
				// deep-link to the right file even when Trivy can't name
				// the specific resource.
				resource = r.Target
			}
			res.Findings = append(res.Findings, scanners.Finding{
				ID:          id,
				Severity:    normaliseSeverity(m.Severity),
				Category:    "compliance",
				Framework:   "Trivy",
				Title:       m.Title,
				Description: firstNonEmpty(m.Description, m.Message),
				Resources:   []string{resource},
				Remediation: firstNonEmpty(m.Resolution, m.PrimaryURL),
			})
		}
	}
	return res, nil
}

// normaliseSeverity maps Trivy's UPPERCASE labels to the scanner-world
// lowercase vocabulary. UNKNOWN severities fall through to "info" so they
// surface but don't block apply.
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

// firstNonEmpty picks the first string argument that isn't empty after trim.
// Trivy fills Description / Message / Resolution inconsistently by rule, so
// the UI-facing fields fall back across multiple source attributes.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func formatExecError(err error, stderr []byte) string {
	if len(stderr) > 0 {
		return fmt.Sprintf("trivy: %s", strings.TrimSpace(string(stderr)))
	}
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Sprintf("trivy: %s", strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Sprintf("trivy: %v", err)
}
