package policy

import (
	"fmt"
	"net"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// Engine evaluates resources against a set of policy rules.
// Think of it as a lightweight OPA/Sentinel that runs locally before plan/apply.
// DevOps engineers define guardrails; the engine blocks non-compliant changes.
type Engine struct {
	rules []Rule
}

// Rule is a single policy check.
type Rule struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Severity    string   `json:"severity"`   // error | warning | info
	Category    string   `json:"category"`   // tagging | encryption | networking | access | naming
	Enabled     bool     `json:"enabled"`
	AppliesToTypes []string `json:"applies_to_types"` // empty = all resources
	check       func(parser.Resource) []Violation
}

// Violation is a policy rule failure.
type Violation struct {
	RuleID      string `json:"rule_id"`
	RuleName    string `json:"rule_name"`
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Resource    string `json:"resource"`     // e.g., "aws_s3_bucket.data"
	Message     string `json:"message"`
	Suggestion  string `json:"suggestion"`
	Field       string `json:"field,omitempty"` // which field caused it
}

// PolicyReport is the full evaluation result.
type PolicyReport struct {
	Violations   []Violation        `json:"violations"`
	ByCategory   map[string]int     `json:"by_category"`
	BySeverity   map[string]int     `json:"by_severity"`
	Passed       int                `json:"passed"`       // resources with zero violations
	Failed       int                `json:"failed"`       // resources with at least one error
	TotalChecks  int                `json:"total_checks"`
	Compliant    bool               `json:"compliant"`    // true if zero errors
}

// New creates a policy engine loaded with the default DevOps guardrails.
func New() *Engine {
	e := &Engine{}
	e.loadDefaultRules()
	return e
}

// NewWithRules creates a policy engine with custom rules only.
func NewWithRules(rules []Rule) *Engine {
	return &Engine{rules: rules}
}

// Evaluate checks all resources against all enabled rules.
func (e *Engine) Evaluate(resources []parser.Resource) *PolicyReport {
	report := &PolicyReport{
		ByCategory: make(map[string]int),
		BySeverity: make(map[string]int),
	}

	failedResources := make(map[string]bool)

	for _, resource := range resources {
		addr := resource.Type + "." + resource.Name
		resourceFailed := false

		for _, rule := range e.rules {
			if !rule.Enabled {
				continue
			}

			// Check if rule applies to this resource type
			if len(rule.AppliesToTypes) > 0 && !containsType(rule.AppliesToTypes, resource.Type) {
				continue
			}

			report.TotalChecks++
			violations := rule.check(resource)

			for _, v := range violations {
				v.Resource = addr
				report.Violations = append(report.Violations, v)
				report.ByCategory[v.Category]++
				report.BySeverity[v.Severity]++
				if v.Severity == "error" {
					resourceFailed = true
				}
			}
		}

		if resourceFailed {
			failedResources[addr] = true
		}
	}

	report.Failed = len(failedResources)
	report.Passed = len(resources) - report.Failed
	report.Compliant = report.BySeverity["error"] == 0

	return report
}

// EnableRule enables a rule by ID.
func (e *Engine) EnableRule(id string) {
	for i := range e.rules {
		if e.rules[i].ID == id {
			e.rules[i].Enabled = true
		}
	}
}

// DisableRule disables a rule by ID.
func (e *Engine) DisableRule(id string) {
	for i := range e.rules {
		if e.rules[i].ID == id {
			e.rules[i].Enabled = false
		}
	}
}

// ListRules returns all available rules.
func (e *Engine) ListRules() []Rule {
	return e.rules
}

func (e *Engine) loadDefaultRules() {
	e.rules = []Rule{
		// --- TAGGING ---
		{
			ID:          "tag-required",
			Name:        "Required Tags",
			Description: "All taggable resources must have Environment, Team, and ManagedBy tags",
			Severity:    "error",
			Category:    "tagging",
			Enabled:     true,
			check: func(r parser.Resource) []Violation {
				if !isTaggable(r.Type) {
					return nil
				}
				required := []string{"Environment", "Team", "ManagedBy"}
				tags := extractTags(r)
				var violations []Violation
				for _, tag := range required {
					if _, ok := tags[tag]; !ok {
						violations = append(violations, Violation{
							RuleID:     "tag-required",
							RuleName:   "Required Tags",
							Severity:   "error",
							Category:   "tagging",
							Message:    fmt.Sprintf("missing required tag '%s'", tag),
							Suggestion: fmt.Sprintf("add tag: %s = \"<value>\"", tag),
							Field:      "tags",
						})
					}
				}
				return violations
			},
		},
		{
			ID:          "tag-naming",
			Name:        "Tag Naming Convention",
			Description: "Tag keys must use PascalCase",
			Severity:    "warning",
			Category:    "tagging",
			Enabled:     true,
			check: func(r parser.Resource) []Violation {
				tags := extractTags(r)
				var violations []Violation
				for key := range tags {
					if key != "" && (key[0] < 'A' || key[0] > 'Z') {
						violations = append(violations, Violation{
							RuleID:     "tag-naming",
							RuleName:   "Tag Naming Convention",
							Severity:   "warning",
							Category:   "tagging",
							Message:    fmt.Sprintf("tag key '%s' should be PascalCase", key),
							Suggestion: fmt.Sprintf("rename to '%s'", toPascalCase(key)),
							Field:      "tags." + key,
						})
					}
				}
				return violations
			},
		},

		// --- ENCRYPTION ---
		{
			ID:          "encrypt-s3",
			Name:        "S3 Encryption",
			Description: "S3 buckets must have server-side encryption enabled",
			Severity:    "error",
			Category:    "encryption",
			Enabled:     true,
			AppliesToTypes: []string{"aws_s3_bucket"},
			check: func(r parser.Resource) []Violation {
				// Check for server_side_encryption_configuration or separate resource
				if _, ok := r.Properties["server_side_encryption_configuration"]; !ok {
					return []Violation{{
						RuleID:     "encrypt-s3",
						RuleName:   "S3 Encryption",
						Severity:   "error",
						Category:   "encryption",
						Message:    "S3 bucket has no server-side encryption configured",
						Suggestion: "add aws_s3_bucket_server_side_encryption_configuration resource or inline block",
						Field:      "server_side_encryption_configuration",
					}}
				}
				return nil
			},
		},
		{
			ID:          "encrypt-rds",
			Name:        "RDS Encryption",
			Description: "RDS instances must have storage encryption enabled",
			Severity:    "error",
			Category:    "encryption",
			Enabled:     true,
			AppliesToTypes: []string{"aws_db_instance"},
			check: func(r parser.Resource) []Violation {
				if v, ok := r.Properties["storage_encrypted"]; ok {
					if v == true || v == "true" {
						return nil
					}
				}
				return []Violation{{
					RuleID:     "encrypt-rds",
					RuleName:   "RDS Encryption",
					Severity:   "error",
					Category:   "encryption",
					Message:    "RDS instance does not have storage encryption enabled",
					Suggestion: "set storage_encrypted = true",
					Field:      "storage_encrypted",
				}}
			},
		},
		{
			ID:          "encrypt-ebs",
			Name:        "EBS Encryption",
			Description: "EBS volumes should be encrypted",
			Severity:    "warning",
			Category:    "encryption",
			Enabled:     true,
			AppliesToTypes: []string{"aws_ebs_volume", "aws_instance"},
			check: func(r parser.Resource) []Violation {
				if r.Type == "aws_ebs_volume" {
					if v, ok := r.Properties["encrypted"]; ok && (v == true || v == "true") {
						return nil
					}
					return []Violation{{
						RuleID:     "encrypt-ebs",
						RuleName:   "EBS Encryption",
						Severity:   "warning",
						Category:   "encryption",
						Message:    "EBS volume is not encrypted",
						Suggestion: "set encrypted = true",
						Field:      "encrypted",
					}}
				}
				return nil
			},
		},

		// --- NETWORKING / ACCESS ---
		{
			ID:          "no-public-s3",
			Name:        "No Public S3",
			Description: "S3 buckets must not have public ACL",
			Severity:    "error",
			Category:    "access",
			Enabled:     true,
			AppliesToTypes: []string{"aws_s3_bucket", "aws_s3_bucket_acl"},
			check: func(r parser.Resource) []Violation {
				acl := fmt.Sprintf("%v", r.Properties["acl"])
				if acl == "public-read" || acl == "public-read-write" {
					return []Violation{{
						RuleID:     "no-public-s3",
						RuleName:   "No Public S3",
						Severity:   "error",
						Category:   "access",
						Message:    "S3 bucket has public ACL — data exposure risk",
						Suggestion: "set acl = \"private\" and use bucket policies for controlled access",
						Field:      "acl",
					}}
				}
				return nil
			},
		},
		{
			ID:          "no-open-sg",
			Name:        "No Open Security Groups",
			Description: "Security groups must not allow 0.0.0.0/0 on non-standard ports",
			Severity:    "error",
			Category:    "networking",
			Enabled:     true,
			AppliesToTypes: []string{"aws_security_group", "aws_security_group_rule"},
			check: func(r parser.Resource) []Violation {
				var violations []Violation
				cidrs := extractCIDRs(r)
				for _, cidr := range cidrs {
					if cidr == "0.0.0.0/0" || cidr == "::/0" {
						port := fmt.Sprintf("%v", r.Properties["from_port"])
						// Allow 80 and 443 from anywhere (standard web ports)
						if port != "80" && port != "443" {
							violations = append(violations, Violation{
								RuleID:     "no-open-sg",
								RuleName:   "No Open Security Groups",
								Severity:   "error",
								Category:   "networking",
								Message:    fmt.Sprintf("security group allows 0.0.0.0/0 on port %s", port),
								Suggestion: "restrict CIDR to specific IP ranges or use a VPN",
								Field:      "cidr_blocks",
							})
						}
					}
				}
				return violations
			},
		},
		{
			ID:          "no-default-vpc",
			Name:        "No Default VPC",
			Description: "Resources should not use the default VPC",
			Severity:    "warning",
			Category:    "networking",
			Enabled:     true,
			check: func(r parser.Resource) []Violation {
				if r.Type == "aws_default_vpc" || r.Type == "aws_default_subnet" {
					return []Violation{{
						RuleID:     "no-default-vpc",
						RuleName:   "No Default VPC",
						Severity:   "warning",
						Category:   "networking",
						Message:    "using default VPC resources is not recommended for production",
						Suggestion: "create a dedicated VPC with proper CIDR allocation",
					}}
				}
				return nil
			},
		},
		{
			ID:          "private-subnet-nat",
			Name:        "Private Subnets Need NAT",
			Description: "Private subnets should have a NAT gateway for outbound access",
			Severity:    "info",
			Category:    "networking",
			Enabled:     true,
			AppliesToTypes: []string{"aws_subnet"},
			check: func(r parser.Resource) []Violation {
				// If map_public_ip_on_launch is false, it's a private subnet
				if v, ok := r.Properties["map_public_ip_on_launch"]; ok {
					if v == false || v == "false" {
						return []Violation{{
							RuleID:     "private-subnet-nat",
							RuleName:   "Private Subnets Need NAT",
							Severity:   "info",
							Category:   "networking",
							Message:    "private subnet detected — ensure a NAT gateway exists for outbound internet",
							Suggestion: "add aws_nat_gateway in a public subnet and a route table pointing to it",
						}}
					}
				}
				return nil
			},
		},

		// --- NAMING ---
		{
			ID:          "naming-convention",
			Name:        "Resource Naming Convention",
			Description: "Resource names should use snake_case and be descriptive",
			Severity:    "warning",
			Category:    "naming",
			Enabled:     true,
			check: func(r parser.Resource) []Violation {
				// Check for single-char or generic names
				if len(r.Name) <= 2 {
					return []Violation{{
						RuleID:     "naming-convention",
						RuleName:   "Resource Naming Convention",
						Severity:   "warning",
						Category:   "naming",
						Message:    fmt.Sprintf("resource name '%s' is too short — use descriptive names", r.Name),
						Suggestion: "use a descriptive name like 'web_server' or 'api_subnet_public'",
					}}
				}
				if strings.Contains(r.Name, "-") {
					return []Violation{{
						RuleID:     "naming-convention",
						RuleName:   "Resource Naming Convention",
						Severity:   "warning",
						Category:   "naming",
						Message:    fmt.Sprintf("resource name '%s' uses hyphens — Terraform convention is snake_case", r.Name),
						Suggestion: fmt.Sprintf("rename to '%s'", strings.ReplaceAll(r.Name, "-", "_")),
					}}
				}
				return nil
			},
		},

		// --- COST / SIZING ---
		{
			ID:          "oversized-instance",
			Name:        "Oversized Instance Check",
			Description: "Flags potentially oversized EC2 instances for cost review",
			Severity:    "info",
			Category:    "cost",
			Enabled:     true,
			AppliesToTypes: []string{"aws_instance"},
			check: func(r parser.Resource) []Violation {
				instanceType := fmt.Sprintf("%v", r.Properties["instance_type"])
				oversized := []string{"x2xlarge", "4xlarge", "8xlarge", "12xlarge", "16xlarge", "24xlarge", "metal"}
				for _, size := range oversized {
					if strings.HasSuffix(instanceType, size) {
						return []Violation{{
							RuleID:     "oversized-instance",
							RuleName:   "Oversized Instance Check",
							Severity:   "info",
							Category:   "cost",
							Message:    fmt.Sprintf("instance type '%s' is large — verify this is intentional", instanceType),
							Suggestion: "consider right-sizing or using auto-scaling",
						}}
					}
				}
				return nil
			},
		},

		// --- BACKUP / DURABILITY ---
		{
			ID:          "rds-backup",
			Name:        "RDS Backup Retention",
			Description: "RDS instances should have backup retention enabled",
			Severity:    "warning",
			Category:    "durability",
			Enabled:     true,
			AppliesToTypes: []string{"aws_db_instance"},
			check: func(r parser.Resource) []Violation {
				if v, ok := r.Properties["backup_retention_period"]; ok {
					if v == 0 || v == "0" {
						return []Violation{{
							RuleID:     "rds-backup",
							RuleName:   "RDS Backup Retention",
							Severity:   "warning",
							Category:   "durability",
							Message:    "RDS backup retention is 0 — no automated backups",
							Suggestion: "set backup_retention_period to at least 7 (days)",
							Field:      "backup_retention_period",
						}}
					}
				}
				return nil
			},
		},
		{
			ID:          "rds-multi-az",
			Name:        "RDS Multi-AZ",
			Description: "Production RDS instances should use Multi-AZ deployment",
			Severity:    "info",
			Category:    "durability",
			Enabled:     true,
			AppliesToTypes: []string{"aws_db_instance"},
			check: func(r parser.Resource) []Violation {
				if v, ok := r.Properties["multi_az"]; ok && (v == true || v == "true") {
					return nil
				}
				return []Violation{{
					RuleID:     "rds-multi-az",
					RuleName:   "RDS Multi-AZ",
					Severity:   "info",
					Category:   "durability",
					Message:    "RDS instance is not Multi-AZ — single point of failure",
					Suggestion: "set multi_az = true for production workloads",
					Field:      "multi_az",
				}}
			},
		},

		// --- LOGGING ---
		{
			ID:          "s3-logging",
			Name:        "S3 Access Logging",
			Description: "S3 buckets should have access logging enabled",
			Severity:    "warning",
			Category:    "logging",
			Enabled:     true,
			AppliesToTypes: []string{"aws_s3_bucket"},
			check: func(r parser.Resource) []Violation {
				if _, ok := r.Properties["logging"]; !ok {
					return []Violation{{
						RuleID:     "s3-logging",
						RuleName:   "S3 Access Logging",
						Severity:   "warning",
						Category:   "logging",
						Message:    "S3 bucket does not have access logging enabled",
						Suggestion: "add aws_s3_bucket_logging resource to track access",
					}}
				}
				return nil
			},
		},
		{
			ID:          "vpc-flow-logs",
			Name:        "VPC Flow Logs",
			Description: "VPCs should have flow logs for network visibility",
			Severity:    "warning",
			Category:    "logging",
			Enabled:     true,
			AppliesToTypes: []string{"aws_vpc"},
			check: func(r parser.Resource) []Violation {
				// This is a cross-resource check — we flag the VPC and suggest adding a flow log resource
				return []Violation{{
					RuleID:     "vpc-flow-logs",
					RuleName:   "VPC Flow Logs",
					Severity:   "warning",
					Category:   "logging",
					Message:    "ensure aws_flow_log resource exists for this VPC",
					Suggestion: "add an aws_flow_log resource pointing to this VPC for network audit trail",
				}}
			},
		},
	}
}

// --- helpers ---

func isTaggable(resourceType string) bool {
	// Most AWS resources support tags
	prefixes := []string{"aws_instance", "aws_vpc", "aws_subnet", "aws_security_group",
		"aws_s3_bucket", "aws_db_instance", "aws_lb", "aws_ecs", "aws_lambda",
		"aws_rds", "aws_ebs", "aws_elasticache", "aws_iam_role", "aws_kms"}
	for _, p := range prefixes {
		if strings.HasPrefix(resourceType, p) {
			return true
		}
	}
	return false
}

func extractTags(r parser.Resource) map[string]string {
	tags := make(map[string]string)
	if t, ok := r.Properties["tags"]; ok {
		if tagMap, ok := t.(map[string]interface{}); ok {
			for k, v := range tagMap {
				tags[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	return tags
}

func extractCIDRs(r parser.Resource) []string {
	var cidrs []string
	for _, field := range []string{"cidr_blocks", "ipv6_cidr_blocks"} {
		if v, ok := r.Properties[field]; ok {
			switch val := v.(type) {
			case []interface{}:
				for _, c := range val {
					cidrs = append(cidrs, fmt.Sprintf("%v", c))
				}
			case string:
				cidrs = append(cidrs, val)
			}
		}
	}
	return cidrs
}

func toPascalCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

func containsType(types []string, t string) bool {
	for _, tt := range types {
		if tt == t {
			return true
		}
	}
	return false
}

// ValidateCIDR checks if a string is a valid CIDR notation.
// Exported for use by other packages.
func ValidateCIDR(cidr string) bool {
	_, _, err := net.ParseCIDR(cidr)
	return err == nil
}
