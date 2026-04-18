package engines

import (
	"context"

	"github.com/iac-studio/iac-studio/internal/policy"
)

// builtinEngine wraps the legacy policy.Engine (the 15+ hand-written Go
// guardrails) behind the multi-engine PolicyEngine interface so it can be run
// alongside OPA, Conftest, and Sentinel without callers caring which is which.
//
// The wrap is intentionally thin — every existing rule keeps working unchanged
// and shows up in the unified Findings stream as engine="builtin".
type builtinEngine struct {
	inner *policy.Engine
}

// NewBuiltin constructs a PolicyEngine backed by the default built-in rules.
// Pass a *policy.Engine constructed via policy.NewWithRules to use a custom
// rule set instead of the defaults.
func NewBuiltin() PolicyEngine {
	return &builtinEngine{inner: policy.New()}
}

// NewBuiltinWith wraps an existing policy.Engine — useful when callers need
// to enable/disable specific rules before evaluation.
func NewBuiltinWith(e *policy.Engine) PolicyEngine {
	return &builtinEngine{inner: e}
}

func (b *builtinEngine) Name() string   { return "builtin" }
func (b *builtinEngine) Available() bool { return true }

func (b *builtinEngine) Evaluate(_ context.Context, in EvalInput) (Result, error) {
	res := Result{Engine: b.Name(), Available: true}
	if len(in.Resources) == 0 {
		// Nothing to evaluate is not an error — keeps the multi-engine flow
		// quiet on an empty project.
		return res, nil
	}
	report := b.inner.Evaluate(in.Resources)
	for _, v := range report.Violations {
		res.Findings = append(res.Findings, Finding{
			Engine:     b.Name(),
			PolicyID:   v.RuleID,
			PolicyName: v.RuleName,
			Severity:   Severity(v.Severity),
			Category:   v.Category,
			Resource:   v.Resource,
			Message:    v.Message,
			Suggestion: v.Suggestion,
		})
	}
	return res, nil
}
