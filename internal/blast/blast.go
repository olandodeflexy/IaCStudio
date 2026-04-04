package blast

import (
	"fmt"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// Analyzer computes the blast radius of changes — what breaks if you modify or delete a resource.
// This is the "show me the damage" tool every DevOps engineer needs before hitting apply.
type Analyzer struct{}

func New() *Analyzer {
	return &Analyzer{}
}

// BlastResult describes the full impact of modifying or deleting a resource.
type BlastResult struct {
	Target         string           `json:"target"`          // resource being changed, e.g. "aws_vpc.main"
	Action         string           `json:"action"`          // delete | modify
	DirectImpact   []ImpactedResource `json:"direct_impact"`   // resources directly referencing target
	IndirectImpact []ImpactedResource `json:"indirect_impact"` // resources referencing directly impacted resources
	TotalAffected  int              `json:"total_affected"`
	Severity       string           `json:"severity"`        // low | medium | high | critical
	Warnings       []string         `json:"warnings,omitempty"`
}

// ImpactedResource is a resource affected by the change.
type ImpactedResource struct {
	Address    string   `json:"address"`     // e.g., "aws_subnet.public"
	Type       string   `json:"type"`
	Name       string   `json:"name"`
	Reason     string   `json:"reason"`      // why it's impacted
	Fields     []string `json:"fields"`      // which fields reference the target
	Depth      int      `json:"depth"`       // 1 = direct, 2 = indirect, etc.
	WillBreak  bool     `json:"will_break"`  // true if this resource will fail without the target
}

// DependencyGraph maps resources to their dependencies and dependents.
type DependencyGraph struct {
	// Forward: resource -> resources it depends on
	Dependencies map[string][]Dependency `json:"dependencies"`
	// Reverse: resource -> resources that depend on it
	Dependents   map[string][]Dependency `json:"dependents"`
	// All resource addresses
	Resources    []string               `json:"resources"`
}

// Dependency describes one edge in the dependency graph.
type Dependency struct {
	From  string `json:"from"`   // the resource that has the reference
	To    string `json:"to"`     // the resource being referenced
	Field string `json:"field"`  // which field creates the dependency (e.g., "vpc_id")
}

// BuildGraph constructs a dependency graph from a set of resources.
// It detects references like `aws_vpc.main.id` in field values and
// infers dependencies from field naming patterns (e.g., vpc_id -> aws_vpc).
func (a *Analyzer) BuildGraph(resources []parser.Resource) *DependencyGraph {
	graph := &DependencyGraph{
		Dependencies: make(map[string][]Dependency),
		Dependents:   make(map[string][]Dependency),
	}

	// Index resources by address and type.name
	byAddress := make(map[string]parser.Resource)
	for _, r := range resources {
		addr := r.Type + "." + r.Name
		byAddress[addr] = r
		graph.Resources = append(graph.Resources, addr)
	}

	// Scan every field of every resource for references
	for _, r := range resources {
		fromAddr := r.Type + "." + r.Name

		for fieldName, fieldValue := range r.Properties {
			strVal := fmt.Sprintf("%v", fieldValue)

			// Method 1: Explicit terraform reference (aws_vpc.main.id)
			for toAddr := range byAddress {
				if strings.Contains(strVal, toAddr+".") || strings.Contains(strVal, toAddr) {
					dep := Dependency{From: fromAddr, To: toAddr, Field: fieldName}
					graph.Dependencies[fromAddr] = append(graph.Dependencies[fromAddr], dep)
					graph.Dependents[toAddr] = append(graph.Dependents[toAddr], dep)
				}
			}

			// Method 2: Field name pattern inference
			// vpc_id -> look for aws_vpc, subnet_id -> look for aws_subnet, etc.
			if strings.HasSuffix(fieldName, "_id") || strings.HasSuffix(fieldName, "_ids") ||
				strings.HasSuffix(fieldName, "_arn") {
				inferredType := inferResourceType(fieldName)
				if inferredType != "" {
					for toAddr, toRes := range byAddress {
						if toRes.Type == inferredType && toAddr != fromAddr {
							// Check if this dependency was already found by explicit reference
							if !hasDep(graph.Dependencies[fromAddr], toAddr, fieldName) {
								dep := Dependency{From: fromAddr, To: toAddr, Field: fieldName}
								graph.Dependencies[fromAddr] = append(graph.Dependencies[fromAddr], dep)
								graph.Dependents[toAddr] = append(graph.Dependents[toAddr], dep)
							}
						}
					}
				}
			}
		}
	}

	return graph
}

// Analyze computes the blast radius of deleting or modifying a resource.
func (a *Analyzer) Analyze(graph *DependencyGraph, targetAddr, action string) *BlastResult {
	result := &BlastResult{
		Target: targetAddr,
		Action: action,
	}

	// Direct impact: resources that reference the target
	visited := make(map[string]bool)
	visited[targetAddr] = true

	if deps, ok := graph.Dependents[targetAddr]; ok {
		for _, dep := range deps {
			if visited[dep.From] {
				continue
			}
			visited[dep.From] = true

			parts := strings.SplitN(dep.From, ".", 2)
			typeName, name := parts[0], ""
			if len(parts) > 1 {
				name = parts[1]
			}

			impacted := ImpactedResource{
				Address:   dep.From,
				Type:      typeName,
				Name:      name,
				Reason:    fmt.Sprintf("references %s via field '%s'", targetAddr, dep.Field),
				Fields:    []string{dep.Field},
				Depth:     1,
				WillBreak: action == "delete", // deletion breaks dependents
			}
			result.DirectImpact = append(result.DirectImpact, impacted)
		}
	}

	// Indirect impact: walk the graph one more level deep
	for _, direct := range result.DirectImpact {
		if deps, ok := graph.Dependents[direct.Address]; ok {
			for _, dep := range deps {
				if visited[dep.From] {
					continue
				}
				visited[dep.From] = true

				parts := strings.SplitN(dep.From, ".", 2)
				typeName, name := parts[0], ""
				if len(parts) > 1 {
					name = parts[1]
				}

				impacted := ImpactedResource{
					Address:   dep.From,
					Type:      typeName,
					Name:      name,
					Reason:    fmt.Sprintf("depends on %s which references %s", direct.Address, targetAddr),
					Fields:    []string{dep.Field},
					Depth:     2,
					WillBreak: action == "delete",
				}
				result.IndirectImpact = append(result.IndirectImpact, impacted)
			}
		}
	}

	result.TotalAffected = len(result.DirectImpact) + len(result.IndirectImpact)
	result.Severity = classifySeverity(result)

	// Add warnings for dangerous patterns
	if action == "delete" {
		for _, r := range result.DirectImpact {
			if strings.Contains(r.Type, "vpc") || strings.Contains(r.Type, "network") {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("Deleting %s will cascade to networking resource %s", targetAddr, r.Address))
			}
			if strings.Contains(r.Type, "db") || strings.Contains(r.Type, "rds") {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("WARNING: Database resource %s depends on %s — data loss risk!", r.Address, targetAddr))
			}
		}
	}

	return result
}

// AnalyzeAll computes blast radius for every resource (useful for overview visualization).
func (a *Analyzer) AnalyzeAll(graph *DependencyGraph) map[string]*BlastResult {
	results := make(map[string]*BlastResult)
	for _, addr := range graph.Resources {
		results[addr] = a.Analyze(graph, addr, "delete")
	}
	return results
}

func classifySeverity(result *BlastResult) string {
	switch {
	case result.TotalAffected == 0:
		return "low"
	case result.TotalAffected <= 2:
		return "medium"
	case result.TotalAffected <= 5:
		return "high"
	default:
		return "critical"
	}
}

// inferResourceType guesses the terraform resource type from a field name.
// vpc_id -> aws_vpc, subnet_id -> aws_subnet, etc.
func inferResourceType(fieldName string) string {
	mapping := map[string]string{
		"vpc_id":             "aws_vpc",
		"subnet_id":          "aws_subnet",
		"subnet_ids":         "aws_subnet",
		"security_group_id":  "aws_security_group",
		"security_group_ids": "aws_security_group",
		"iam_role_arn":       "aws_iam_role",
		"role_arn":           "aws_iam_role",
		"target_group_arn":   "aws_lb_target_group",
		"lb_arn":             "aws_lb",
		"certificate_arn":    "aws_acm_certificate",
		"kms_key_id":         "aws_kms_key",
		"log_group_name":     "aws_cloudwatch_log_group",
		"sns_topic_arn":      "aws_sns_topic",
		"sqs_queue_arn":      "aws_sqs_queue",
		"bucket":             "aws_s3_bucket",
		"function_name":      "aws_lambda_function",
		"cluster_id":         "aws_ecs_cluster",
		"db_subnet_group_name": "aws_db_subnet_group",
		"route_table_id":     "aws_route_table",
		"internet_gateway_id": "aws_internet_gateway",
		"nat_gateway_id":     "aws_nat_gateway",
		"eip_id":             "aws_eip",
	}

	if t, ok := mapping[fieldName]; ok {
		return t
	}
	return ""
}

func hasDep(deps []Dependency, toAddr, field string) bool {
	for _, d := range deps {
		if d.To == toAddr && d.Field == field {
			return true
		}
	}
	return false
}
