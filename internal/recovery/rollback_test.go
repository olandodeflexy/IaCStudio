package recovery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildRollbackProposalIsFailClosedAndExplicit(t *testing.T) {
	target := StateSnapshot{
		ID:        "20260610T120000Z-terraform-apply-dev-abc12345",
		Project:   "demo",
		Tool:      "terraform",
		Env:       "dev",
		Command:   "apply",
		WorkDir:   "environments/dev",
		StatePath: "environments/dev/terraform.tfstate",
		StateSHA:  "abc1234567890abcdef",
		PlanPath:  "environments/dev/tfplan.json",
		PlanSHA:   "def4567890abcdef",
		CreatedAt: time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
	}
	current := target
	current.ID = "20260610T130000Z-terraform-apply-dev-current"
	current.CreatedAt = target.CreatedAt.Add(time.Hour)

	proposal, err := BuildRollbackProposal(RollbackInput{
		ProjectName:     "demo",
		TargetSnapshot:  target,
		CurrentSnapshot: &current,
	})
	if err != nil {
		t.Fatalf("build rollback proposal: %v", err)
	}

	if proposal.ID == "" || proposal.Branch == "" || proposal.CommitMessage == "" {
		t.Fatalf("expected PR metadata to be populated: %#v", proposal)
	}
	if proposal.TargetSnapshot.ID != target.ID || proposal.CurrentSnapshot == nil || proposal.CurrentSnapshot.ID != current.ID {
		t.Fatalf("unexpected snapshot linkage: %#v", proposal)
	}
	if proposal.Classification == nil ||
		!proposal.Classification.Summary.RequiresAcknowledgment ||
		proposal.Classification.Summary.Unknown != 1 {
		t.Fatalf("rollback proposal should fail closed with unknown classification: %#v", proposal.Classification)
	}
	for _, want := range []string{
		"not an unconditional undo button",
		"run semantic plan classification",
		target.ID,
		current.ID,
	} {
		if !strings.Contains(proposal.Body, want) {
			t.Fatalf("proposal body missing %q:\n%s", want, proposal.Body)
		}
	}
}

func TestBuildRollbackProposalWarnsWhenCheckpointLacksArtifacts(t *testing.T) {
	proposal, err := BuildRollbackProposal(RollbackInput{
		ProjectName: "demo",
		TargetSnapshot: StateSnapshot{
			ID:        "ansible-checkpoint",
			Project:   "demo",
			Tool:      "ansible",
			Command:   "playbook",
			CreatedAt: time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("build rollback proposal: %v", err)
	}

	warnings := strings.Join(proposal.Warnings, "\n")
	for _, want := range []string{
		"no local state file metadata",
		"no saved plan metadata",
		"fail-closed for this tool",
	} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("warnings missing %q: %#v", want, proposal.Warnings)
		}
	}
}

func TestRenderRollbackArtifactsIncludesRunbookAndMetadata(t *testing.T) {
	proposal, err := BuildRollbackProposal(RollbackInput{
		ProjectName: "demo",
		TargetSnapshot: StateSnapshot{
			ID:        "checkpoint-1",
			Project:   "demo",
			Tool:      "terraform",
			Command:   "apply",
			CreatedAt: time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("build rollback proposal: %v", err)
	}

	set, rendered, err := RenderRollbackArtifacts(proposal, time.Date(2026, 6, 10, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("render rollback artifacts: %v", err)
	}

	if set.ID != "rollback-demo-terraform-checkpoint-1" {
		t.Fatalf("artifact set id = %q", set.ID)
	}
	if set.Root != ".iac-studio/rollbacks/rollback-demo-terraform-checkpoint-1" {
		t.Fatalf("artifact root = %q", set.Root)
	}
	if len(rendered) != 3 || len(set.Files) != 3 {
		t.Fatalf("rendered %d artifacts, set has %d files", len(rendered), len(set.Files))
	}
	contents := map[string]string{}
	for _, artifact := range rendered {
		contents[artifact.Path] = artifact.Content
	}
	if !strings.Contains(contents[set.Root+"/README.md"], "does not apply infrastructure changes automatically") {
		t.Fatalf("runbook missing safety warning:\n%s", contents[set.Root+"/README.md"])
	}
	if contents[set.Root+"/proposal.md"] != proposal.Body {
		t.Fatal("proposal markdown should match proposal body")
	}
	var metadata RollbackArtifactSet
	if err := json.Unmarshal([]byte(contents[set.Root+"/proposal.json"]), &metadata); err != nil {
		t.Fatalf("metadata json: %v", err)
	}
	if metadata.Proposal.ID != proposal.ID || metadata.Files[0].Size == 0 {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	metadataFile := metadata.Files[len(metadata.Files)-1]
	if metadataFile.Path != set.Root+"/proposal.json" || metadataFile.Size != len(contents[set.Root+"/proposal.json"]) {
		t.Fatalf("metadata self-size = %#v, content length = %d", metadataFile, len(contents[set.Root+"/proposal.json"]))
	}
}
