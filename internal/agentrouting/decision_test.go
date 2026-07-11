package agentrouting

import (
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

func readOnlyEvaluation() (Policy, Request, mcpairlock.ToolDecision) {
	request := validRequest()
	request.Mode = agentruns.ModeReadOnly
	request.Risk = mcpairlock.RiskReadOnly

	rule := validRule()
	rule.Modes = []agentruns.Mode{request.Mode}
	rule.Risk = request.Risk
	rule.ApprovalRequired = false

	airlock := mcpairlock.ToolDecision{
		Status:          "allowed",
		Allowed:         true,
		Risk:            request.Risk,
		UntrustedOutput: true,
	}
	return Policy{Rules: []Rule{rule}}, request, airlock
}

func TestEvaluateAllowsOnlyWhenBothLayersAllow(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	decision := Evaluate(policy, request, airlock)
	if decision.Status != DecisionAllowed || !decision.Allowed || decision.ApprovalRequired || !decision.UntrustedOutput {
		t.Fatalf("Evaluate() = %+v, want allowed untrusted output", decision)
	}
}

func TestEvaluateRequiresApprovalFromEitherLayer(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Policy, *mcpairlock.ToolDecision)
	}{
		{name: "route policy", mutate: func(policy *Policy, _ *mcpairlock.ToolDecision) {
			policy.Rules[0].ApprovalRequired = true
		}},
		{name: "airlock", mutate: func(_ *Policy, airlock *mcpairlock.ToolDecision) {
			airlock.Status = "approval_required"
			airlock.Allowed = false
			airlock.ApprovalRequired = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, airlock := readOnlyEvaluation()
			test.mutate(&policy, &airlock)
			decision := Evaluate(policy, request, airlock)
			if decision.Status != DecisionApprovalRequired || decision.Allowed || !decision.ApprovalRequired || !decision.UntrustedOutput {
				t.Fatalf("Evaluate() = %+v, want approval required", decision)
			}
		})
	}
}

func TestModeAllowsRisk(t *testing.T) {
	tests := []struct {
		name string
		mode agentruns.Mode
		risk mcpairlock.ToolRisk
		want bool
	}{
		{name: "read-only inventory", mode: agentruns.ModeReadOnly, risk: mcpairlock.RiskReadOnly, want: true},
		{name: "read-only generation", mode: agentruns.ModeReadOnly, risk: mcpairlock.RiskGenerateCode, want: false},
		{name: "propose inventory", mode: agentruns.ModeProposeOnly, risk: mcpairlock.RiskReadOnly, want: true},
		{name: "propose generation", mode: agentruns.ModeProposeOnly, risk: mcpairlock.RiskGenerateCode, want: true},
		{name: "propose workspace mutation", mode: agentruns.ModeProposeOnly, risk: mcpairlock.RiskModifyWorkspace, want: false},
		{name: "approved cloud mutation", mode: agentruns.ModeApprovedExecute, risk: mcpairlock.RiskCloudMutation, want: true},
		{name: "approved destructive", mode: agentruns.ModeApprovedExecute, risk: mcpairlock.RiskDestructive, want: true},
		{name: "approved unknown", mode: agentruns.ModeApprovedExecute, risk: mcpairlock.RiskUnknown, want: false},
		{name: "unknown mode", mode: "execute", risk: mcpairlock.RiskReadOnly, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := modeAllowsRisk(test.mode, test.risk); got != test.want {
				t.Fatalf("modeAllowsRisk(%q, %q) = %v, want %v", test.mode, test.risk, got, test.want)
			}
		})
	}
}

func TestEvaluateFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*Policy, *Request, *mcpairlock.ToolDecision)
		wantReason DecisionReason
	}{
		{name: "invalid request", mutate: func(_ *Policy, request *Request, _ *mcpairlock.ToolDecision) {
			request.ConnectionID = ""
		}, wantReason: ReasonInvalidRequest},
		{name: "unsafe mode and risk", mutate: func(policy *Policy, request *Request, airlock *mcpairlock.ToolDecision) {
			request.Risk = mcpairlock.RiskCloudMutation
			policy.Rules[0].Risk = request.Risk
			policy.Rules[0].ApprovalRequired = true
			airlock.Risk = request.Risk
		}, wantReason: ReasonModeRiskMismatch},
		{name: "invalid policy", mutate: func(policy *Policy, _ *Request, _ *mcpairlock.ToolDecision) {
			bad := validRule()
			bad.ConnectionID = ""
			policy.Rules = append(policy.Rules, bad)
		}, wantReason: ReasonInvalidPolicy},
		{name: "no matching rule", mutate: func(policy *Policy, _ *Request, _ *mcpairlock.ToolDecision) {
			policy.Rules[0].ToolName = "other_tool"
		}, wantReason: ReasonNoMatchingRule},
		{name: "deny rule", mutate: func(policy *Policy, _ *Request, _ *mcpairlock.ToolDecision) {
			policy.Rules[0].Effect = EffectDeny
		}, wantReason: ReasonPolicyDenied},
		{name: "airlock risk mismatch", mutate: func(_ *Policy, _ *Request, airlock *mcpairlock.ToolDecision) {
			airlock.Risk = mcpairlock.RiskGenerateCode
		}, wantReason: ReasonAirlockRiskMismatch},
		{name: "airlock blocked", mutate: func(_ *Policy, _ *Request, airlock *mcpairlock.ToolDecision) {
			airlock.Status = "blocked"
			airlock.Allowed = false
		}, wantReason: ReasonAirlockBlocked},
		{name: "blocked trusted output", mutate: func(_ *Policy, _ *Request, airlock *mcpairlock.ToolDecision) {
			airlock.Status = "blocked"
			airlock.Allowed = false
			airlock.UntrustedOutput = false
		}, wantReason: ReasonInvalidAirlockDecision},
		{name: "contradictory airlock flags", mutate: func(_ *Policy, _ *Request, airlock *mcpairlock.ToolDecision) {
			airlock.ApprovalRequired = true
		}, wantReason: ReasonInvalidAirlockDecision},
		{name: "trusted airlock output", mutate: func(_ *Policy, _ *Request, airlock *mcpairlock.ToolDecision) {
			airlock.UntrustedOutput = false
		}, wantReason: ReasonInvalidAirlockDecision},
		{name: "unknown airlock status", mutate: func(_ *Policy, _ *Request, airlock *mcpairlock.ToolDecision) {
			airlock.Status = "unknown"
		}, wantReason: ReasonInvalidAirlockDecision},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, airlock := readOnlyEvaluation()
			test.mutate(&policy, &request, &airlock)
			decision := Evaluate(policy, request, airlock)
			if decision.Status != DecisionDenied || decision.Allowed || decision.ApprovalRequired || decision.Reason != test.wantReason || !decision.UntrustedOutput {
				t.Fatalf("Evaluate() = %+v, want denied reason %q", decision, test.wantReason)
			}
		})
	}
}

func TestPolicyMatchRejectsEntireInvalidPolicy(t *testing.T) {
	request := validRequest()
	bad := validRule()
	bad.ConnectionID = ""
	policy := Policy{Rules: []Rule{validRule(), bad}}
	if matched, ok := policy.Match(request); ok {
		t.Fatalf("Match() = %+v, true; want invalid policy to fail closed", matched)
	}
}
