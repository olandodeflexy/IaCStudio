package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ProviderSchema represents the output of `terraform providers schema -json`.
type ProviderSchema struct {
	FormatVersion string                       `json:"format_version"`
	Schemas       map[string]ProviderSchemaData `json:"provider_schemas"`
}

type ProviderSchemaData struct {
	Provider          SchemaRepr                `json:"provider"`
	ResourceSchemas   map[string]SchemaRepr     `json:"resource_schemas"`
	DataSourceSchemas map[string]SchemaRepr     `json:"data_source_schemas"`
}

type SchemaRepr struct {
	Version int       `json:"version"`
	Block   BlockRepr `json:"block"`
}

type BlockRepr struct {
	Attributes map[string]AttributeRepr `json:"attributes,omitempty"`
	BlockTypes map[string]BlockTypeRepr `json:"block_types,omitempty"`
}

type AttributeRepr struct {
	Type        json.RawMessage `json:"type"`
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Optional    bool            `json:"optional"`
	Computed    bool            `json:"computed"`
	Sensitive   bool            `json:"sensitive"`
}

type BlockTypeRepr struct {
	NestingMode string    `json:"nesting_mode"`
	Block       BlockRepr `json:"block"`
	MinItems    int       `json:"min_items"`
	MaxItems    int       `json:"max_items"`
}

// DynamicCatalog fetches real provider schemas by running terraform.
type DynamicCatalog struct {
	cacheDir string
	cacheTTL time.Duration
}

func NewDynamicCatalog(cacheDir string) *DynamicCatalog {
	return &DynamicCatalog{
		cacheDir: cacheDir,
		cacheTTL: 24 * time.Hour, // cache schemas for 24 hours
	}
}

// FetchProviderSchema runs `terraform providers schema -json` in a temp project
// with the given providers and returns the full schema.
func (dc *DynamicCatalog) FetchProviderSchema(ctx context.Context, providers []string) (*ProviderSchema, error) {
	// Check cache first
	cacheKey := strings.Join(providers, "_")
	if cached, err := dc.loadCache(cacheKey); err == nil {
		return cached, nil
	}

	// Create a temporary directory with a minimal terraform config
	tmpDir, err := os.MkdirTemp("", "iac-studio-schema-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a minimal config that references the requested providers
	config := generateProviderConfig(providers)
	configPath := filepath.Join(tmpDir, "main.tf")
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return nil, fmt.Errorf("writing config: %w", err)
	}

	// Run terraform init
	initCmd := exec.CommandContext(ctx, "terraform", "init", "-no-color")
	initCmd.Dir = tmpDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("terraform init failed: %s: %w", string(out), err)
	}

	// Run terraform providers schema -json
	schemaCmd := exec.CommandContext(ctx, "terraform", "providers", "schema", "-json", "-no-color")
	schemaCmd.Dir = tmpDir
	out, err := schemaCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("terraform providers schema failed: %w", err)
	}

	var schema ProviderSchema
	if err := json.Unmarshal(out, &schema); err != nil {
		return nil, fmt.Errorf("parsing schema: %w", err)
	}

	// Cache the result
	dc.saveCache(cacheKey, &schema)

	return &schema, nil
}

// ConvertToResources converts a provider schema into our Resource catalog format.
func (dc *DynamicCatalog) ConvertToResources(schema *ProviderSchema) []Resource {
	var resources []Resource

	for providerName, providerData := range schema.Schemas {
		// Extract short provider name (e.g., "aws" from "registry.terraform.io/hashicorp/aws")
		shortName := extractProviderShortName(providerName)

		for resourceType, resourceSchema := range providerData.ResourceSchemas {
			r := Resource{
				Type:     resourceType,
				Label:    humanizeResourceType(resourceType, shortName),
				Icon:     guessIcon(resourceType),
				Category: guessCategory(resourceType),
				Provider: shortName,
				Defaults: make(map[string]any),
				Fields:   convertAttributes(resourceSchema.Block.Attributes),
				ConnectsVia: detectConnections(resourceType, resourceSchema.Block.Attributes),
			}

			// Set sensible defaults for required fields
			for name, attr := range resourceSchema.Block.Attributes {
				if attr.Required && !attr.Computed {
					r.Defaults[name] = defaultForType(attr.Type, name)
				}
			}

			resources = append(resources, r)
		}
	}

	return resources
}

// ─── Helpers ───

// validProviderName checks that a provider name segment is safe to embed in HCL.
// Allows only lowercase alphanumeric and hyphens (matching Terraform registry naming).
func validProviderName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

func generateProviderConfig(providers []string) string {
	var b strings.Builder
	b.WriteString("terraform {\n  required_providers {\n")
	for _, p := range providers {
		parts := strings.Split(p, "/")
		// Validate every segment of the provider name (e.g. "hashicorp/aws")
		valid := true
		for _, part := range parts {
			if !validProviderName(part) {
				valid = false
				break
			}
		}
		if !valid {
			continue // skip invalid provider names
		}
		name := parts[len(parts)-1]
		source := p
		if !strings.Contains(p, "/") {
			source = "hashicorp/" + p
		}
		b.WriteString(fmt.Sprintf("    %s = {\n      source = \"%s\"\n    }\n", name, source))
	}
	b.WriteString("  }\n}\n")
	return b.String()
}

func extractProviderShortName(fullName string) string {
	parts := strings.Split(fullName, "/")
	return parts[len(parts)-1]
}

func humanizeResourceType(resourceType, provider string) string {
	// aws_vpc → VPC, aws_s3_bucket → S3 Bucket
	name := strings.TrimPrefix(resourceType, provider+"_")
	name = strings.ReplaceAll(name, "_", " ")
	words := strings.Fields(name)
	for i, w := range words {
		// Keep acronyms uppercase
		upper := strings.ToUpper(w)
		if len(w) <= 3 || isAcronym(upper) {
			words[i] = upper
		} else {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func isAcronym(s string) bool {
	acronyms := map[string]bool{
		"VPC": true, "EC2": true, "IAM": true, "RDS": true, "ECS": true,
		"EKS": true, "S3": true, "SQS": true, "SNS": true, "ALB": true,
		"NLB": true, "ELB": true, "EBS": true, "EFS": true, "EIP": true,
		"AMI": true, "ACM": true, "KMS": true, "WAF": true, "API": true,
		"DNS": true, "NAT": true, "VPN": true, "SSL": true, "TLS": true,
		"SSH": true, "HTTP": true, "HTTPS": true, "TCP": true, "UDP": true,
		"DB": true, "LB": true, "SG": true, "AZ": true, "ARN": true,
		"GCP": true, "VM": true, "IP": true, "ID": true,
	}
	return acronyms[s]
}

func guessIcon(resourceType string) string {
	iconMap := map[string]string{
		"vpc": "🌐", "subnet": "📡", "instance": "🖥️", "s3": "🪣",
		"security_group": "🛡️", "iam": "🔑", "rds": "🗄️", "lambda": "⚡",
		"dynamodb": "📊", "sqs": "📬", "sns": "📢", "ecs": "🚢",
		"eks": "☸️", "elb": "⚖️", "lb": "⚖️", "ebs": "💾",
		"efs": "📁", "route": "🗺️", "nat": "🔄", "internet_gateway": "🌍",
		"kms": "🔐", "acm": "📜", "cloudwatch": "📋", "cloudfront": "🌎",
		"api_gateway": "🚪", "waf": "🧱", "elasticache": "⚡",
		"eip": "📌", "key_pair": "🔑", "launch_template": "📋",
		"autoscaling": "📈", "ecr": "📦", "codebuild": "🏗️",
		"codepipeline": "🔄", "secretsmanager": "🔒", "ssm": "⚙️",
	}
	for keyword, icon := range iconMap {
		if strings.Contains(resourceType, keyword) {
			return icon
		}
	}
	return "📦"
}

func guessCategory(resourceType string) string {
	categoryMap := map[string]string{
		"vpc": "Networking", "subnet": "Networking", "route": "Networking",
		"internet_gateway": "Networking", "nat_gateway": "Networking",
		"eip": "Networking", "network": "Networking", "vpn": "Networking",
		"instance": "Compute", "lambda": "Compute", "ecs": "Compute",
		"eks": "Compute", "launch": "Compute", "autoscaling": "Compute",
		"batch": "Compute",
		"s3": "Storage", "ebs": "Storage", "efs": "Storage",
		"glacier": "Storage", "backup": "Storage",
		"rds": "Database", "dynamodb": "Database", "elasticache": "Database",
		"redshift": "Database", "docdb": "Database", "neptune": "Database",
		"security_group": "Security", "iam": "Security", "kms": "Security",
		"acm": "Security", "waf": "Security", "secretsmanager": "Security",
		"guardduty": "Security", "inspector": "Security",
		"lb": "Load Balancing", "elb": "Load Balancing", "target_group": "Load Balancing",
		"cloudwatch": "Monitoring", "sns": "Monitoring", "sqs": "Monitoring",
		"cloudtrail": "Monitoring",
		"route53": "DNS", "cloudfront": "CDN",
		"api_gateway": "API", "appsync": "API",
		"codebuild": "CI/CD", "codepipeline": "CI/CD", "codecommit": "CI/CD",
		"ecr": "Containers",
	}
	for keyword, category := range categoryMap {
		if strings.Contains(resourceType, keyword) {
			return category
		}
	}
	return "Other"
}

func convertAttributes(attrs map[string]AttributeRepr) []Field {
	var fields []Field
	for name, attr := range attrs {
		if attr.Computed && !attr.Optional {
			continue // skip read-only attributes
		}
		f := Field{
			Name:        name,
			Type:        parseAttrType(attr.Type),
			Required:    attr.Required,
			Description: attr.Description,
		}
		fields = append(fields, f)
	}
	return fields
}

func parseAttrType(raw json.RawMessage) string {
	var simple string
	if err := json.Unmarshal(raw, &simple); err == nil {
		switch simple {
		case "string":
			return "string"
		case "number":
			return "number"
		case "bool":
			return "bool"
		}
		return "string"
	}
	// Complex type (list, set, map, object)
	return "string"
}

func defaultForType(raw json.RawMessage, name string) any {
	// Common field defaults
	commonDefaults := map[string]any{
		"region":          "us-east-1",
		"instance_type":   "t3.micro",
		"engine":          "postgres",
		"allocated_storage": 20,
		"name":            "my-resource",
	}
	if v, ok := commonDefaults[name]; ok {
		return v
	}

	var simple string
	if err := json.Unmarshal(raw, &simple); err == nil {
		switch simple {
		case "bool":
			return false
		case "number":
			return 0
		}
	}
	return ""
}

func detectConnections(resourceType string, attrs map[string]AttributeRepr) map[string]string {
	connections := make(map[string]string)

	// Common reference patterns
	refPatterns := map[string]string{
		"vpc_id":                    "aws_vpc",
		"subnet_id":                "aws_subnet",
		"subnet_ids":               "aws_subnet",
		"security_group_ids":       "aws_security_group",
		"vpc_security_group_ids":   "aws_security_group",
		"iam_role_arn":             "aws_iam_role",
		"role_arn":                 "aws_iam_role",
		"kms_key_id":              "aws_kms_key",
		"target_group_arn":        "aws_lb_target_group",
		"load_balancer_arn":       "aws_lb",
		"db_subnet_group_name":    "aws_db_subnet_group",
		"cluster_id":              "aws_ecs_cluster",
		"launch_template_id":      "aws_launch_template",
		"allocation_id":           "aws_eip",
		"zone_id":                 "aws_route53_zone",
		"certificate_arn":         "aws_acm_certificate",
	}

	for attrName := range attrs {
		if target, ok := refPatterns[attrName]; ok {
			// Only add if the resource type uses the same provider
			provider := strings.Split(resourceType, "_")[0]
			if strings.HasPrefix(target, provider+"_") {
				connections[attrName] = target
			}
		}
	}

	return connections
}

// ─── Cache ───

func (dc *DynamicCatalog) cacheFile(key string) string {
	return filepath.Join(dc.cacheDir, "schema_"+key+".json")
}

func (dc *DynamicCatalog) loadCache(key string) (*ProviderSchema, error) {
	path := dc.cacheFile(key)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if time.Since(info.ModTime()) > dc.cacheTTL {
		return nil, fmt.Errorf("cache expired")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var schema ProviderSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}

	log.Printf("Loaded provider schema from cache: %s", path)
	return &schema, nil
}

func (dc *DynamicCatalog) saveCache(key string, schema *ProviderSchema) {
	os.MkdirAll(dc.cacheDir, 0755)
	data, err := json.Marshal(schema)
	if err != nil {
		return
	}
	os.WriteFile(dc.cacheFile(key), data, 0644)
	log.Printf("Cached provider schema: %s", dc.cacheFile(key))
}
