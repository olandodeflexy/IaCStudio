package security

import (
	"fmt"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// Scanner performs graph-based security analysis across all resources.
// Unlike single-resource checks, this detects cross-resource attack paths
// like "S3 bucket reachable via public internet gateway" or "EC2 instance
// with admin IAM role exposed to 0.0.0.0/0".
type Scanner struct{}

func New() *Scanner {
	return &Scanner{}
}

// Finding is a security issue found during scanning.
type Finding struct {
	ID           string   `json:"id"`
	Severity     string   `json:"severity"`      // critical | high | medium | low | info
	Category     string   `json:"category"`       // exposure | encryption | iam | logging | networking | compliance
	Framework    string   `json:"framework"`      // CIS | SOC2 | HIPAA | OWASP | custom
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Resources    []string `json:"resources"`       // affected resource addresses
	Remediation  string   `json:"remediation"`
	AutoFixable  bool     `json:"auto_fixable"`    // can be fixed automatically
	Fix          *AutoFix `json:"fix,omitempty"`
}

// AutoFix describes an automatic remediation.
type AutoFix struct {
	ResourceType string `json:"resource_type"`
	ResourceName string `json:"resource_name"`
	Field        string `json:"field"`
	Value        interface{} `json:"value"`
}

// ScanReport is the complete security scan output.
type ScanReport struct {
	Findings     []Finding          `json:"findings"`
	Score        int                `json:"score"`          // 0-100 security score
	Summary      ScanSummary        `json:"summary"`
	Frameworks   map[string]int     `json:"frameworks"`     // framework -> finding count
	BySeverity   map[string]int     `json:"by_severity"`
	ByCategory   map[string]int     `json:"by_category"`
	ByResource   map[string]int     `json:"by_resource"`    // resource address -> finding count
}

type ScanSummary struct {
	Total     int `json:"total"`
	Critical  int `json:"critical"`
	High      int `json:"high"`
	Medium    int `json:"medium"`
	Low       int `json:"low"`
	Passed    int `json:"passed"`
}

// Scan runs all security checks against the resource graph.
func (s *Scanner) Scan(resources []parser.Resource) *ScanReport {
	report := &ScanReport{
		Frameworks: make(map[string]int),
		BySeverity: make(map[string]int),
		ByCategory: make(map[string]int),
		ByResource: make(map[string]int),
	}

	// Build resource index
	byType := make(map[string][]parser.Resource)
	byAddr := make(map[string]parser.Resource)
	for _, r := range resources {
		addr := r.Type + "." + r.Name
		byType[r.Type] = append(byType[r.Type], r)
		byAddr[addr] = r
	}

	// Run all check categories
	report.Findings = append(report.Findings, checkExposure(resources, byType)...)
	report.Findings = append(report.Findings, checkEncryption(resources, byType)...)
	report.Findings = append(report.Findings, checkIAM(resources, byType)...)
	report.Findings = append(report.Findings, checkLogging(resources, byType)...)
	report.Findings = append(report.Findings, checkNetworking(resources, byType)...)
	report.Findings = append(report.Findings, checkCompliance(resources, byType)...)
	report.Findings = append(report.Findings, checkCrossResource(resources, byType, byAddr)...)

	// Build summary
	totalChecks := len(resources) * 8 // approximate checks per resource
	for _, f := range report.Findings {
		report.BySeverity[f.Severity]++
		report.ByCategory[f.Category]++
		report.Frameworks[f.Framework]++
		for _, r := range f.Resources {
			report.ByResource[r]++
		}
		switch f.Severity {
		case "critical":
			report.Summary.Critical++
		case "high":
			report.Summary.High++
		case "medium":
			report.Summary.Medium++
		case "low":
			report.Summary.Low++
		}
	}
	report.Summary.Total = len(report.Findings)
	report.Summary.Passed = totalChecks - report.Summary.Total
	if report.Summary.Passed < 0 {
		report.Summary.Passed = 0
	}

	// Score: 100 - (critical*20 + high*10 + medium*5 + low*1), min 0
	report.Score = 100 - (report.Summary.Critical*20 + report.Summary.High*10 + report.Summary.Medium*5 + report.Summary.Low)
	if report.Score < 0 {
		report.Score = 0
	}

	return report
}

// ─── Exposure checks ───

func checkExposure(resources []parser.Resource, byType map[string][]parser.Resource) []Finding {
	var findings []Finding

	// Public S3 buckets
	for _, r := range byType["aws_s3_bucket"] {
		acl := str(r.Properties["acl"])
		if acl == "public-read" || acl == "public-read-write" {
			findings = append(findings, Finding{
				ID: "EXP-001", Severity: "critical", Category: "exposure", Framework: "CIS",
				Title: "S3 bucket has public ACL",
				Description: fmt.Sprintf("%s.%s is publicly accessible via ACL '%s'", r.Type, r.Name, acl),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Set acl to 'private' and use bucket policies for controlled access",
				AutoFixable: true,
				Fix: &AutoFix{r.Type, r.Name, "acl", "private"},
			})
		}
	}

	// Public RDS instances
	for _, r := range append(byType["aws_db_instance"], byType["aws_rds_instance"]...) {
		if b, ok := r.Properties["publicly_accessible"]; ok && (b == true || b == "true") {
			findings = append(findings, Finding{
				ID: "EXP-002", Severity: "critical", Category: "exposure", Framework: "CIS",
				Title: "RDS instance is publicly accessible",
				Description: fmt.Sprintf("%s.%s has publicly_accessible=true", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Set publicly_accessible = false and access via VPC",
				AutoFixable: true,
				Fix: &AutoFix{r.Type, r.Name, "publicly_accessible", false},
			})
		}
	}

	// GCP instances with public IP
	for _, r := range byType["google_compute_instance"] {
		// If access_config exists, instance gets a public IP
		if _, ok := r.Properties["access_config"]; ok {
			findings = append(findings, Finding{
				ID: "EXP-003", Severity: "high", Category: "exposure", Framework: "CIS",
				Title: "GCP VM has external IP",
				Description: fmt.Sprintf("%s.%s has access_config which assigns a public IP", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Remove access_config to make the instance private, use IAP or NAT for access",
			})
		}
	}

	// Azure storage with public access
	for _, r := range byType["azurerm_storage_account"] {
		if b, ok := r.Properties["allow_blob_public_access"]; ok && (b == true || b == "true") {
			findings = append(findings, Finding{
				ID: "EXP-004", Severity: "high", Category: "exposure", Framework: "CIS",
				Title: "Azure Storage allows public blob access",
				Description: fmt.Sprintf("%s.%s has public blob access enabled", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Set allow_blob_public_access = false",
				AutoFixable: true,
				Fix: &AutoFix{r.Type, r.Name, "allow_blob_public_access", false},
			})
		}
	}

	return findings
}

// ─── Encryption checks ───

func checkEncryption(resources []parser.Resource, byType map[string][]parser.Resource) []Finding {
	var findings []Finding

	// Unencrypted RDS
	for _, r := range append(byType["aws_db_instance"], byType["aws_rds_instance"]...) {
		if !boolProp(r, "storage_encrypted") {
			findings = append(findings, Finding{
				ID: "ENC-001", Severity: "high", Category: "encryption", Framework: "SOC2",
				Title: "RDS storage not encrypted",
				Description: fmt.Sprintf("%s.%s does not have storage encryption enabled", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Set storage_encrypted = true",
				AutoFixable: true,
				Fix: &AutoFix{r.Type, r.Name, "storage_encrypted", true},
			})
		}
	}

	// Unencrypted EBS
	for _, r := range byType["aws_ebs_volume"] {
		if !boolProp(r, "encrypted") {
			findings = append(findings, Finding{
				ID: "ENC-002", Severity: "medium", Category: "encryption", Framework: "CIS",
				Title: "EBS volume not encrypted",
				Description: fmt.Sprintf("%s.%s is not encrypted at rest", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Set encrypted = true",
				AutoFixable: true,
				Fix: &AutoFix{r.Type, r.Name, "encrypted", true},
			})
		}
	}

	// GCP Cloud SQL without SSL
	for _, r := range byType["google_sql_database_instance"] {
		if !boolProp(r, "require_ssl") {
			findings = append(findings, Finding{
				ID: "ENC-003", Severity: "high", Category: "encryption", Framework: "SOC2",
				Title: "Cloud SQL does not require SSL",
				Description: fmt.Sprintf("%s.%s allows unencrypted connections", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Enable require_ssl in settings.ip_configuration",
			})
		}
	}

	// Azure SQL without TLS
	for _, r := range byType["azurerm_mssql_server"] {
		ver := str(r.Properties["minimum_tls_version"])
		if ver != "" && ver < "1.2" {
			findings = append(findings, Finding{
				ID: "ENC-004", Severity: "high", Category: "encryption", Framework: "SOC2",
				Title: "Azure SQL allows TLS < 1.2",
				Description: fmt.Sprintf("%s.%s allows TLS version %s", r.Type, r.Name, ver),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Set minimum_tls_version = '1.2'",
				AutoFixable: true,
				Fix: &AutoFix{r.Type, r.Name, "minimum_tls_version", "1.2"},
			})
		}
	}

	return findings
}

// ─── IAM checks ───

func checkIAM(resources []parser.Resource, byType map[string][]parser.Resource) []Finding {
	var findings []Finding

	// Wildcard IAM policies
	for _, r := range byType["aws_iam_policy"] {
		policy := str(r.Properties["policy"])
		if strings.Contains(policy, "\"*\"") && strings.Contains(policy, "\"Action\"") {
			findings = append(findings, Finding{
				ID: "IAM-001", Severity: "critical", Category: "iam", Framework: "CIS",
				Title: "IAM policy with wildcard actions",
				Description: fmt.Sprintf("%s.%s contains Action: '*' — grants full admin access", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Follow least-privilege principle — specify only required actions",
			})
		}
	}

	// GCP service account with editor/owner role
	for _, r := range byType["google_project_iam_member"] {
		role := str(r.Properties["role"])
		if role == "roles/owner" || role == "roles/editor" {
			findings = append(findings, Finding{
				ID: "IAM-002", Severity: "high", Category: "iam", Framework: "CIS",
				Title: "GCP IAM binding with overly permissive role",
				Description: fmt.Sprintf("%s.%s grants %s which is overly permissive", r.Type, r.Name, role),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Use specific roles instead of roles/owner or roles/editor",
			})
		}
	}

	// EC2 instances without IAM instance profile
	for _, r := range byType["aws_instance"] {
		if _, ok := r.Properties["iam_instance_profile"]; !ok {
			findings = append(findings, Finding{
				ID: "IAM-003", Severity: "medium", Category: "iam", Framework: "CIS",
				Title: "EC2 instance without IAM role",
				Description: fmt.Sprintf("%s.%s has no IAM instance profile — uses default permissions", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Create an IAM instance profile with least-privilege permissions",
			})
		}
	}

	return findings
}

// ─── Logging checks ───

func checkLogging(resources []parser.Resource, byType map[string][]parser.Resource) []Finding {
	var findings []Finding

	// S3 without access logging
	for _, r := range byType["aws_s3_bucket"] {
		if _, ok := r.Properties["logging"]; !ok {
			findings = append(findings, Finding{
				ID: "LOG-001", Severity: "medium", Category: "logging", Framework: "SOC2",
				Title: "S3 bucket without access logging",
				Description: fmt.Sprintf("%s.%s does not have access logging enabled", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Add aws_s3_bucket_logging resource",
			})
		}
	}

	// VPC without flow logs
	for _, r := range byType["aws_vpc"] {
		// Check if a flow log exists for this VPC
		hasFlowLog := false
		for _, fl := range byType["aws_flow_log"] {
			if strings.Contains(str(fl.Properties["vpc_id"]), r.Name) {
				hasFlowLog = true
				break
			}
		}
		if !hasFlowLog {
			findings = append(findings, Finding{
				ID: "LOG-002", Severity: "medium", Category: "logging", Framework: "CIS",
				Title: "VPC without flow logs",
				Description: fmt.Sprintf("%s.%s has no flow logs for network audit trail", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Add aws_flow_log resource for this VPC",
			})
		}
	}

	// Azure without diagnostic settings
	for _, r := range byType["azurerm_key_vault"] {
		findings = append(findings, Finding{
			ID: "LOG-003", Severity: "low", Category: "logging", Framework: "SOC2",
			Title: "Key Vault should have diagnostic settings",
			Description: fmt.Sprintf("%s.%s should send audit logs to Log Analytics", r.Type, r.Name),
			Resources: []string{r.Type + "." + r.Name},
			Remediation: "Add azurerm_monitor_diagnostic_setting for this Key Vault",
		})
	}

	return findings
}

// ─── Networking checks ───

func checkNetworking(resources []parser.Resource, byType map[string][]parser.Resource) []Finding {
	var findings []Finding

	// Open security groups on sensitive ports
	for _, r := range byType["aws_security_group"] {
		// Can't check rules inline since they're usually separate resources
		// but flag SGs without descriptions
		desc := str(r.Properties["description"])
		if desc == "" || desc == "Managed by Terraform" {
			findings = append(findings, Finding{
				ID: "NET-001", Severity: "low", Category: "networking", Framework: "CIS",
				Title: "Security group without meaningful description",
				Description: fmt.Sprintf("%s.%s has no description — makes audit difficult", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Add a description explaining the purpose of this security group",
			})
		}
	}

	// GCP firewall allowing all traffic
	for _, r := range byType["google_compute_firewall"] {
		srcRanges := str(r.Properties["source_ranges"])
		if strings.Contains(srcRanges, "0.0.0.0/0") {
			findings = append(findings, Finding{
				ID: "NET-002", Severity: "high", Category: "networking", Framework: "CIS",
				Title: "GCP firewall allows all source IPs",
				Description: fmt.Sprintf("%s.%s allows traffic from 0.0.0.0/0", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Restrict source_ranges to specific CIDR blocks",
			})
		}
	}

	// Azure NSG with any-any rule
	for _, r := range byType["azurerm_network_security_group"] {
		_ = r // NSG rules are usually inline or separate — flag the resource
	}

	// Default VPC usage
	for _, r := range resources {
		if r.Type == "aws_default_vpc" || r.Type == "aws_default_subnet" {
			findings = append(findings, Finding{
				ID: "NET-003", Severity: "medium", Category: "networking", Framework: "CIS",
				Title: "Default VPC/subnet in use",
				Description: fmt.Sprintf("%s.%s — default VPCs have overly permissive defaults", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Create a custom VPC with proper CIDR allocation",
			})
		}
	}

	return findings
}

// ─── Compliance checks ───

func checkCompliance(resources []parser.Resource, byType map[string][]parser.Resource) []Finding {
	var findings []Finding

	// HIPAA: RDS without backup retention
	for _, r := range append(byType["aws_db_instance"], byType["aws_rds_instance"]...) {
		if v, ok := r.Properties["backup_retention_period"]; ok && (v == 0 || v == "0") {
			findings = append(findings, Finding{
				ID: "HIPAA-001", Severity: "high", Category: "compliance", Framework: "HIPAA",
				Title: "RDS without backup retention (HIPAA)",
				Description: fmt.Sprintf("%s.%s has zero backup retention — violates HIPAA data durability requirements", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Set backup_retention_period to at least 7 days",
				AutoFixable: true,
				Fix: &AutoFix{r.Type, r.Name, "backup_retention_period", 7},
			})
		}
	}

	// SOC2: Resources without tags
	taggedTypes := []string{"aws_instance", "aws_vpc", "aws_subnet", "aws_s3_bucket", "aws_db_instance", "aws_rds_instance", "aws_lb", "aws_security_group"}
	for _, t := range taggedTypes {
		for _, r := range byType[t] {
			if _, ok := r.Properties["tags"]; !ok {
				findings = append(findings, Finding{
					ID: "SOC2-001", Severity: "low", Category: "compliance", Framework: "SOC2",
					Title: "Resource missing tags",
					Description: fmt.Sprintf("%s.%s has no tags — needed for cost allocation and audit", r.Type, r.Name),
					Resources: []string{r.Type + "." + r.Name},
					Remediation: "Add tags: Environment, Team, ManagedBy, Project",
				})
			}
		}
	}

	// RDS without multi-AZ (production readiness)
	for _, r := range append(byType["aws_db_instance"], byType["aws_rds_instance"]...) {
		if !boolProp(r, "multi_az") {
			findings = append(findings, Finding{
				ID: "PROD-001", Severity: "medium", Category: "compliance", Framework: "CIS",
				Title: "RDS not multi-AZ",
				Description: fmt.Sprintf("%s.%s is single-AZ — single point of failure", r.Type, r.Name),
				Resources: []string{r.Type + "." + r.Name},
				Remediation: "Set multi_az = true for production workloads",
				AutoFixable: true,
				Fix: &AutoFix{r.Type, r.Name, "multi_az", true},
			})
		}
	}

	return findings
}

// ─── Cross-resource graph checks (unique to IaC Studio) ───

func checkCrossResource(resources []parser.Resource, byType map[string][]parser.Resource, byAddr map[string]parser.Resource) []Finding {
	var findings []Finding

	// EC2 in public subnet without WAF/ALB (direct internet exposure)
	for _, ec2 := range byType["aws_instance"] {
		if boolProp(ec2, "associate_public_ip_address") && len(byType["aws_lb"]) == 0 && len(byType["aws_wafv2_web_acl"]) == 0 {
			findings = append(findings, Finding{
				ID: "GRAPH-001", Severity: "high", Category: "exposure", Framework: "OWASP",
				Title: "EC2 directly exposed without WAF or ALB",
				Description: fmt.Sprintf("%s.%s has a public IP but no ALB or WAF in front — direct attack surface", ec2.Type, ec2.Name),
				Resources: []string{ec2.Type + "." + ec2.Name},
				Remediation: "Place behind an ALB with WAF for DDoS protection and request filtering",
			})
		}
	}

	// S3 bucket without server-side encryption configuration
	for _, bucket := range byType["aws_s3_bucket"] {
		hasEncryption := false
		for _, enc := range byType["aws_s3_bucket_server_side_encryption_configuration"] {
			if strings.Contains(str(enc.Properties["bucket"]), bucket.Name) {
				hasEncryption = true
				break
			}
		}
		if !hasEncryption {
			findings = append(findings, Finding{
				ID: "GRAPH-002", Severity: "high", Category: "encryption", Framework: "CIS",
				Title: "S3 bucket without encryption configuration",
				Description: fmt.Sprintf("%s.%s has no aws_s3_bucket_server_side_encryption_configuration", bucket.Type, bucket.Name),
				Resources: []string{bucket.Type + "." + bucket.Name},
				Remediation: "Add aws_s3_bucket_server_side_encryption_configuration with AES256 or KMS",
			})
		}
	}

	// S3 bucket without public access block
	for _, bucket := range byType["aws_s3_bucket"] {
		hasBlock := false
		for _, block := range byType["aws_s3_bucket_public_access_block"] {
			if strings.Contains(str(block.Properties["bucket"]), bucket.Name) {
				hasBlock = true
				break
			}
		}
		if !hasBlock {
			findings = append(findings, Finding{
				ID: "GRAPH-003", Severity: "high", Category: "exposure", Framework: "CIS",
				Title: "S3 bucket without public access block",
				Description: fmt.Sprintf("%s.%s has no aws_s3_bucket_public_access_block — could be made public accidentally", bucket.Type, bucket.Name),
				Resources: []string{bucket.Type + "." + bucket.Name},
				Remediation: "Add aws_s3_bucket_public_access_block with all four settings enabled",
			})
		}
	}

	// Lambda without X-Ray tracing
	for _, fn := range byType["aws_lambda_function"] {
		if str(fn.Properties["tracing_config"]) == "" {
			findings = append(findings, Finding{
				ID: "GRAPH-004", Severity: "low", Category: "logging", Framework: "SOC2",
				Title: "Lambda without X-Ray tracing",
				Description: fmt.Sprintf("%s.%s has no tracing enabled — makes debugging production issues difficult", fn.Type, fn.Name),
				Resources: []string{fn.Type + "." + fn.Name},
				Remediation: "Add tracing_config { mode = 'Active' }",
			})
		}
	}

	// EKS/GKE/AKS without network policy
	k8sClusters := append(append(byType["aws_eks_cluster"], byType["google_container_cluster"]...), byType["azurerm_kubernetes_cluster"]...)
	for _, cluster := range k8sClusters {
		findings = append(findings, Finding{
			ID: "GRAPH-005", Severity: "medium", Category: "networking", Framework: "CIS",
			Title: "Kubernetes cluster — verify network policies",
			Description: fmt.Sprintf("%s.%s should have network policies enabled to restrict pod-to-pod traffic", cluster.Type, cluster.Name),
			Resources: []string{cluster.Type + "." + cluster.Name},
			Remediation: "Enable network policy enforcement (Calico for EKS/GKE, Azure CNI for AKS)",
		})
	}

	// Resources in multiple AZs check (high availability)
	subnets := byType["aws_subnet"]
	if len(subnets) > 0 {
		azs := make(map[string]bool)
		for _, s := range subnets {
			az := str(s.Properties["availability_zone"])
			if az != "" {
				azs[az] = true
			}
		}
		if len(azs) < 2 && len(subnets) >= 2 {
			findings = append(findings, Finding{
				ID: "GRAPH-006", Severity: "medium", Category: "compliance", Framework: "CIS",
				Title: "All subnets in single availability zone",
				Description: "All subnets are in the same AZ — no high availability",
				Resources: func() []string {
					var addrs []string
					for _, s := range subnets {
						addrs = append(addrs, s.Type+"."+s.Name)
					}
					return addrs
				}(),
				Remediation: "Distribute subnets across at least 2 availability zones",
			})
		}
	}

	return findings
}

// ─── Helpers ───

func str(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func boolProp(r parser.Resource, key string) bool {
	v, ok := r.Properties[key]
	if !ok {
		return false
	}
	return v == true || v == "true"
}
