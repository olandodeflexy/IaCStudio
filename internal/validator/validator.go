package validator

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// Severity indicates the importance of a validation issue.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

// Issue describes a single validation problem.
type Issue struct {
	ResourceID string   `json:"resource_id"`
	Field      string   `json:"field"`
	Severity   Severity `json:"severity"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
}

// Validate checks a list of resources for common issues.
func Validate(resources []parser.Resource) []Issue {
	var issues []Issue

	resourcesByType := make(map[string][]parser.Resource)
	for _, r := range resources {
		resourcesByType[r.Type] = append(resourcesByType[r.Type], r)
	}

	for _, r := range resources {
		issues = append(issues, validateResource(r, resources)...)
	}

	issues = append(issues, validateGlobal(resources, resourcesByType)...)

	return issues
}

func validateResource(r parser.Resource, all []parser.Resource) []Issue {
	var issues []Issue

	// Check required name
	if r.Name == "" {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "name", Severity: Error,
			Message: "Resource must have a name",
		})
	}

	// Name format validation
	if r.Name != "" && !isValidResourceName(r.Name) {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "name", Severity: Error,
			Message:    "Resource name must start with a letter and contain only letters, numbers, and underscores",
			Suggestion: sanitizeName(r.Name),
		})
	}

	// Duplicate names
	for _, other := range all {
		if other.ID != r.ID && other.Type == r.Type && other.Name == r.Name {
			issues = append(issues, Issue{
				ResourceID: r.ID, Field: "name", Severity: Error,
				Message:    fmt.Sprintf("Duplicate resource: %s.%s already exists", r.Type, r.Name),
				Suggestion: r.Name + "_2",
			})
			break
		}
	}

	// Type-specific validation
	switch r.Type {
	case "aws_vpc":
		issues = append(issues, validateVPC(r)...)
	case "aws_subnet":
		issues = append(issues, validateSubnet(r, all)...)
	case "aws_instance":
		issues = append(issues, validateInstance(r)...)
	case "aws_s3_bucket":
		issues = append(issues, validateS3Bucket(r)...)
	case "aws_rds_instance":
		issues = append(issues, validateRDS(r)...)
	case "aws_security_group":
		issues = append(issues, validateSecurityGroup(r)...)
	case "aws_lambda_function":
		issues = append(issues, validateLambda(r)...)
	}

	return issues
}

func validateVPC(r parser.Resource) []Issue {
	var issues []Issue
	cidr, _ := r.Properties["cidr_block"].(string)
	if cidr == "" {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "cidr_block", Severity: Error,
			Message: "VPC must have a CIDR block",
		})
	} else if !isValidCIDR(cidr) {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "cidr_block", Severity: Error,
			Message: fmt.Sprintf("Invalid CIDR block: %s", cidr),
		})
	}

	// Warn if DNS is disabled
	dnsSupport, ok := r.Properties["enable_dns_support"]
	if ok && dnsSupport == false {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "enable_dns_support", Severity: Warning,
			Message: "DNS support is disabled — many AWS services require this",
		})
	}

	return issues
}

func validateSubnet(r parser.Resource, all []parser.Resource) []Issue {
	var issues []Issue
	cidr, _ := r.Properties["cidr_block"].(string)
	if cidr == "" {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "cidr_block", Severity: Error,
			Message: "Subnet must have a CIDR block",
		})
	} else if !isValidCIDR(cidr) {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "cidr_block", Severity: Error,
			Message: fmt.Sprintf("Invalid CIDR block: %s", cidr),
		})
	}

	// Check for overlapping subnets
	for _, other := range all {
		if other.ID == r.ID || other.Type != "aws_subnet" {
			continue
		}
		otherCIDR, _ := other.Properties["cidr_block"].(string)
		if cidr != "" && otherCIDR != "" && cidrsOverlap(cidr, otherCIDR) {
			issues = append(issues, Issue{
				ResourceID: r.ID, Field: "cidr_block", Severity: Error,
				Message: fmt.Sprintf("CIDR %s overlaps with subnet %s (%s)", cidr, other.Name, otherCIDR),
			})
		}
	}

	return issues
}

func validateInstance(r parser.Resource) []Issue {
	var issues []Issue
	ami, _ := r.Properties["ami"].(string)
	if ami == "" {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "ami", Severity: Error,
			Message: "EC2 instance requires an AMI ID",
		})
	}

	instanceType, _ := r.Properties["instance_type"].(string)
	if instanceType == "" {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "instance_type", Severity: Error,
			Message: "EC2 instance requires an instance type",
		})
	}

	return issues
}

func validateS3Bucket(r parser.Resource) []Issue {
	var issues []Issue
	bucket, _ := r.Properties["bucket"].(string)
	if bucket == "" {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "bucket", Severity: Error,
			Message: "S3 bucket requires a name",
		})
	} else if !isValidBucketName(bucket) {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "bucket", Severity: Error,
			Message: "S3 bucket names must be 3-63 chars, lowercase, no underscores",
		})
	}

	return issues
}

func validateRDS(r parser.Resource) []Issue {
	var issues []Issue

	skipSnapshot, _ := r.Properties["skip_final_snapshot"].(bool)
	if skipSnapshot {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "skip_final_snapshot", Severity: Warning,
			Message: "Final snapshot is skipped — data will be lost on destroy",
		})
	}

	encrypted, ok := r.Properties["storage_encrypted"]
	if !ok || encrypted == false {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "storage_encrypted", Severity: Warning,
			Message:    "Storage encryption is not enabled",
			Suggestion: "Set storage_encrypted = true for production databases",
		})
	}

	return issues
}

func validateSecurityGroup(r parser.Resource) []Issue {
	var issues []Issue

	name, _ := r.Properties["name"].(string)
	if name == "" {
		issues = append(issues, Issue{
			ResourceID: r.ID, Field: "name", Severity: Error,
			Message: "Security group requires a name",
		})
	}

	return issues
}

func validateLambda(r parser.Resource) []Issue {
	var issues []Issue

	memSize, ok := r.Properties["memory_size"]
	if ok {
		mem, _ := toInt(memSize)
		if mem < 128 || mem > 10240 {
			issues = append(issues, Issue{
				ResourceID: r.ID, Field: "memory_size", Severity: Error,
				Message: "Lambda memory must be between 128 and 10240 MB",
			})
		}
	}

	timeout, ok := r.Properties["timeout"]
	if ok {
		t, _ := toInt(timeout)
		if t > 900 {
			issues = append(issues, Issue{
				ResourceID: r.ID, Field: "timeout", Severity: Error,
				Message: "Lambda timeout cannot exceed 900 seconds (15 minutes)",
			})
		}
	}

	return issues
}

func validateGlobal(resources []parser.Resource, byType map[string][]parser.Resource) []Issue {
	var issues []Issue

	// Warn if subnets exist without a VPC
	if len(byType["aws_subnet"]) > 0 && len(byType["aws_vpc"]) == 0 {
		issues = append(issues, Issue{
			Severity: Warning,
			Message:  "Subnets defined without a VPC — make sure they reference an existing VPC",
		})
	}

	// Warn if EC2 instances lack security groups
	for _, inst := range byType["aws_instance"] {
		if _, ok := inst.Properties["vpc_security_group_ids"]; !ok {
			issues = append(issues, Issue{
				ResourceID: inst.ID, Severity: Warning,
				Message: "EC2 instance has no security group — it will use the VPC default",
			})
		}
	}

	return issues
}

// ─── Helpers ───

var nameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
var bucketRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func isValidResourceName(name string) bool { return nameRegex.MatchString(name) }
func isValidBucketName(name string) bool    { return bucketRegex.MatchString(name) }

func sanitizeName(name string) string {
	s := strings.ReplaceAll(name, "-", "_")
	s = regexp.MustCompile(`[^a-zA-Z0-9_]`).ReplaceAllString(s, "")
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		s = "r_" + s
	}
	if s == "" {
		s = "resource"
	}
	return s
}

func isValidCIDR(cidr string) bool {
	_, _, err := net.ParseCIDR(cidr)
	return err == nil
}

func cidrsOverlap(cidr1, cidr2 string) bool {
	_, net1, err1 := net.ParseCIDR(cidr1)
	_, net2, err2 := net.ParseCIDR(cidr2)
	if err1 != nil || err2 != nil {
		return false
	}
	return net1.Contains(net2.IP) || net2.Contains(net1.IP)
}

func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case float64:
		return int(val), true
	case int64:
		return int(val), true
	}
	return 0, false
}
