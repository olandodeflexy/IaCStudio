package ai

import (
	"testing"
)

func TestPatternMatch_Terraform(t *testing.T) {
	tests := []struct {
		input    string
		wantType string
		wantMsg  bool
	}{
		{"add a vpc", "aws_vpc", true},
		{"create an ec2 instance", "aws_instance", true},
		{"I need an s3 bucket", "aws_s3_bucket", true},
		{"add a database", "aws_rds_instance", true},
		{"create a lambda function", "aws_lambda_function", true},
		{"add a security group", "aws_security_group", true},
		{"something random", "", true},
	}

	for _, tt := range tests {
		msg, resources := PatternMatch(tt.input, "terraform", "aws")
		if msg == "" {
			t.Errorf("PatternMatch(%q) returned empty message", tt.input)
		}
		if tt.wantType == "" {
			if len(resources) != 0 {
				t.Errorf("PatternMatch(%q) returned resources for unknown input", tt.input)
			}
			continue
		}
		if len(resources) == 0 {
			t.Errorf("PatternMatch(%q) returned no resources, want %s", tt.input, tt.wantType)
			continue
		}
		if resources[0].Type != tt.wantType {
			t.Errorf("PatternMatch(%q) type = %s, want %s", tt.input, resources[0].Type, tt.wantType)
		}
		if resources[0].ID == "" {
			t.Errorf("PatternMatch(%q) returned resource with empty ID", tt.input)
		}
	}
}

func TestPatternMatch_Ansible(t *testing.T) {
	tests := []struct {
		input    string
		wantType string
	}{
		{"install nginx", "apt"},
		{"add a docker container", "docker_container"},
		{"create a user account", "user"},
		{"copy a file", "copy"},
	}

	for _, tt := range tests {
		_, resources := PatternMatch(tt.input, "ansible", "")
		if len(resources) == 0 {
			t.Errorf("PatternMatch(%q, ansible) returned no resources", tt.input)
			continue
		}
		if resources[0].Type != tt.wantType {
			t.Errorf("PatternMatch(%q, ansible) type = %s, want %s", tt.input, resources[0].Type, tt.wantType)
		}
	}
}

func TestPatternMatch_ResourceProperties(t *testing.T) {
	_, resources := PatternMatch("add a vpc", "terraform", "aws")
	if len(resources) == 0 {
		t.Fatal("Expected a resource")
	}

	vpc := resources[0]
	if vpc.Properties == nil {
		t.Fatal("Properties should not be nil")
	}

	cidr, ok := vpc.Properties["cidr_block"]
	if !ok {
		t.Error("Missing cidr_block property")
	}
	if cidr != "10.0.0.0/16" {
		t.Errorf("cidr_block = %v, want 10.0.0.0/16", cidr)
	}
}
