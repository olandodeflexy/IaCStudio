package drift

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectClassifiesMetadataDriftAsLegitimate(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `{
		"version": 4,
		"resources": [{
			"mode": "managed",
			"type": "aws_s3_bucket",
			"name": "logs",
			"instances": [{"attributes": {"tags": {"Owner": "old"}}}]
		}]
	}`)

	report, err := New().Detect(dir, map[string]map[string]interface{}{
		"aws_s3_bucket.logs": {"tags": map[string]interface{}{"Owner": "platform"}},
	})
	if err != nil {
		t.Fatalf("detect drift: %v", err)
	}

	requireFinding(t, report, "aws_s3_bucket.logs", "tags", ClassificationLegitimateConfigChange, ActionCodifyOrAccept)
	if report.Classifications[ClassificationLegitimateConfigChange] != 1 {
		t.Fatalf("classification counts = %#v", report.Classifications)
	}
}

func TestDetectClassifiesSecurityGroupDriftAsUnauthorized(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `{
		"version": 4,
		"resources": [{
			"mode": "managed",
			"type": "aws_security_group",
			"name": "web",
			"instances": [{"attributes": {"ingress": [{"cidr_blocks": ["0.0.0.0/0"]}]}}]
		}]
	}`)

	report, err := New().Detect(dir, map[string]map[string]interface{}{
		"aws_security_group.web": {"ingress": []interface{}{}},
	})
	if err != nil {
		t.Fatalf("detect drift: %v", err)
	}

	requireFinding(t, report, "aws_security_group.web", "ingress", ClassificationUnauthorizedChange, ActionRevertOrCodifyAfterReview)
	if report.Findings[0].ExpectedValue == nil || report.Findings[0].CurrentValue == nil {
		t.Fatalf("finding should include expected/current values: %#v", report.Findings[0])
	}
}

func TestDetectReportsMissingAndUnmanagedResources(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `{
		"version": 4,
		"resources": [{
			"mode": "managed",
			"type": "aws_instance",
			"name": "manual",
			"instances": [{"attributes": {"instance_type": "t3.micro"}}]
		}]
	}`)

	report, err := New().Detect(dir, map[string]map[string]interface{}{
		"aws_s3_bucket.logs": {"bucket": "logs"},
	})
	if err != nil {
		t.Fatalf("detect drift: %v", err)
	}

	requireFinding(t, report, "aws_s3_bucket.logs", "", ClassificationMissingFromState, ActionImportOrApply)
	requireFinding(t, report, "aws_instance.manual", "", ClassificationUnauthorizedChange, ActionReviewImportOrRemove)
	if len(report.Missing) != 1 || len(report.Unmanaged) != 1 {
		t.Fatalf("missing/unmanaged = %#v/%#v", report.Missing, report.Unmanaged)
	}
}

func writeState(t *testing.T, dir, state string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(state), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func requireFinding(t *testing.T, report *DriftReport, address, path, classification, action string) {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.Address == address &&
			finding.Path == path &&
			finding.Classification == classification &&
			finding.RecommendedAction == action {
			return
		}
	}
	t.Fatalf("finding not found: address=%s path=%s classification=%s action=%s report=%#v",
		address, path, classification, action, report.Findings)
}
