package plan

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Parser converts raw `terraform plan -json` output into structured diffs
// that the frontend can render as a visual change review.
type Parser struct{}

func New() *Parser {
	return &Parser{}
}

// PlanResult is the structured output of parsing a terraform plan.
type PlanResult struct {
	Changes      []ResourceChange `json:"changes"`
	Summary      PlanSummary      `json:"summary"`
	Warnings     []string         `json:"warnings,omitempty"`
	Errors       []string         `json:"errors,omitempty"`
	HasChanges   bool             `json:"has_changes"`
	RequiresApproval bool         `json:"requires_approval"` // true if any destroy
}

// PlanSummary counts by action type.
type PlanSummary struct {
	Add       int `json:"add"`
	Change    int `json:"change"`
	Destroy   int `json:"destroy"`
	Import    int `json:"import"`
	NoOp      int `json:"no_op"`
	Total     int `json:"total"`
}

func (s PlanSummary) String() string {
	return fmt.Sprintf("%d to add, %d to change, %d to destroy", s.Add, s.Change, s.Destroy)
}

// ResourceChange represents one resource's planned changes.
type ResourceChange struct {
	Address      string            `json:"address"`       // e.g., aws_vpc.main
	Type         string            `json:"type"`          // e.g., aws_vpc
	Name         string            `json:"name"`          // e.g., main
	Provider     string            `json:"provider"`
	Action       string            `json:"action"`        // create | update | delete | replace | read | no-op
	ActionReason string            `json:"action_reason,omitempty"`
	Before       map[string]interface{} `json:"before,omitempty"`  // current state
	After        map[string]interface{} `json:"after,omitempty"`   // planned state
	FieldChanges []FieldChange     `json:"field_changes,omitempty"` // per-attribute diffs
}

// FieldChange is a single attribute-level diff.
type FieldChange struct {
	Path      string      `json:"path"`       // e.g., "tags.Name"
	OldValue  interface{} `json:"old_value"`
	NewValue  interface{} `json:"new_value"`
	Sensitive bool        `json:"sensitive"`   // true if value is masked
	Computed  bool        `json:"computed"`    // true if known only after apply
}

// --- Terraform JSON plan format structures ---

type tfPlanLine struct {
	Level      string          `json:"@level"`
	Message    string          `json:"@message"`
	Type       string          `json:"type"`
	Change     *tfChange       `json:"change,omitempty"`
	Diagnostic *tfDiagnostic   `json:"diagnostic,omitempty"`
}

type tfChange struct {
	Resource tfResource     `json:"resource"`
	Action   string         `json:"action"`  // "create", "update", "delete", "replace", "noop", "read"
	Reason   string         `json:"reason,omitempty"`
}

type tfResource struct {
	Addr     string `json:"addr"`
	Type     string `json:"resource_type"`
	Name     string `json:"resource_name"`
	Provider string `json:"provider"`
}

type tfDiagnostic struct {
	Severity string `json:"severity"` // "warning" or "error"
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
}

// For full plan JSON output (terraform show -json planfile)
type tfFullPlan struct {
	ResourceChanges []tfFullResourceChange `json:"resource_changes"`
}

type tfFullResourceChange struct {
	Address  string   `json:"address"`
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Provider string   `json:"provider_name"`
	Change   tfFullChange `json:"change"`
}

type tfFullChange struct {
	Actions []string               `json:"actions"` // ["create"], ["update"], ["delete"], ["create","delete"] for replace
	Before  map[string]interface{} `json:"before"`
	After   map[string]interface{} `json:"after"`
}

// ParseStreamOutput parses the line-by-line JSON from `terraform plan -json`.
func (p *Parser) ParseStreamOutput(jsonLines string) (*PlanResult, error) {
	result := &PlanResult{}

	for _, line := range strings.Split(strings.TrimSpace(jsonLines), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry tfPlanLine
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip non-JSON lines (terraform sometimes outputs plain text)
		}

		switch {
		case entry.Type == "planned_change" && entry.Change != nil:
			rc := ResourceChange{
				Address:      entry.Change.Resource.Addr,
				Type:         entry.Change.Resource.Type,
				Name:         entry.Change.Resource.Name,
				Provider:     entry.Change.Resource.Provider,
				Action:       normalizeAction(entry.Change.Action),
				ActionReason: entry.Change.Reason,
			}
			result.Changes = append(result.Changes, rc)

		case entry.Diagnostic != nil:
			msg := entry.Diagnostic.Summary
			if entry.Diagnostic.Detail != "" {
				msg += ": " + entry.Diagnostic.Detail
			}
			if entry.Diagnostic.Severity == "error" {
				result.Errors = append(result.Errors, msg)
			} else {
				result.Warnings = append(result.Warnings, msg)
			}
		}
	}

	// Build summary
	for _, c := range result.Changes {
		switch c.Action {
		case "create":
			result.Summary.Add++
		case "update":
			result.Summary.Change++
		case "delete":
			result.Summary.Destroy++
			result.RequiresApproval = true
		case "replace":
			result.Summary.Destroy++
			result.Summary.Add++
			result.RequiresApproval = true
		case "read":
			// data source refresh, no action
		default:
			result.Summary.NoOp++
		}
	}
	result.Summary.Total = len(result.Changes)
	result.HasChanges = result.Summary.Add > 0 || result.Summary.Change > 0 || result.Summary.Destroy > 0

	return result, nil
}

// ParseFullPlan parses the output of `terraform show -json planfile` which includes
// before/after state for detailed field-level diffs.
func (p *Parser) ParseFullPlan(jsonOutput string) (*PlanResult, error) {
	var fullPlan tfFullPlan
	if err := json.Unmarshal([]byte(jsonOutput), &fullPlan); err != nil {
		return nil, fmt.Errorf("parsing plan JSON: %w", err)
	}

	result := &PlanResult{}

	for _, rc := range fullPlan.ResourceChanges {
		action := actionsToAction(rc.Change.Actions)
		change := ResourceChange{
			Address:  rc.Address,
			Type:     rc.Type,
			Name:     rc.Name,
			Provider: rc.Provider,
			Action:   action,
			Before:   rc.Change.Before,
			After:    rc.Change.After,
		}

		// Compute field-level diffs
		if rc.Change.Before != nil && rc.Change.After != nil {
			change.FieldChanges = diffMaps("", rc.Change.Before, rc.Change.After)
		} else if rc.Change.After != nil {
			// New resource — all fields are additions
			for k, v := range rc.Change.After {
				change.FieldChanges = append(change.FieldChanges, FieldChange{
					Path:     k,
					NewValue: v,
				})
			}
		}

		result.Changes = append(result.Changes, change)

		switch action {
		case "create":
			result.Summary.Add++
		case "update":
			result.Summary.Change++
		case "delete":
			result.Summary.Destroy++
			result.RequiresApproval = true
		case "replace":
			result.Summary.Destroy++
			result.Summary.Add++
			result.RequiresApproval = true
		}
	}

	result.Summary.Total = len(result.Changes)
	result.HasChanges = result.Summary.Add > 0 || result.Summary.Change > 0 || result.Summary.Destroy > 0

	return result, nil
}

// diffMaps computes field-level differences between two attribute maps.
func diffMaps(prefix string, before, after map[string]interface{}) []FieldChange {
	var changes []FieldChange
	seen := make(map[string]bool)

	for k, oldVal := range before {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		seen[k] = true

		newVal, exists := after[k]
		if !exists {
			changes = append(changes, FieldChange{Path: path, OldValue: oldVal})
			continue
		}

		if fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			changes = append(changes, FieldChange{Path: path, OldValue: oldVal, NewValue: newVal})
		}
	}

	for k, newVal := range after {
		if seen[k] {
			continue
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		changes = append(changes, FieldChange{Path: path, NewValue: newVal})
	}

	return changes
}

func normalizeAction(action string) string {
	switch action {
	case "create":
		return "create"
	case "update":
		return "update"
	case "delete":
		return "delete"
	case "replace":
		return "replace"
	case "read":
		return "read"
	case "noop", "no-op":
		return "no-op"
	default:
		return action
	}
}

func actionsToAction(actions []string) string {
	if len(actions) == 1 {
		return normalizeAction(actions[0])
	}
	// ["create", "delete"] or ["delete", "create"] = replace
	if len(actions) == 2 {
		return "replace"
	}
	return "unknown"
}
