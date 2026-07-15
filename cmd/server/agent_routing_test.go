package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

type countingToolEvaluator struct {
	calls int
}

func (e *countingToolEvaluator) EvaluateTool(_, _, _ string) (mcpairlock.ToolInventoryEntry, error) {
	e.calls++
	return mcpairlock.ToolInventoryEntry{}, errors.New("unexpected Airlock evaluation")
}

func TestNewAgentRoutingServicesRequiresToolEvaluator(t *testing.T) {
	_, err := newAgentRoutingServices(nil)
	if !errors.Is(err, agentrouting.ErrToolEvaluatorRequired) {
		t.Fatalf("newAgentRoutingServices(nil) error = %v, want %v", err, agentrouting.ErrToolEvaluatorRequired)
	}
}

func TestNewAgentRoutingServicesFailsClosedAndAuditsMissingPolicy(t *testing.T) {
	evaluator := &countingToolEvaluator{}
	services, err := newAgentRoutingServices(evaluator)
	if err != nil {
		t.Fatalf("newAgentRoutingServices(): %v", err)
	}
	run, err := services.runs.Create(agentruns.CreateRequest{
		Project:    "demo",
		Prompt:     "inventory the project",
		ProviderID: "codex",
		Mode:       agentruns.ModeReadOnly,
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	result, err := services.router.Route(run.ID, agentrouting.Request{
		Project:      run.Project,
		ProviderID:   run.ProviderID,
		ConnectionID: "aws-prod",
		ServerID:     "aws",
		ToolName:     "list_buckets",
		Mode:         run.Mode,
		Risk:         mcpairlock.RiskReadOnly,
	})
	if err != nil {
		t.Fatalf("Route(): %v", err)
	}
	if result.Decision.Status != agentrouting.DecisionDenied || result.Decision.Reason != agentrouting.ReasonPolicyUnavailable {
		t.Fatalf("decision = %+v, want denied %q", result.Decision, agentrouting.ReasonPolicyUnavailable)
	}
	if result.Run.Status != agentruns.StatusFailed || !strings.Contains(result.Run.Error, string(agentrouting.ReasonPolicyUnavailable)) {
		t.Fatalf("recorded run = %+v, want audited policy denial", result.Run)
	}
	stored, ok := services.runs.Get(run.ID)
	if !ok || stored.Status != agentruns.StatusFailed || stored.Error != result.Run.Error {
		t.Fatalf("stored run = %+v, found = %t; want recorded result", stored, ok)
	}
	if evaluator.calls != 0 {
		t.Fatalf("Airlock evaluation calls = %d, want none without a scoped policy", evaluator.calls)
	}
}
