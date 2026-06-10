package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	iacplan "github.com/iac-studio/iac-studio/internal/plan"
)

const rollbackArtifactRoot = ".iac-studio/rollbacks"

// RollbackProposal is a review artifact, not an executable undo operation. It
// identifies the target checkpoint and carries a fail-closed semantic
// classification until a generated rollback plan is reviewed.
type RollbackProposal struct {
	ID              string                        `json:"id"`
	Title           string                        `json:"title"`
	Branch          string                        `json:"branch"`
	CommitMessage   string                        `json:"commit_message"`
	Body            string                        `json:"body"`
	Tool            string                        `json:"tool"`
	Env             string                        `json:"env,omitempty"`
	WorkDir         string                        `json:"work_dir"`
	TargetSnapshot  StateSnapshot                 `json:"target_snapshot"`
	CurrentSnapshot *StateSnapshot                `json:"current_snapshot,omitempty"`
	Classification  *iacplan.ClassificationResult `json:"classification"`
	Warnings        []string                      `json:"warnings,omitempty"`
}

type RollbackInput struct {
	ProjectName     string
	TargetSnapshot  StateSnapshot
	CurrentSnapshot *StateSnapshot
}

type RollbackArtifactSet struct {
	ID        string                 `json:"id"`
	Root      string                 `json:"root"`
	CreatedAt time.Time              `json:"created_at"`
	Proposal  RollbackProposal       `json:"proposal"`
	Files     []RollbackArtifactFile `json:"files"`
}

type RollbackArtifactFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Size    int    `json:"size"`
}

type RenderedRollbackArtifact struct {
	RollbackArtifactFile
	Content string `json:"-"`
}

func BuildRollbackProposal(input RollbackInput) (RollbackProposal, error) {
	projectName := strings.TrimSpace(input.ProjectName)
	if projectName == "" {
		projectName = input.TargetSnapshot.Project
	}
	if strings.TrimSpace(projectName) == "" {
		return RollbackProposal{}, errors.New("project name is required")
	}
	if strings.TrimSpace(input.TargetSnapshot.ID) == "" {
		return RollbackProposal{}, errors.New("target snapshot is required")
	}

	id := rollbackID(projectName, input.TargetSnapshot)
	proposal := RollbackProposal{
		ID:              id,
		Title:           fmt.Sprintf("Rollback %s to checkpoint %s", projectName, input.TargetSnapshot.ID),
		Branch:          "iac-studio-rollback-" + slug(projectName) + "-" + slug(input.TargetSnapshot.ID),
		CommitMessage:   fmt.Sprintf("Document rollback proposal for %s", projectName),
		Tool:            input.TargetSnapshot.Tool,
		Env:             input.TargetSnapshot.Env,
		WorkDir:         input.TargetSnapshot.WorkDir,
		TargetSnapshot:  input.TargetSnapshot,
		CurrentSnapshot: input.CurrentSnapshot,
		Classification: iacplan.UnknownClassification(
			"rollback reverse plan has not been generated yet; create and review a fresh plan before applying",
		),
		Warnings: rollbackWarnings(input.TargetSnapshot),
	}
	proposal.Body = renderRollbackBody(proposal)
	return proposal, nil
}

func RenderRollbackArtifacts(proposal RollbackProposal, createdAt time.Time) (RollbackArtifactSet, []RenderedRollbackArtifact, error) {
	if strings.TrimSpace(proposal.ID) == "" {
		return RollbackArtifactSet{}, nil, errors.New("rollback proposal id is required")
	}
	if strings.TrimSpace(proposal.Title) == "" {
		return RollbackArtifactSet{}, nil, errors.New("rollback proposal title is required")
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	} else {
		createdAt = createdAt.UTC()
	}

	root := rollbackArtifactRoot + "/" + cleanArtifactSegment(proposal.ID)
	rendered := []RenderedRollbackArtifact{
		{
			RollbackArtifactFile: RollbackArtifactFile{
				Path:    root + "/README.md",
				Kind:    "runbook",
				Summary: "Human-readable rollback review runbook.",
			},
			Content: renderRollbackRunbook(proposal, createdAt),
		},
		{
			RollbackArtifactFile: RollbackArtifactFile{
				Path:    root + "/proposal.md",
				Kind:    "proposal",
				Summary: "Markdown rollback proposal for code review.",
			},
			Content: proposal.Body,
		},
	}

	metadataContent, err := renderRollbackMetadata(root, createdAt, proposal, rendered)
	if err != nil {
		return RollbackArtifactSet{}, nil, err
	}
	rendered = append(rendered, RenderedRollbackArtifact{
		RollbackArtifactFile: RollbackArtifactFile{
			Path:    root + "/proposal.json",
			Kind:    "metadata",
			Summary: "Machine-readable rollback proposal metadata.",
		},
		Content: metadataContent,
	})

	set := RollbackArtifactSet{
		ID:        cleanArtifactSegment(proposal.ID),
		Root:      root,
		CreatedAt: createdAt,
		Proposal:  proposal,
		Files:     make([]RollbackArtifactFile, 0, len(rendered)),
	}
	for i := range rendered {
		rendered[i].Size = len(rendered[i].Content)
		set.Files = append(set.Files, rendered[i].RollbackArtifactFile)
	}
	return set, rendered, nil
}

func rollbackWarnings(target StateSnapshot) []string {
	warnings := []string{
		"This proposal does not apply infrastructure changes automatically.",
		"Generate and review a fresh plan before applying any rollback.",
	}
	if target.StatePath == "" {
		warnings = append(warnings, "The target checkpoint has no local state file metadata.")
	}
	if target.PlanPath == "" {
		warnings = append(warnings, "The target checkpoint has no saved plan metadata.")
	}
	switch target.Tool {
	case "terraform", "opentofu":
	default:
		warnings = append(warnings, "Semantic rollback plan classification is currently fail-closed for this tool.")
	}
	return warnings
}

func rollbackID(projectName string, target StateSnapshot) string {
	parts := []string{"rollback", slug(projectName), slug(target.Tool)}
	if target.Env != "" {
		parts = append(parts, slug(target.Env))
	}
	parts = append(parts, slug(target.ID))
	return strings.Join(parts, "-")
}

func renderRollbackBody(proposal RollbackProposal) string {
	var b strings.Builder
	b.WriteString("## Summary\n")
	b.WriteString("IaC Studio generated this rollback proposal for review. It is not an unconditional undo button and does not apply changes automatically.\n\n")
	b.WriteString("## Target checkpoint\n")
	writeSnapshotBullets(&b, proposal.TargetSnapshot)
	if proposal.CurrentSnapshot != nil {
		b.WriteString("\n## Current checkpoint\n")
		writeSnapshotBullets(&b, *proposal.CurrentSnapshot)
	}
	b.WriteString("\n## Classification\n")
	if proposal.Classification != nil {
		b.WriteString("- Semantic result: `")
		b.WriteString(proposal.Classification.Summary.Text)
		b.WriteString("`\n")
		b.WriteString("- Requires acknowledgement: `")
		b.WriteString(fmt.Sprintf("%t", proposal.Classification.Summary.RequiresAcknowledgment))
		b.WriteString("`\n")
	}
	b.WriteString("\n## Required review steps\n")
	b.WriteString("- Restore or generate the rollback candidate from the target checkpoint and current cloud state.\n")
	b.WriteString("- Run init and plan in the recorded work directory.\n")
	b.WriteString("- Confirm the generated plan matches the intended rollback, then run semantic plan classification.\n")
	b.WriteString("- Run policy and security checks before any apply.\n")
	b.WriteString("- Apply only after reviewers acknowledge risky, destructive, or unknown changes.\n")
	if len(proposal.Warnings) > 0 {
		b.WriteString("\n## Warnings\n")
		for _, warning := range proposal.Warnings {
			b.WriteString("- ")
			b.WriteString(warning)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func writeSnapshotBullets(b *strings.Builder, snapshot StateSnapshot) {
	b.WriteString(fmt.Sprintf("- ID: `%s`\n", snapshot.ID))
	b.WriteString(fmt.Sprintf("- Tool: `%s`\n", snapshot.Tool))
	if snapshot.Env != "" {
		b.WriteString(fmt.Sprintf("- Environment: `%s`\n", snapshot.Env))
	}
	if snapshot.WorkDir != "" {
		b.WriteString(fmt.Sprintf("- Work directory: `%s`\n", snapshot.WorkDir))
	}
	b.WriteString(fmt.Sprintf("- Command: `%s`\n", snapshot.Command))
	b.WriteString(fmt.Sprintf("- Created: `%s`\n", snapshot.CreatedAt.Format(time.RFC3339)))
	if snapshot.StatePath != "" {
		b.WriteString(fmt.Sprintf("- State: `%s` (`%s`)\n", snapshot.StatePath, shortHash(snapshot.StateSHA)))
	}
	if snapshot.PlanPath != "" {
		b.WriteString(fmt.Sprintf("- Saved plan: `%s` (`%s`)\n", snapshot.PlanPath, shortHash(snapshot.PlanSHA)))
	}
}

func renderRollbackRunbook(proposal RollbackProposal, createdAt time.Time) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(proposal.Title)
	b.WriteString("\n\n")
	b.WriteString("Generated by IaC Studio for human review. This artifact does not apply infrastructure changes automatically.\n\n")
	b.WriteString("## PR metadata\n")
	b.WriteString(fmt.Sprintf("- Branch: `%s`\n", proposal.Branch))
	b.WriteString(fmt.Sprintf("- Commit message: `%s`\n", proposal.CommitMessage))
	b.WriteString(fmt.Sprintf("- Generated: `%s`\n", createdAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Tool: `%s`\n", proposal.Tool))
	if proposal.Env != "" {
		b.WriteString(fmt.Sprintf("- Environment: `%s`\n", proposal.Env))
	}
	b.WriteString("\n")
	b.WriteString(proposal.Body)
	b.WriteString("\n\n## Operator checklist\n")
	b.WriteString("- Do not apply this proposal directly.\n")
	b.WriteString("- Generate a fresh rollback plan from the target checkpoint.\n")
	b.WriteString("- Run semantic plan classification and policy gates before apply.\n")
	b.WriteString("- Preserve this artifact with the incident or change record.\n")
	return b.String()
}

func renderRollbackMetadata(root string, createdAt time.Time, proposal RollbackProposal, rendered []RenderedRollbackArtifact) (string, error) {
	files := make([]RollbackArtifactFile, 0, len(rendered)+1)
	for _, file := range rendered {
		files = append(files, RollbackArtifactFile{
			Path:    file.Path,
			Kind:    file.Kind,
			Summary: file.Summary,
			Size:    len(file.Content),
		})
	}
	files = append(files, RollbackArtifactFile{
		Path:    root + "/proposal.json",
		Kind:    "metadata",
		Summary: "Machine-readable rollback proposal metadata.",
	})
	payload := struct {
		ID        string                 `json:"id"`
		Root      string                 `json:"root"`
		CreatedAt time.Time              `json:"created_at"`
		Proposal  RollbackProposal       `json:"proposal"`
		Files     []RollbackArtifactFile `json:"files"`
	}{
		ID:        cleanArtifactSegment(proposal.ID),
		Root:      root,
		CreatedAt: createdAt,
		Proposal:  proposal,
		Files:     files,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("rendering rollback metadata: %w", err)
	}
	return string(data) + "\n", nil
}

func cleanArtifactSegment(value string) string {
	return slug(value)
}

func shortHash(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
