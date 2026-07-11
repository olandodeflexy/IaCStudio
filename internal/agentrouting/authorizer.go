package agentrouting

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

var ErrToolEvaluatorRequired = errors.New("tool evaluator is required")

// ToolEvaluator is the read-only MCP Airlock boundary used to retrieve one
// discovered tool's current firewall decision.
type ToolEvaluator interface {
	EvaluateTool(serverID, project, toolName string) (mcpairlock.ToolInventoryEntry, error)
}

// Authorizer combines an immutable routing policy snapshot with live MCP
// Airlock inventory decisions. It never invokes an external tool.
type Authorizer struct {
	policy    Policy
	evaluator ToolEvaluator
}

// NewAuthorizer validates and snapshots the routing policy before it can be
// used. Later caller mutations cannot widen the authorizer's permissions.
func NewAuthorizer(policy Policy, evaluator ToolEvaluator) (*Authorizer, error) {
	if missingToolEvaluator(evaluator) {
		return nil, ErrToolEvaluatorRequired
	}
	snapshot := clonePolicy(policy)
	if err := snapshot.Validate(); err != nil {
		return nil, fmt.Errorf("validate routing policy: %w", err)
	}
	return &Authorizer{policy: snapshot, evaluator: evaluator}, nil
}

// Authorize evaluates the current Airlock decision for one fully scoped tool
// route. Airlock errors and inconsistent inventory identities fail closed.
func (a *Authorizer) Authorize(request Request) Decision {
	if decision, stop := preflight(request); stop {
		return decision
	}
	if a == nil || missingToolEvaluator(a.evaluator) {
		return denied(ReasonAirlockUnavailable)
	}
	if a.policy.Validate() != nil {
		return denied(ReasonInvalidPolicy)
	}
	rule, matched := a.policy.matchValidated(request)
	if !matched {
		return denied(ReasonNoMatchingRule)
	}
	if rule.Effect == EffectDeny {
		return denied(ReasonPolicyDenied)
	}

	entry, err := a.evaluator.EvaluateTool(request.ServerID, request.Project, request.ToolName)
	if err != nil {
		return denied(ReasonAirlockUnavailable)
	}
	if entry.ServerID != request.ServerID ||
		entry.Name != request.ToolName ||
		entry.Risk != request.Risk ||
		entry.Risk != entry.Decision.Risk {
		return denied(ReasonAirlockToolMismatch)
	}
	return evaluateMatched(rule, request, entry.Decision)
}

func missingToolEvaluator(evaluator ToolEvaluator) bool {
	if evaluator == nil {
		return true
	}
	value := reflect.ValueOf(evaluator)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func clonePolicy(policy Policy) Policy {
	rules := make([]Rule, len(policy.Rules))
	copy(rules, policy.Rules)
	for i := range rules {
		rules[i].Modes = append([]agentruns.Mode(nil), rules[i].Modes...)
	}
	return Policy{Rules: rules}
}
