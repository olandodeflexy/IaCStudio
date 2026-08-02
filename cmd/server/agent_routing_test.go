package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

type countingToolEvaluator struct {
	calls int
	entry mcpairlock.ToolInventoryEntry
	err   error
}

func (e *countingToolEvaluator) EvaluateTool(_, _, _ string) (mcpairlock.ToolInventoryEntry, error) {
	e.calls++
	return e.entry, e.err
}

func TestNewAgentRoutingServicesRequiresToolEvaluator(t *testing.T) {
	_, err := newAgentRoutingServices(t.TempDir(), nil, successfulToolInvoker)
	if !errors.Is(err, agentrouting.ErrToolEvaluatorRequired) {
		t.Fatalf("newAgentRoutingServices(nil) error = %v, want %v", err, agentrouting.ErrToolEvaluatorRequired)
	}
}

func TestNewAgentRoutingServicesRequiresToolInvoker(t *testing.T) {
	_, err := newAgentRoutingServices(t.TempDir(), &countingToolEvaluator{}, nil)
	if !errors.Is(err, agentrouting.ErrToolInvokerRequired) {
		t.Fatalf("newAgentRoutingServices(nil invoker) error = %v, want %v", err, agentrouting.ErrToolInvokerRequired)
	}
}

func TestNewAgentRoutingServicesFailsClosedAndAuditsMissingPolicy(t *testing.T) {
	evaluator := &countingToolEvaluator{}
	services, err := newAgentRoutingServices(t.TempDir(), evaluator, successfulToolInvoker)
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

func TestNewAgentRoutingServicesRejectsCorruptPolicyStore(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".iac-studio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-routing-policies.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	_, err := newAgentRoutingServices(root, &countingToolEvaluator{}, successfulToolInvoker)
	if !errors.Is(err, agentrouting.ErrInvalidPolicyStore) {
		t.Fatalf("newAgentRoutingServices() error = %v, want ErrInvalidPolicyStore", err)
	}
}

func TestNewAgentRoutingServicesComposesGuardedExecutor(t *testing.T) {
	request := agentrouting.Request{
		Project:      "demo",
		ProviderID:   "codex",
		ConnectionID: "aws-prod",
		ServerID:     "aws",
		ToolName:     "list_buckets",
		Mode:         agentruns.ModeReadOnly,
		Risk:         mcpairlock.RiskReadOnly,
	}
	evaluator := &countingToolEvaluator{entry: mcpairlock.ToolInventoryEntry{
		ServerID: request.ServerID,
		Name:     request.ToolName,
		Risk:     request.Risk,
		Decision: mcpairlock.ToolDecision{
			Status:          "allowed",
			Allowed:         true,
			Risk:            request.Risk,
			UntrustedOutput: true,
		},
	}}
	invocations := 0
	services, err := newAgentRoutingServices(t.TempDir(), evaluator, func(_ context.Context, call mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
		invocations++
		if call.ServerID != request.ServerID || call.ToolName != request.ToolName {
			t.Fatalf("tool call = %+v, want exact authorized route", call)
		}
		return mcpairlock.NewToolCallResult([]byte("inventory ready"), false), nil
	})
	if err != nil {
		t.Fatalf("newAgentRoutingServices(): %v", err)
	}
	if err := services.policies.Save(agentrouting.PolicyScope{
		Project:    request.Project,
		ProviderID: request.ProviderID,
	}, agentrouting.Policy{Rules: []agentrouting.Rule{{
		Project:          request.Project,
		ProviderID:       request.ProviderID,
		ConnectionID:     request.ConnectionID,
		ServerID:         request.ServerID,
		ToolName:         request.ToolName,
		Modes:            []agentruns.Mode{request.Mode},
		Risk:             request.Risk,
		Effect:           agentrouting.EffectAllow,
		ApprovalRequired: false,
	}}}); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	run, err := services.runs.Create(agentruns.CreateRequest{
		Project:    request.Project,
		Prompt:     "inventory the project",
		ProviderID: request.ProviderID,
		Mode:       request.Mode,
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	arguments, err := mcpairlock.ParseToolCallArguments([]byte(`{"region":"us-east-1"}`))
	if err != nil {
		t.Fatalf("ParseToolCallArguments(): %v", err)
	}

	result, err := services.executor.Execute(context.Background(), run.ID, request, arguments)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !result.Invoked || result.Result == nil || result.Result.Output != "inventory ready" || invocations != 1 || evaluator.calls != 1 {
		t.Fatalf("Execute() = %+v, invocations = %d, evaluations = %d", result, invocations, evaluator.calls)
	}
}

func successfulToolInvoker(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
	return mcpairlock.NewToolCallResult(nil, false), nil
}
