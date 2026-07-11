package agentrouting

import (
	"errors"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

func validRequest() Request {
	return Request{
		Project:      "platform-prod",
		ProviderID:   "aws-bedrock",
		ConnectionID: "aws-prod",
		ServerID:     "terraform-official",
		ToolName:     "plan_workspace",
		Mode:         agentruns.ModeProposeOnly,
		Risk:         mcpairlock.RiskGenerateCode,
	}
}

func validRule() Rule {
	request := validRequest()
	return Rule{
		Project:          request.Project,
		ProviderID:       request.ProviderID,
		ConnectionID:     request.ConnectionID,
		ServerID:         request.ServerID,
		ToolName:         request.ToolName,
		Modes:            []agentruns.Mode{request.Mode},
		Risk:             request.Risk,
		Effect:           EffectAllow,
		ApprovalRequired: true,
	}
}

func TestRequestValidationRequiresCompleteScope(t *testing.T) {
	base := validRequest()
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "project", mutate: func(request *Request) { request.Project = "" }},
		{name: "provider", mutate: func(request *Request) { request.ProviderID = "" }},
		{name: "connection", mutate: func(request *Request) { request.ConnectionID = "" }},
		{name: "server", mutate: func(request *Request) { request.ServerID = "" }},
		{name: "tool", mutate: func(request *Request) { request.ToolName = "" }},
		{name: "padded server", mutate: func(request *Request) { request.ServerID = " terraform-official" }},
		{name: "padded tool", mutate: func(request *Request) { request.ToolName = "plan_workspace\t" }},
		{name: "mode", mutate: func(request *Request) { request.Mode = "execute" }},
		{name: "risk", mutate: func(request *Request) { request.Risk = "unclassified" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.mutate(&request)
			if err := request.Validate(); err == nil || !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Validate() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestRuleMatchesRejectsInvalidInputs(t *testing.T) {
	t.Run("invalid request", func(t *testing.T) {
		request := validRequest()
		request.ServerID = " terraform-official"
		if validRule().Matches(request) {
			t.Fatal("rule should not match an invalid request")
		}
	})

	t.Run("invalid rule", func(t *testing.T) {
		rule := validRule()
		rule.ConnectionID = ""
		if rule.Matches(validRequest()) {
			t.Fatal("invalid rule should not match a request")
		}
	})
}

func TestRuleMatchesEverySecurityDimension(t *testing.T) {
	rule := validRule()
	if err := rule.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
	request := validRequest()
	if !rule.Matches(request) {
		t.Fatal("valid request should match rule")
	}

	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "project", mutate: func(request *Request) { request.Project = "platform-dev" }},
		{name: "provider", mutate: func(request *Request) { request.ProviderID = "openai-api" }},
		{name: "connection", mutate: func(request *Request) { request.ConnectionID = "aws-dev" }},
		{name: "server", mutate: func(request *Request) { request.ServerID = "aws-readonly" }},
		{name: "tool", mutate: func(request *Request) { request.ToolName = "apply_workspace" }},
		{name: "mode", mutate: func(request *Request) { request.Mode = agentruns.ModeApprovedExecute }},
		{name: "risk", mutate: func(request *Request) { request.Risk = mcpairlock.RiskCloudMutation }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := request
			test.mutate(&candidate)
			if rule.Matches(candidate) {
				t.Fatal("route with changed security dimension should not match")
			}
		})
	}
}

func TestPolicyDefaultsToDenyAndMatchesExplicitRules(t *testing.T) {
	request := validRequest()
	policy := Policy{Rules: []Rule{validRule()}}

	if matched, ok := policy.Match(request); !ok || matched.Effect != EffectAllow || !matched.ApprovalRequired {
		t.Fatalf("Match() = %#v, %v; want approved allow rule", matched, ok)
	}
	request.ToolName = "apply_workspace"
	if _, ok := policy.Match(request); ok {
		t.Fatal("unmatched route should fail closed")
	}
}

func TestRuleRejectsApprovalOnDeny(t *testing.T) {
	rule := validRule()
	rule.Effect = EffectDeny
	if err := rule.Validate(); err == nil || !errors.Is(err, ErrInvalidRule) {
		t.Fatalf("Validate() error = %v, want ErrInvalidRule", err)
	}
}

func TestPolicyValidateSurfacesMisconfiguredRules(t *testing.T) {
	good := validRule()

	badRule := validRule()
	badRule.ConnectionID = ""
	paddedRule := validRule()
	paddedRule.ServerID = "terraform-official "

	tests := []struct {
		name    string
		policy  Policy
		wantErr bool
	}{
		{name: "valid policy", policy: Policy{Rules: []Rule{good}}, wantErr: false},
		{name: "empty policy", policy: Policy{}, wantErr: false},
		{name: "invalid rule at index 0", policy: Policy{Rules: []Rule{badRule}}, wantErr: true},
		{name: "invalid rule after valid rule", policy: Policy{Rules: []Rule{good, badRule}}, wantErr: true},
		{name: "whitespace-padded rule", policy: Policy{Rules: []Rule{paddedRule}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.policy.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Policy.Validate() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.wantErr && !errors.Is(err, ErrInvalidRule) {
				t.Fatalf("Policy.Validate() error = %v, want ErrInvalidRule", err)
			}
		})
	}
}

func TestRuleRejectsUnsafeAllowDefaults(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Rule)
	}{
		{name: "unknown risk", mutate: func(rule *Rule) { rule.Risk = mcpairlock.RiskUnknown }},
		{name: "mutation without approval", mutate: func(rule *Rule) {
			rule.Risk = mcpairlock.RiskCloudMutation
			rule.ApprovalRequired = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := validRule()
			test.mutate(&rule)
			if err := rule.Validate(); err == nil || !errors.Is(err, ErrInvalidRule) {
				t.Fatalf("Validate() error = %v, want ErrInvalidRule", err)
			}
		})
	}
}
