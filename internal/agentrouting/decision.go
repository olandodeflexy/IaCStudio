package agentrouting

import (
	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

// DecisionStatus is the authorization outcome for one Agent Hub tool route.
type DecisionStatus string

const (
	DecisionDenied           DecisionStatus = "denied"
	DecisionApprovalRequired DecisionStatus = "approval_required"
	DecisionAllowed          DecisionStatus = "allowed"
)

// DecisionReason is a stable machine-readable explanation for an outcome.
type DecisionReason string

const (
	ReasonInvalidRequest         DecisionReason = "invalid_request"
	ReasonInvalidPolicy          DecisionReason = "invalid_policy"
	ReasonModeRiskMismatch       DecisionReason = "mode_risk_mismatch"
	ReasonNoMatchingRule         DecisionReason = "no_matching_rule"
	ReasonPolicyDenied           DecisionReason = "policy_denied"
	ReasonAirlockUnavailable     DecisionReason = "airlock_unavailable"
	ReasonAirlockToolMismatch    DecisionReason = "airlock_tool_mismatch"
	ReasonAirlockRiskMismatch    DecisionReason = "airlock_risk_mismatch"
	ReasonInvalidAirlockDecision DecisionReason = "invalid_airlock_decision"
	ReasonAirlockBlocked         DecisionReason = "airlock_blocked"
	ReasonApprovalRequired       DecisionReason = "approval_required"
	ReasonAllowed                DecisionReason = "allowed"
)

// Decision combines the scoped route policy with MCP Airlock's firewall
// result. External MCP output remains untrusted for every outcome.
type Decision struct {
	Status           DecisionStatus `json:"status"`
	Reason           DecisionReason `json:"reason"`
	Allowed          bool           `json:"allowed"`
	ApprovalRequired bool           `json:"approval_required"`
	UntrustedOutput  bool           `json:"untrusted_output"`
}

// Evaluate intersects the route policy, agent mode, and Airlock firewall
// result. Missing, malformed, or contradictory inputs always fail closed.
func Evaluate(policy Policy, request Request, airlock mcpairlock.ToolDecision) Decision {
	if decision, stop := preflight(request); stop {
		return decision
	}
	if policy.Validate() != nil {
		return denied(ReasonInvalidPolicy)
	}
	return evaluateValidated(policy, request, airlock)
}

func evaluateValidated(policy Policy, request Request, airlock mcpairlock.ToolDecision) Decision {
	rule, matched := policy.matchValidated(request)
	if !matched {
		return denied(ReasonNoMatchingRule)
	}
	return evaluateMatched(rule, request, airlock)
}

func evaluateMatched(rule Rule, request Request, airlock mcpairlock.ToolDecision) Decision {
	if rule.Effect == EffectDeny {
		return denied(ReasonPolicyDenied)
	}
	if airlock.Risk != request.Risk {
		return denied(ReasonAirlockRiskMismatch)
	}

	switch {
	case airlock.Status == "blocked" && !airlock.Allowed && !airlock.ApprovalRequired && airlock.UntrustedOutput:
		return denied(ReasonAirlockBlocked)
	case airlock.Status == "approval_required" && !airlock.Allowed && airlock.ApprovalRequired && airlock.UntrustedOutput:
		return approvalRequired()
	case airlock.Status == "allowed" && airlock.Allowed && !airlock.ApprovalRequired && airlock.UntrustedOutput:
		if rule.ApprovalRequired {
			return approvalRequired()
		}
		return allowed()
	default:
		return denied(ReasonInvalidAirlockDecision)
	}
}

func preflight(request Request) (Decision, bool) {
	if request.Validate() != nil {
		return denied(ReasonInvalidRequest), true
	}
	if !modeAllowsRisk(request.Mode, request.Risk) {
		return denied(ReasonModeRiskMismatch), true
	}
	return Decision{}, false
}

func modeAllowsRisk(mode agentruns.Mode, risk mcpairlock.ToolRisk) bool {
	switch mode {
	case agentruns.ModeReadOnly:
		return risk == mcpairlock.RiskReadOnly
	case agentruns.ModeProposeOnly:
		return risk == mcpairlock.RiskReadOnly || risk == mcpairlock.RiskGenerateCode
	case agentruns.ModeApprovedExecute:
		return risk != mcpairlock.RiskUnknown
	default:
		return false
	}
}

func denied(reason DecisionReason) Decision {
	return Decision{Status: DecisionDenied, Reason: reason, UntrustedOutput: true}
}

func approvalRequired() Decision {
	return Decision{
		Status:           DecisionApprovalRequired,
		Reason:           ReasonApprovalRequired,
		ApprovalRequired: true,
		UntrustedOutput:  true,
	}
}

func allowed() Decision {
	return Decision{
		Status:          DecisionAllowed,
		Reason:          ReasonAllowed,
		Allowed:         true,
		UntrustedOutput: true,
	}
}
