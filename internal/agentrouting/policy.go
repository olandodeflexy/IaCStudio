// Package agentrouting defines the scoped policy contract used before an
// Agent Hub run can call an external infrastructure tool.
package agentrouting

import (
	"errors"
	"fmt"
	"strings"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

var (
	ErrInvalidRequest = errors.New("invalid tool route request")
	ErrInvalidRule    = errors.New("invalid tool route rule")
)

type fieldValue struct {
	name  string
	value string
}

// Effect is the outcome a matching policy rule assigns to a tool route.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Request identifies one attempted Agent Hub to MCP tool route. Every scope
// field is required so later enforcement cannot accidentally fall back to a
// provider-wide or project-wide credential choice.
type Request struct {
	Project      string              `json:"project"`
	ProviderID   string              `json:"provider_id"`
	ConnectionID string              `json:"connection_id"`
	ServerID     string              `json:"server_id"`
	ToolName     string              `json:"tool_name"`
	Mode         agentruns.Mode      `json:"mode"`
	Risk         mcpairlock.ToolRisk `json:"risk"`
}

// Rule matches one fully scoped route. Modes are explicit rather than
// inferred from risk so a read-only request cannot silently become a write.
type Rule struct {
	Project          string              `json:"project"`
	ProviderID       string              `json:"provider_id"`
	ConnectionID     string              `json:"connection_id"`
	ServerID         string              `json:"server_id"`
	ToolName         string              `json:"tool_name"`
	Modes            []agentruns.Mode    `json:"modes"`
	Risk             mcpairlock.ToolRisk `json:"risk"`
	Effect           Effect              `json:"effect"`
	ApprovalRequired bool                `json:"approval_required,omitempty"`
}

// Policy is intentionally only a contract in this first slice. An empty
// policy has no matches and therefore remains fail-closed until a later
// enforcement layer is wired to it.
type Policy struct {
	Rules []Rule `json:"rules"`
}

func (r Request) Validate() error {
	if err := validateRequiredFields(ErrInvalidRequest,
		fieldValue{name: "project", value: r.Project},
		fieldValue{name: "provider_id", value: r.ProviderID},
		fieldValue{name: "connection_id", value: r.ConnectionID},
		fieldValue{name: "server_id", value: r.ServerID},
		fieldValue{name: "tool_name", value: r.ToolName},
	); err != nil {
		return err
	}
	if !r.Mode.Valid() {
		return fmt.Errorf("%w: unsupported mode %q", ErrInvalidRequest, r.Mode)
	}
	if !validRisk(r.Risk) {
		return fmt.Errorf("%w: unsupported risk %q", ErrInvalidRequest, r.Risk)
	}
	return nil
}

func (r Rule) Validate() error {
	if err := validateRequiredFields(ErrInvalidRule,
		fieldValue{name: "project", value: r.Project},
		fieldValue{name: "provider_id", value: r.ProviderID},
		fieldValue{name: "connection_id", value: r.ConnectionID},
		fieldValue{name: "server_id", value: r.ServerID},
		fieldValue{name: "tool_name", value: r.ToolName},
	); err != nil {
		return err
	}
	if len(r.Modes) == 0 {
		return fmt.Errorf("%w: at least one mode is required", ErrInvalidRule)
	}
	for _, mode := range r.Modes {
		if !mode.Valid() {
			return fmt.Errorf("%w: unsupported mode %q", ErrInvalidRule, mode)
		}
	}
	if !validRisk(r.Risk) {
		return fmt.Errorf("%w: unsupported risk %q", ErrInvalidRule, r.Risk)
	}
	if r.Effect != EffectAllow && r.Effect != EffectDeny {
		return fmt.Errorf("%w: unsupported effect %q", ErrInvalidRule, r.Effect)
	}
	if r.Effect == EffectDeny && r.ApprovalRequired {
		return fmt.Errorf("%w: deny rules cannot require approval", ErrInvalidRule)
	}
	if r.Effect == EffectAllow && r.Risk == mcpairlock.RiskUnknown {
		return fmt.Errorf("%w: unknown risk cannot be allowed", ErrInvalidRule)
	}
	if r.Effect == EffectAllow && r.Risk != mcpairlock.RiskReadOnly && !r.ApprovalRequired {
		return fmt.Errorf("%w: non-read-only allow rules require approval", ErrInvalidRule)
	}
	return nil
}

// Matches reports whether a route request is covered by the rule. Invalid
// rules and requests fail closed. Matching is exact across every scope field,
// risk, and action mode.
func (r Rule) Matches(request Request) bool {
	if r.Validate() != nil || request.Validate() != nil {
		return false
	}
	return r.matchesValidated(request)
}

func (r Rule) matchesValidated(request Request) bool {
	if r.Project != request.Project ||
		r.ProviderID != request.ProviderID ||
		r.ConnectionID != request.ConnectionID ||
		r.ServerID != request.ServerID ||
		r.ToolName != request.ToolName ||
		r.Risk != request.Risk {
		return false
	}
	for _, mode := range r.Modes {
		if mode == request.Mode {
			return true
		}
	}
	return false
}

// Validate checks every rule in the policy and returns the first error found.
// Callers should call Validate when loading a policy so that misconfigured
// rules (e.g. empty required fields after a config write bug) are reported at
// load time rather than silently skipped during Match.
func (p Policy) Validate() error {
	for i, rule := range p.Rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("rule[%d]: %w", i, err)
		}
	}
	return nil
}

// Match returns the first matching rule. Callers must treat a false result as
// deny; this package does not provide an implicit allow decision.
func (p Policy) Match(request Request) (Rule, bool) {
	if request.Validate() != nil || p.Validate() != nil {
		return Rule{}, false
	}
	return p.matchValidated(request)
}

func (p Policy) matchValidated(request Request) (Rule, bool) {
	for _, rule := range p.Rules {
		if rule.matchesValidated(request) {
			return rule, true
		}
	}
	return Rule{}, false
}

func validateRequiredFields(root error, fields ...fieldValue) error {
	for _, field := range fields {
		trimmed := strings.TrimSpace(field.value)
		if trimmed == "" {
			return fmt.Errorf("%w: %s is required", root, field.name)
		}
		if trimmed != field.value {
			return fmt.Errorf("%w: %s must not contain leading or trailing whitespace", root, field.name)
		}
	}
	return nil
}

func validRisk(risk mcpairlock.ToolRisk) bool {
	switch risk {
	case mcpairlock.RiskReadOnly,
		mcpairlock.RiskGenerateCode,
		mcpairlock.RiskModifyWorkspace,
		mcpairlock.RiskCloudMutation,
		mcpairlock.RiskSecretSensitive,
		mcpairlock.RiskDestructive,
		mcpairlock.RiskUnknown:
		return true
	default:
		return false
	}
}
