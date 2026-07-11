package agentrouting

import (
	"errors"
	"testing"

	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

type fakeToolEvaluator struct {
	entry    mcpairlock.ToolInventoryEntry
	err      error
	calls    int
	serverID string
	project  string
	toolName string
}

func (f *fakeToolEvaluator) EvaluateTool(serverID, project, toolName string) (mcpairlock.ToolInventoryEntry, error) {
	f.calls++
	f.serverID = serverID
	f.project = project
	f.toolName = toolName
	return f.entry, f.err
}

func evaluationEntry(request Request, decision mcpairlock.ToolDecision) mcpairlock.ToolInventoryEntry {
	return mcpairlock.ToolInventoryEntry{
		ServerID: request.ServerID,
		Name:     request.ToolName,
		Risk:     decision.Risk,
		Decision: decision,
	}
}

func TestAuthorizerForwardsExactAirlockScope(t *testing.T) {
	policy, request, decision := readOnlyEvaluation()
	evaluator := &fakeToolEvaluator{entry: evaluationEntry(request, decision)}
	authorizer, err := NewAuthorizer(policy, evaluator)
	if err != nil {
		t.Fatalf("NewAuthorizer(): %v", err)
	}

	got := authorizer.Authorize(request)
	if got.Status != DecisionAllowed || !got.Allowed {
		t.Fatalf("Authorize() = %+v, want allowed", got)
	}
	if evaluator.calls != 1 || evaluator.serverID != request.ServerID || evaluator.project != request.Project || evaluator.toolName != request.ToolName {
		t.Fatalf("EvaluateTool calls = %d (%q, %q, %q), want exact request scope", evaluator.calls, evaluator.serverID, evaluator.project, evaluator.toolName)
	}
}

func TestAuthorizerRejectsBeforeCallingAirlock(t *testing.T) {
	tests := []struct {
		name         string
		mutatePolicy func(*Policy)
		mutate       func(*Request)
		wantReason   DecisionReason
	}{
		{name: "invalid request", mutate: func(request *Request) {
			request.ConnectionID = ""
		}, wantReason: ReasonInvalidRequest},
		{name: "unsafe mode and risk", mutate: func(request *Request) {
			request.Risk = mcpairlock.RiskCloudMutation
		}, wantReason: ReasonModeRiskMismatch},
		{name: "no matching rule", mutatePolicy: func(policy *Policy) {
			policy.Rules[0].ToolName = "other_tool"
		}, wantReason: ReasonNoMatchingRule},
		{name: "deny rule", mutatePolicy: func(policy *Policy) {
			policy.Rules[0].Effect = EffectDeny
		}, wantReason: ReasonPolicyDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, decision := readOnlyEvaluation()
			evaluator := &fakeToolEvaluator{entry: evaluationEntry(request, decision)}
			if test.mutatePolicy != nil {
				test.mutatePolicy(&policy)
			}
			authorizer, err := NewAuthorizer(policy, evaluator)
			if err != nil {
				t.Fatalf("NewAuthorizer(): %v", err)
			}
			if test.mutate != nil {
				test.mutate(&request)
			}

			got := authorizer.Authorize(request)
			if got.Status != DecisionDenied || got.Reason != test.wantReason || evaluator.calls != 0 {
				t.Fatalf("Authorize() = %+v, calls = %d; want denied %q without Airlock call", got, evaluator.calls, test.wantReason)
			}
		})
	}
}

func TestAuthorizerFailsClosedOnAirlockErrorsAndMismatches(t *testing.T) {
	airlockErr := errors.New("inventory unavailable")
	tests := []struct {
		name       string
		mutate     func(*fakeToolEvaluator)
		wantReason DecisionReason
	}{
		{name: "evaluation error", mutate: func(evaluator *fakeToolEvaluator) {
			evaluator.err = airlockErr
		}, wantReason: ReasonAirlockUnavailable},
		{name: "server mismatch", mutate: func(evaluator *fakeToolEvaluator) {
			evaluator.entry.ServerID = "other-server"
		}, wantReason: ReasonAirlockToolMismatch},
		{name: "tool mismatch", mutate: func(evaluator *fakeToolEvaluator) {
			evaluator.entry.Name = "other_tool"
		}, wantReason: ReasonAirlockToolMismatch},
		{name: "inventory risk mismatch", mutate: func(evaluator *fakeToolEvaluator) {
			evaluator.entry.Risk = mcpairlock.RiskCloudMutation
		}, wantReason: ReasonAirlockToolMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, decision := readOnlyEvaluation()
			evaluator := &fakeToolEvaluator{entry: evaluationEntry(request, decision)}
			test.mutate(evaluator)
			authorizer, err := NewAuthorizer(policy, evaluator)
			if err != nil {
				t.Fatalf("NewAuthorizer(): %v", err)
			}

			got := authorizer.Authorize(request)
			if got.Status != DecisionDenied || got.Reason != test.wantReason || got.Allowed || got.ApprovalRequired {
				t.Fatalf("Authorize() = %+v, want denied %q", got, test.wantReason)
			}
		})
	}
}

func TestAuthorizerSnapshotsValidatedPolicy(t *testing.T) {
	policy, request, decision := readOnlyEvaluation()
	evaluator := &fakeToolEvaluator{entry: evaluationEntry(request, decision)}
	authorizer, err := NewAuthorizer(policy, evaluator)
	if err != nil {
		t.Fatalf("NewAuthorizer(): %v", err)
	}

	policy.Rules[0].Effect = EffectDeny
	policy.Rules[0].Modes[0] = "invalid"
	got := authorizer.Authorize(request)
	if got.Status != DecisionAllowed || !got.Allowed {
		t.Fatalf("Authorize() = %+v, want immutable policy snapshot to remain allowed", got)
	}
}

func TestNewAuthorizerRejectsInvalidDependencies(t *testing.T) {
	policy, _, _ := readOnlyEvaluation()
	if _, err := NewAuthorizer(policy, nil); !errors.Is(err, ErrToolEvaluatorRequired) {
		t.Fatalf("NewAuthorizer(nil) error = %v, want ErrToolEvaluatorRequired", err)
	}
	var typedNil *fakeToolEvaluator
	if _, err := NewAuthorizer(policy, typedNil); !errors.Is(err, ErrToolEvaluatorRequired) {
		t.Fatalf("NewAuthorizer(typed nil) error = %v, want ErrToolEvaluatorRequired", err)
	}

	bad := validRule()
	bad.ConnectionID = ""
	if _, err := NewAuthorizer(Policy{Rules: []Rule{bad}}, &fakeToolEvaluator{}); !errors.Is(err, ErrInvalidRule) {
		t.Fatalf("NewAuthorizer(invalid policy) error = %v, want ErrInvalidRule", err)
	}
}
