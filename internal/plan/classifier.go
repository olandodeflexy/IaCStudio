package plan

import (
	"fmt"
	"sort"
	"strings"
)

// RiskLevel is the apply-gate severity assigned to a planned change.
type RiskLevel string

const (
	RiskSafe        RiskLevel = "safe"
	RiskRisky       RiskLevel = "risky"
	RiskDestructive RiskLevel = "destructive"
	RiskUnknown     RiskLevel = "unknown"
)

// ClassifiedChange is the reviewer-facing interpretation of one planned
// resource change.
type ClassifiedChange struct {
	Address       string        `json:"address"`
	Type          string        `json:"type"`
	Name          string        `json:"name"`
	Provider      string        `json:"provider,omitempty"`
	Action        string        `json:"action"`
	Risk          RiskLevel     `json:"risk"`
	Categories    []string      `json:"categories"`
	Reason        string        `json:"reason"`
	ReviewerFocus []string      `json:"reviewer_focus"`
	FieldChanges  []FieldChange `json:"field_changes,omitempty"`
}

// ClassificationSummary is the compact result used by the UI and apply gate.
type ClassificationSummary struct {
	Safe                   int    `json:"safe"`
	Risky                  int    `json:"risky"`
	Destructive            int    `json:"destructive"`
	Unknown                int    `json:"unknown"`
	Total                  int    `json:"total"`
	RequiresAcknowledgment bool   `json:"requires_acknowledgment"`
	Text                   string `json:"text"`
}

// ClassificationResult is the full semantic plan classifier response.
type ClassificationResult struct {
	Summary  ClassificationSummary `json:"summary"`
	Changes  []ClassifiedChange    `json:"changes"`
	Markdown string                `json:"markdown,omitempty"`
}

// ClassifyFullPlan parses `terraform show -json tfplan` output and classifies it.
func (p *Parser) ClassifyFullPlan(jsonOutput string) (*ClassificationResult, error) {
	parsed, err := p.ParseFullPlan(jsonOutput)
	if err != nil {
		return nil, err
	}
	return p.Classify(parsed), nil
}

// Classify translates parsed plan changes into a reviewer-facing risk summary.
func (p *Parser) Classify(result *PlanResult) *ClassificationResult {
	if result == nil {
		return UnknownClassification("no plan result was available")
	}
	classified := make([]ClassifiedChange, 0, len(result.Changes))
	for _, change := range result.Changes {
		classified = append(classified, classifyChange(change))
	}

	summary := ClassificationSummary{Total: len(classified)}
	for _, change := range classified {
		switch change.Risk {
		case RiskSafe:
			summary.Safe++
		case RiskRisky:
			summary.Risky++
		case RiskDestructive:
			summary.Destructive++
		default:
			summary.Unknown++
		}
	}
	summary.RequiresAcknowledgment = summary.Risky > 0 || summary.Destructive > 0 || summary.Unknown > 0
	summary.Text = formatSummaryText(summary)

	res := &ClassificationResult{
		Summary: summary,
		Changes: classified,
	}
	res.Markdown = formatClassificationMarkdown(res)
	return res
}

// UnknownClassification returns a blocking classifier result for cases where
// the plan cannot be inspected. The apply gate uses this fail-closed path.
func UnknownClassification(reason string) *ClassificationResult {
	if strings.TrimSpace(reason) == "" {
		reason = "plan could not be classified"
	}
	result := &ClassificationResult{
		Summary: ClassificationSummary{
			Unknown:                1,
			Total:                  1,
			RequiresAcknowledgment: true,
			Text:                   "Semantic plan: 1 unknown change",
		},
		Changes: []ClassifiedChange{{
			Address:       "(plan)",
			Action:        "unknown",
			Risk:          RiskUnknown,
			Categories:    []string{"unknown"},
			Reason:        reason,
			ReviewerFocus: []string{"Inspect the saved plan output before applying."},
		}},
	}
	result.Markdown = formatClassificationMarkdown(result)
	return result
}

func classifyChange(change ResourceChange) ClassifiedChange {
	out := ClassifiedChange{
		Address:      change.Address,
		Type:         change.Type,
		Name:         change.Name,
		Provider:     change.Provider,
		Action:       change.Action,
		FieldChanges: change.FieldChanges,
	}

	switch change.Action {
	case "no-op", "read":
		out.Risk = RiskSafe
		out.Categories = []string{"safe_noise"}
		out.Reason = "No infrastructure mutation is planned."
		out.ReviewerFocus = []string{"Confirm the plan contains no create, update, replace, or delete action for this resource."}
		return out
	case "delete", "replace":
		out.Risk = RiskDestructive
		out.Categories = []string{"destructive"}
		if isStatefulResource(change.Type) {
			out.Categories = append(out.Categories, "data")
			out.Reason = fmt.Sprintf("%s %s can remove or recreate stateful data.", actionLabel(change.Action), change.Type)
			out.ReviewerFocus = []string{"Verify backups, snapshots, retention policy, and restore steps before approving."}
		} else {
			out.Reason = fmt.Sprintf("%s %s can remove existing infrastructure.", actionLabel(change.Action), change.Type)
			out.ReviewerFocus = []string{"Confirm the resource is intentionally removed and downstream dependencies are understood."}
		}
		return out
	case "update":
		if isMetadataOnlyUpdate(change.FieldChanges) {
			out.Risk = RiskSafe
			out.Categories = []string{"safe_noise"}
			out.Reason = "Only metadata fields such as tags or description changed."
			out.ReviewerFocus = []string{"Confirm tag and description values match the owning team's conventions."}
			return out
		}
	case "create":
		// Fall through to resource-sensitive rules below. A generic create
		// remains unknown because cost, IAM, and network blast radius depend
		// on provider-specific defaults.
	default:
		out.Risk = RiskUnknown
		out.Categories = []string{"unknown"}
		out.Reason = fmt.Sprintf("Action %q is not recognized by the semantic classifier.", change.Action)
		out.ReviewerFocus = []string{"Inspect the raw plan and provider documentation before approving."}
		return out
	}

	switch {
	case isIAMResource(change.Type):
		out.Risk = RiskRisky
		out.Categories = []string{"identity"}
		out.Reason = "IAM changes can expand permissions or trust relationships."
		out.ReviewerFocus = []string{"Review newly granted actions, principals, conditions, and assume-role paths."}
	case isNetworkExposureResource(change.Type) || containsPublicCIDR(change.After):
		out.Risk = RiskRisky
		out.Categories = []string{"network_exposure"}
		if containsPublicCIDR(change.After) {
			out.Reason = "The planned network configuration includes public CIDR exposure."
			out.ReviewerFocus = []string{"Check 0.0.0.0/0 or ::/0 rules, ports, protocols, and source restrictions."}
		} else {
			out.Reason = "Network boundary changes can alter service reachability."
			out.ReviewerFocus = []string{"Review ingress, egress, route, firewall, listener, and peering changes."}
		}
	case isStatefulResource(change.Type):
		out.Risk = RiskRisky
		out.Categories = []string{"data", "cost_sensitive"}
		out.Reason = "Stateful data resources can affect durability, availability, or spend."
		out.ReviewerFocus = []string{"Review storage class, deletion protection, encryption, retention, and size changes."}
	default:
		out.Risk = RiskUnknown
		out.Categories = []string{"unknown"}
		out.Reason = "This change does not match a known safe pattern."
		out.ReviewerFocus = []string{"Inspect provider defaults, cost impact, and dependency changes before applying."}
	}
	return out
}

func isMetadataOnlyUpdate(changes []FieldChange) bool {
	if len(changes) == 0 {
		return false
	}
	for _, change := range changes {
		root := strings.Split(change.Path, ".")[0]
		switch root {
		case "tags", "tags_all", "description", "labels", "annotations":
			continue
		default:
			return false
		}
	}
	return true
}

func isIAMResource(resourceType string) bool {
	t := strings.ToLower(resourceType)
	return strings.Contains(t, "_iam_") ||
		strings.Contains(t, "iam") ||
		strings.Contains(t, "role_assignment") ||
		strings.Contains(t, "role_binding") ||
		strings.Contains(t, "service_account")
}

func isNetworkExposureResource(resourceType string) bool {
	t := strings.ToLower(resourceType)
	needles := []string{
		"security_group",
		"firewall",
		"network_acl",
		"network_security_group",
		"route",
		"listener",
		"load_balancer",
		"target_group",
		"vpc_peering",
		"internet_gateway",
	}
	for _, needle := range needles {
		if strings.Contains(t, needle) {
			return true
		}
	}
	return false
}

func isStatefulResource(resourceType string) bool {
	t := strings.ToLower(resourceType)
	needles := []string{
		"db_instance",
		"db_cluster",
		"rds",
		"dynamodb",
		"s3_bucket",
		"storage_bucket",
		"sql_database",
		"postgresql",
		"mysql",
		"disk",
		"volume",
		"snapshot",
		"redis",
		"elasticache",
		"backup",
	}
	for _, needle := range needles {
		if strings.Contains(t, needle) {
			return true
		}
	}
	return false
}

func containsPublicCIDR(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v == "0.0.0.0/0" || v == "::/0"
	case []any:
		for _, item := range v {
			if containsPublicCIDR(item) {
				return true
			}
		}
	case map[string]interface{}:
		for _, item := range v {
			if containsPublicCIDR(item) {
				return true
			}
		}
	}
	return false
}

func actionLabel(action string) string {
	switch action {
	case "replace":
		return "Replacing"
	case "delete":
		return "Deleting"
	case "create":
		return "Creating"
	case "update":
		return "Updating"
	default:
		return "Changing"
	}
}

func formatSummaryText(summary ClassificationSummary) string {
	if summary.Total == 0 {
		return "Semantic plan: no infrastructure changes"
	}
	parts := []string{}
	if summary.Safe > 0 {
		parts = append(parts, fmt.Sprintf("%d safe", summary.Safe))
	}
	if summary.Risky > 0 {
		parts = append(parts, fmt.Sprintf("%d risky", summary.Risky))
	}
	if summary.Destructive > 0 {
		parts = append(parts, fmt.Sprintf("%d destructive", summary.Destructive))
	}
	if summary.Unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d unknown", summary.Unknown))
	}
	return "Semantic plan: " + strings.Join(parts, " · ")
}

func formatClassificationMarkdown(result *ClassificationResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(result.Summary.Text)
	if result.Summary.RequiresAcknowledgment {
		b.WriteString("\n\nAcknowledgement required before apply.")
	}
	if len(result.Changes) == 0 {
		return b.String()
	}

	b.WriteString("\n\n")
	changes := append([]ClassifiedChange(nil), result.Changes...)
	sort.SliceStable(changes, func(i, j int) bool {
		left, right := riskRank(changes[i].Risk), riskRank(changes[j].Risk)
		if left != right {
			return left > right
		}
		return changes[i].Address < changes[j].Address
	})
	limit := len(changes)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		change := changes[i]
		b.WriteString(fmt.Sprintf("- [%s] %s `%s`: %s", strings.ToUpper(string(change.Risk)), change.Action, change.Address, change.Reason))
		if len(change.ReviewerFocus) > 0 {
			b.WriteString(" Focus: ")
			b.WriteString(strings.Join(change.ReviewerFocus, " "))
		}
		b.WriteString("\n")
	}
	if len(changes) > limit {
		b.WriteString(fmt.Sprintf("- ...and %d more change(s)\n", len(changes)-limit))
	}
	return strings.TrimSpace(b.String())
}

func riskRank(risk RiskLevel) int {
	switch risk {
	case RiskDestructive:
		return 4
	case RiskRisky:
		return 3
	case RiskUnknown:
		return 2
	case RiskSafe:
		return 1
	default:
		return 0
	}
}
