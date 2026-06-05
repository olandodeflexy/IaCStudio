package plan

import (
	"strings"
	"testing"
)

func TestClassifySafeTagUpdate(t *testing.T) {
	result := classifyPlanJSON(t, `{
		"resource_changes": [{
			"address": "aws_s3_bucket.logs",
			"type": "aws_s3_bucket",
			"name": "logs",
			"provider_name": "registry.terraform.io/hashicorp/aws",
			"change": {
				"actions": ["update"],
				"before": {"tags": {"Owner": "platform"}},
				"after": {"tags": {"Owner": "sre"}}
			}
		}]
	}`)

	assertSummary(t, result, RiskSafe, 1)
	if result.Summary.RequiresAcknowledgment {
		t.Fatal("safe tag-only update should not require acknowledgement")
	}
	assertReasonContains(t, result, "metadata")
}

func TestClassifySecurityGroupPublicIngressRisky(t *testing.T) {
	result := classifyPlanJSON(t, `{
		"resource_changes": [{
			"address": "aws_security_group.web",
			"type": "aws_security_group",
			"name": "web",
			"change": {
				"actions": ["update"],
				"before": {"ingress": []},
				"after": {"ingress": [{"from_port": 22, "to_port": 22, "cidr_blocks": ["0.0.0.0/0"]}]}
			}
		}]
	}`)

	assertSummary(t, result, RiskRisky, 1)
	assertRequiresAcknowledgment(t, result)
	assertCategory(t, result.Changes[0], "network_exposure")
	assertReasonContains(t, result, "public CIDR")
}

func TestClassifyIAMExpansionRisky(t *testing.T) {
	result := classifyPlanJSON(t, `{
		"resource_changes": [{
			"address": "aws_iam_policy.analytics",
			"type": "aws_iam_policy",
			"name": "analytics",
			"change": {
				"actions": ["update"],
				"before": {"policy": "{\"Statement\":[]}"},
				"after": {"policy": "{\"Statement\":[{\"Action\":\"s3:ListBucket\",\"Resource\":\"*\"}]}"}
			}
		}]
	}`)

	assertSummary(t, result, RiskRisky, 1)
	assertRequiresAcknowledgment(t, result)
	assertCategory(t, result.Changes[0], "identity")
	assertReasonContains(t, result, "permissions")
}

func TestClassifyRDSReplacementDestructive(t *testing.T) {
	result := classifyPlanJSON(t, `{
		"resource_changes": [{
			"address": "aws_db_instance.primary",
			"type": "aws_db_instance",
			"name": "primary",
			"change": {
				"actions": ["delete", "create"],
				"before": {"identifier": "prod-db", "allocated_storage": 100},
				"after": {"identifier": "prod-db", "allocated_storage": 500}
			}
		}]
	}`)

	assertSummary(t, result, RiskDestructive, 1)
	assertRequiresAcknowledgment(t, result)
	assertCategory(t, result.Changes[0], "data")
	assertReasonContains(t, result, "stateful data")
}

func TestClassifyDeleteDestructive(t *testing.T) {
	result := classifyPlanJSON(t, `{
		"resource_changes": [{
			"address": "aws_lambda_function.worker",
			"type": "aws_lambda_function",
			"name": "worker",
			"change": {
				"actions": ["delete"],
				"before": {"function_name": "worker"},
				"after": null
			}
		}]
	}`)

	assertSummary(t, result, RiskDestructive, 1)
	assertRequiresAcknowledgment(t, result)
	assertReasonContains(t, result, "remove existing infrastructure")
}

func TestClassifyUnknownAction(t *testing.T) {
	result := classifyPlanJSON(t, `{
		"resource_changes": [{
			"address": "example_service.main",
			"type": "example_service",
			"name": "main",
			"change": {
				"actions": ["rotate"],
				"before": {"name": "main"},
				"after": {"name": "main"}
			}
		}]
	}`)

	assertSummary(t, result, RiskUnknown, 1)
	assertRequiresAcknowledgment(t, result)
	assertReasonContains(t, result, "not recognized")
}

func classifyPlanJSON(t *testing.T, data string) *ClassificationResult {
	t.Helper()
	result, err := New().ClassifyFullPlan(data)
	if err != nil {
		t.Fatalf("classify plan JSON: %v", err)
	}
	return result
}

func assertSummary(t *testing.T, result *ClassificationResult, risk RiskLevel, want int) {
	t.Helper()
	if result.Summary.Total != want {
		t.Fatalf("summary total = %d, want %d", result.Summary.Total, want)
	}
	var got int
	switch risk {
	case RiskSafe:
		got = result.Summary.Safe
	case RiskRisky:
		got = result.Summary.Risky
	case RiskDestructive:
		got = result.Summary.Destructive
	case RiskUnknown:
		got = result.Summary.Unknown
	}
	if got != want {
		t.Fatalf("summary %s = %d, want %d: %#v", risk, got, want, result.Summary)
	}
	if len(result.Changes) != want {
		t.Fatalf("changes = %d, want %d", len(result.Changes), want)
	}
	if result.Changes[0].Risk != risk {
		t.Fatalf("risk = %s, want %s", result.Changes[0].Risk, risk)
	}
}

func assertRequiresAcknowledgment(t *testing.T, result *ClassificationResult) {
	t.Helper()
	if !result.Summary.RequiresAcknowledgment {
		t.Fatalf("expected acknowledgement requirement: %#v", result.Summary)
	}
}

func assertCategory(t *testing.T, change ClassifiedChange, want string) {
	t.Helper()
	for _, category := range change.Categories {
		if category == want {
			return
		}
	}
	t.Fatalf("categories %v do not contain %q", change.Categories, want)
}

func assertReasonContains(t *testing.T, result *ClassificationResult, want string) {
	t.Helper()
	if len(result.Changes) == 0 {
		t.Fatal("no changes returned")
	}
	if !strings.Contains(result.Changes[0].Reason, want) {
		t.Fatalf("reason %q does not contain %q", result.Changes[0].Reason, want)
	}
}
