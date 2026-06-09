package drift

import (
	"strings"
	"testing"
)

func TestBuildCodifyRemediationProposal(t *testing.T) {
	proposal, err := BuildRemediationProposal(RemediationInput{
		ProjectName: "demo",
		Tool:        "terraform",
		Env:         "dev",
		Mode:        RemediationModeCodify,
		Findings: []DriftFinding{
			{
				Address:           "aws_s3_bucket.logs",
				Type:              "aws_s3_bucket",
				Name:              "logs",
				Status:            "drifted",
				Path:              "tags",
				ExpectedValue:     map[string]interface{}{"Owner": "platform"},
				CurrentValue:      map[string]interface{}{"Owner": "data"},
				Classification:    ClassificationLegitimateConfigChange,
				RecommendedAction: ActionCodifyOrAccept,
				Reason:            "Only metadata fields drifted.",
			},
		},
		Locations: map[string]ResourceLocation{
			"aws_s3_bucket.logs": {File: "main.tf", Line: 2},
		},
	})
	if err != nil {
		t.Fatalf("BuildRemediationProposal: %v", err)
	}

	if proposal.Title != "Codify drift for demo" {
		t.Fatalf("title = %q", proposal.Title)
	}
	if proposal.Branch != "iac-studio-drift-codify-demo-dev" {
		t.Fatalf("branch = %q", proposal.Branch)
	}
	if len(proposal.FileChanges) != 1 {
		t.Fatalf("file changes = %d, want 1", len(proposal.FileChanges))
	}
	change := proposal.FileChanges[0]
	if change.Action != "codify" || change.Path != "main.tf" || change.Line != 2 {
		t.Fatalf("unexpected change metadata: %#v", change)
	}
	if change.Before != `{"Owner":"platform"}` || change.After != `{"Owner":"data"}` {
		t.Fatalf("unexpected before/after: %#v", change)
	}
	if !strings.Contains(proposal.Body, "Run drift again") {
		t.Fatalf("body should include validation guidance: %s", proposal.Body)
	}
}

func TestBuildRevertRemediationProposalSkipsLegitimateDrift(t *testing.T) {
	proposal, err := BuildRemediationProposal(RemediationInput{
		ProjectName: "demo",
		Tool:        "opentofu",
		Mode:        RemediationModeRevert,
		Findings: []DriftFinding{
			{
				Address:           "aws_s3_bucket.logs",
				Type:              "aws_s3_bucket",
				Name:              "logs",
				Status:            "drifted",
				Path:              "tags",
				Classification:    ClassificationLegitimateConfigChange,
				RecommendedAction: ActionCodifyOrAccept,
				Reason:            "Only metadata fields drifted.",
			},
			{
				Address:           "aws_security_group.web",
				Type:              "aws_security_group",
				Name:              "web",
				Status:            "drifted",
				Path:              "ingress",
				ExpectedValue:     []interface{}{},
				CurrentValue:      []interface{}{map[string]interface{}{"cidr_blocks": []interface{}{"0.0.0.0/0"}}},
				Classification:    ClassificationUnauthorizedChange,
				RecommendedAction: ActionRevertOrCodifyAfterReview,
				Reason:            "Network drift can change reachability.",
			},
		},
		Locations: map[string]ResourceLocation{
			"aws_security_group.web": {File: "network.tf", Line: 12},
		},
	})
	if err != nil {
		t.Fatalf("BuildRemediationProposal: %v", err)
	}

	if proposal.Mode != RemediationModeRevert {
		t.Fatalf("mode = %q", proposal.Mode)
	}
	if len(proposal.Findings) != 1 || proposal.Findings[0].Address != "aws_security_group.web" {
		t.Fatalf("unexpected findings: %#v", proposal.Findings)
	}
	if len(proposal.Warnings) < 2 {
		t.Fatalf("expected skip and revert warnings, got %#v", proposal.Warnings)
	}
	if !strings.Contains(strings.Join(proposal.Warnings, "\n"), "legitimate configuration change") {
		t.Fatalf("expected legitimate drift skip warning: %#v", proposal.Warnings)
	}
	if !strings.Contains(proposal.Body, "provider-side change") {
		t.Fatalf("body should explain provider-side revert: %s", proposal.Body)
	}
}

func TestBuildRemediationProposalRejectsUnknownMode(t *testing.T) {
	_, err := BuildRemediationProposal(RemediationInput{
		ProjectName: "demo",
		Mode:        "delete-everything",
	})
	if err == nil {
		t.Fatal("expected unknown mode error")
	}
}
