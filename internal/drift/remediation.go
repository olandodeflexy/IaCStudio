package drift

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	RemediationModeCodify = "codify"
	RemediationModeRevert = "revert"
)

// ResourceLocation points a finding back to source code when the parser can
// identify the resource block that produced it.
type ResourceLocation struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
}

// RemediationInput is the provider-neutral input for building a drift PR draft.
type RemediationInput struct {
	ProjectName string
	Tool        string
	Env         string
	Mode        string
	Findings    []DriftFinding
	Locations   map[string]ResourceLocation
}

// RemediationFileChange describes the file or runbook change a PR should carry.
type RemediationFileChange struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Action  string `json:"action"`
	Address string `json:"address"`
	Field   string `json:"field,omitempty"`
	Summary string `json:"summary"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
}

// RemediationProposal is a PR-ready remediation draft. It intentionally avoids
// mutating source or cloud resources; later provider integrations can convert
// these file-change notes into exact patches or apply runbooks.
type RemediationProposal struct {
	Mode          string                  `json:"mode"`
	Title         string                  `json:"title"`
	Branch        string                  `json:"branch"`
	CommitMessage string                  `json:"commit_message"`
	Body          string                  `json:"body"`
	Findings      []DriftFinding          `json:"findings"`
	FileChanges   []RemediationFileChange `json:"file_changes"`
	Warnings      []string                `json:"warnings,omitempty"`
}

// BuildRemediationProposal converts active drift findings into a conservative
// PR draft. Codify means "accept the observed state into IaC"; revert means
// "bring live infrastructure back to the IaC value."
func BuildRemediationProposal(input RemediationInput) (RemediationProposal, error) {
	mode := strings.TrimSpace(strings.ToLower(input.Mode))
	if mode != RemediationModeCodify && mode != RemediationModeRevert {
		return RemediationProposal{}, errors.New("remediation mode must be codify or revert")
	}

	projectName := strings.TrimSpace(input.ProjectName)
	if projectName == "" {
		projectName = "project"
	}
	tool := strings.TrimSpace(input.Tool)
	if tool == "" {
		tool = "terraform"
	}

	proposal := RemediationProposal{
		Mode:          mode,
		Title:         remediationTitle(mode, projectName),
		Branch:        remediationBranch(mode, projectName, input.Env),
		CommitMessage: remediationCommitMessage(mode, projectName),
	}

	var skippedLegitimate int
	for _, finding := range input.Findings {
		if finding.Suppressed {
			continue
		}
		if mode == RemediationModeRevert && finding.Classification == ClassificationLegitimateConfigChange {
			skippedLegitimate++
			continue
		}
		change, warnings := remediationChange(mode, finding, input.Locations[finding.Address])
		proposal.Findings = append(proposal.Findings, finding)
		proposal.FileChanges = append(proposal.FileChanges, change)
		proposal.Warnings = append(proposal.Warnings, warnings...)
	}

	if skippedLegitimate > 0 {
		proposal.Warnings = append(proposal.Warnings,
			fmt.Sprintf("%d legitimate configuration change finding(s) were left out of the revert draft; codify is usually the safer workflow for those.", skippedLegitimate))
	}
	if len(proposal.Findings) == 0 {
		proposal.Warnings = append(proposal.Warnings, "No active drift findings matched this remediation mode.")
	}
	if mode == RemediationModeRevert {
		proposal.Warnings = append(proposal.Warnings, "Revert drafts do not edit IaC files; they document the provider-side change needed to bring live infrastructure back to code.")
	}

	proposal.Body = remediationBody(projectName, tool, input.Env, proposal)
	return proposal, nil
}

func remediationTitle(mode, projectName string) string {
	switch mode {
	case RemediationModeCodify:
		return fmt.Sprintf("Codify drift for %s", projectName)
	case RemediationModeRevert:
		return fmt.Sprintf("Revert unauthorized drift for %s", projectName)
	default:
		return fmt.Sprintf("Resolve drift for %s", projectName)
	}
}

func remediationBranch(mode, projectName, env string) string {
	parts := []string{"iac-studio", "drift", mode, slug(projectName)}
	if strings.TrimSpace(env) != "" {
		parts = append(parts, slug(env))
	}
	return strings.Join(parts, "-")
}

func remediationCommitMessage(mode, projectName string) string {
	switch mode {
	case RemediationModeCodify:
		return fmt.Sprintf("Codify drift for %s", projectName)
	case RemediationModeRevert:
		return fmt.Sprintf("Document drift revert for %s", projectName)
	default:
		return fmt.Sprintf("Resolve drift for %s", projectName)
	}
}

func remediationChange(mode string, finding DriftFinding, location ResourceLocation) (RemediationFileChange, []string) {
	change := RemediationFileChange{
		Path:    location.File,
		Line:    location.Line,
		Address: finding.Address,
		Field:   finding.Path,
	}
	var warnings []string
	if change.Path == "" {
		change.Path = "manual-remediation.md"
		warnings = append(warnings, fmt.Sprintf("No source file location was available for %s; the draft includes manual remediation notes.", finding.Address))
	}

	switch mode {
	case RemediationModeCodify:
		change.Action = "codify"
		change.Before = formatRemediationValue(finding.ExpectedValue)
		change.After = formatRemediationValue(finding.CurrentValue)
		switch finding.Status {
		case "drifted":
			change.Summary = fmt.Sprintf("Update %s %s to the current state value.", finding.Address, fallbackField(finding.Path))
			if finding.Classification == ClassificationUnauthorizedChange {
				warnings = append(warnings, fmt.Sprintf("%s is classified as unauthorized drift; review ownership before accepting it into code.", finding.Address))
			}
		case "unmanaged":
			change.Action = "manual"
			change.Summary = fmt.Sprintf("Import or add unmanaged resource %s to IaC after ownership review.", finding.Address)
		case "missing":
			change.Action = "manual"
			change.Summary = fmt.Sprintf("Apply or import %s so state and code agree; codifying is not enough for missing state.", finding.Address)
		default:
			change.Action = "manual"
			change.Summary = fmt.Sprintf("Review %s and update IaC if the drift is intentional.", finding.Address)
		}
	case RemediationModeRevert:
		change.Action = "revert"
		change.Before = formatRemediationValue(finding.CurrentValue)
		change.After = formatRemediationValue(finding.ExpectedValue)
		switch finding.Status {
		case "drifted":
			change.Summary = fmt.Sprintf("Restore live %s %s to the value declared in code.", finding.Address, fallbackField(finding.Path))
		case "unmanaged":
			change.Action = "manual"
			change.Summary = fmt.Sprintf("Remove unmanaged resource %s or import it intentionally after review.", finding.Address)
		case "missing":
			change.Action = "manual"
			change.Summary = fmt.Sprintf("Recreate or import %s because it exists in code but is missing from state.", finding.Address)
		default:
			change.Action = "manual"
			change.Summary = fmt.Sprintf("Review %s and choose a provider-side revert action.", finding.Address)
		}
	}
	return change, warnings
}

func remediationBody(projectName, tool, env string, proposal RemediationProposal) string {
	var b strings.Builder
	b.WriteString("## Summary\n")
	b.WriteString(fmt.Sprintf("- Project: `%s`\n", projectName))
	b.WriteString(fmt.Sprintf("- Tool: `%s`\n", tool))
	if strings.TrimSpace(env) != "" {
		b.WriteString(fmt.Sprintf("- Environment: `%s`\n", env))
	}
	b.WriteString(fmt.Sprintf("- Mode: `%s`\n", proposal.Mode))
	b.WriteString(fmt.Sprintf("- Findings: `%d`\n\n", len(proposal.Findings)))

	b.WriteString("## Proposed remediation\n")
	if len(proposal.FileChanges) == 0 {
		b.WriteString("- No file or runbook changes were generated.\n")
	}
	for _, change := range proposal.FileChanges {
		location := change.Path
		if change.Line > 0 {
			location = fmt.Sprintf("%s:%d", change.Path, change.Line)
		}
		if location == "" {
			location = "manual remediation"
		}
		b.WriteString(fmt.Sprintf("- `%s` `%s`: %s", change.Action, change.Address, change.Summary))
		if change.Field != "" {
			b.WriteString(fmt.Sprintf(" Field: `%s`.", change.Field))
		}
		b.WriteString(fmt.Sprintf(" Location: `%s`.\n", location))
	}

	if len(proposal.Warnings) > 0 {
		b.WriteString("\n## Review notes\n")
		for _, warning := range proposal.Warnings {
			b.WriteString(fmt.Sprintf("- %s\n", warning))
		}
	}

	b.WriteString("\n## Validation\n")
	b.WriteString("- Run plan/classification before applying any remediation.\n")
	b.WriteString("- Run drift again after the change to confirm the findings are resolved.\n")
	return b.String()
}

func formatRemediationValue(value interface{}) string {
	if value == nil {
		return "not set"
	}
	if s, ok := value.(string); ok {
		return s
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func fallbackField(path string) string {
	if strings.TrimSpace(path) == "" {
		return "configuration"
	}
	return path
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		keep := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if keep {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	s := strings.Trim(out.String(), "-")
	if s == "" {
		return "project"
	}
	return s
}
