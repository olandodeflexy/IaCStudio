package opa

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
)

const sampleRego = `package terraform.tags

deny[msg] {
  resource := input.resource_changes[_]
  resource.type == "aws_s3_bucket"
  not resource.change.after.tags.Owner
  msg := sprintf("bucket %q is missing the Owner tag", [resource.address])
}

warn[msg] {
  resource := input.resource_changes[_]
  resource.type == "aws_s3_bucket"
  not resource.change.after.tags.CostCenter
  msg := sprintf("bucket %q is missing the CostCenter tag", [resource.address])
}
`

const samplePlanJSON = `{
  "resource_changes": [
    {
      "address": "aws_s3_bucket.untagged",
      "type": "aws_s3_bucket",
      "change": {
        "actions": ["create"],
        "after": {
          "bucket": "untagged-bucket",
          "tags": {}
        }
      }
    },
    {
      "address": "aws_s3_bucket.tagged",
      "type": "aws_s3_bucket",
      "change": {
        "actions": ["create"],
        "after": {
          "bucket": "tagged-bucket",
          "tags": {"Owner": "platform", "CostCenter": "shared"}
        }
      }
    }
  ]
}`

// scaffoldProject lays out a tiny project tree with one Rego policy under
// policies/opa/. Returned path is the project root.
func scaffoldProject(t *testing.T, regoBody string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, PoliciesDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(regoBody), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return root
}

// TestOPAEngineEvaluatesDenyAndWarn verifies the adapter picks up both
// rule types from a single file, classifies severity correctly, and
// preserves the deny message verbatim.
func TestOPAEngineEvaluatesDenyAndWarn(t *testing.T) {
	root := scaffoldProject(t, sampleRego)
	e := New()
	res, err := e.Evaluate(context.Background(), engines.EvalInput{
		ProjectDir: root,
		PlanJSON:   []byte(samplePlanJSON),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Engine != "opa" || !res.Available {
		t.Errorf("metadata wrong: %+v", res)
	}
	// Expect 2 findings: untagged bucket fails deny (Owner missing), and
	// also fails warn (CostCenter missing). Tagged bucket passes both.
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(res.Findings), res.Findings)
	}
	// Verify severities and policy ids are filled in.
	severities := map[engines.Severity]int{}
	for _, f := range res.Findings {
		severities[f.Severity]++
		if f.PolicyID == "" || f.PolicyName == "" || f.Message == "" {
			t.Errorf("finding missing fields: %+v", f)
		}
		if f.PolicyFile == "" {
			t.Errorf("finding should record source policy file path: %+v", f)
		}
	}
	if severities[engines.SeverityError] != 1 {
		t.Errorf("expected one deny (error) finding, got: %v", severities)
	}
	if severities[engines.SeverityWarning] != 1 {
		t.Errorf("expected one warn finding, got: %v", severities)
	}
}

// TestOPAEngineNoPoliciesIsQuiet — when policies/opa/ doesn't exist or is
// empty, the engine returns a clean Result rather than an error so it
// doesn't pollute the multi-engine output for new projects.
func TestOPAEngineNoPoliciesIsQuiet(t *testing.T) {
	root := t.TempDir()
	res, err := New().Evaluate(context.Background(), engines.EvalInput{
		ProjectDir: root,
		PlanJSON:   []byte(samplePlanJSON),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Findings) != 0 || res.Error != "" {
		t.Errorf("expected quiet result, got: %+v", res)
	}
}

// TestOPAEngineRequiresPlanJSON guards the user-facing error message that
// nudges callers to run terraform plan first.
func TestOPAEngineRequiresPlanJSON(t *testing.T) {
	root := scaffoldProject(t, sampleRego)
	res, _ := New().Evaluate(context.Background(), engines.EvalInput{ProjectDir: root})
	if res.Error == "" {
		t.Error("expected an Error explaining plan JSON requirement")
	}
}

// TestOPAEngineRejectsBadPlanJSON confirms a malformed plan surfaces the
// underlying parse error rather than crashing the engine.
func TestOPAEngineRejectsBadPlanJSON(t *testing.T) {
	root := scaffoldProject(t, sampleRego)
	_, err := New().Evaluate(context.Background(), engines.EvalInput{
		ProjectDir: root,
		PlanJSON:   []byte(`{"oops": [`),
	})
	if err == nil {
		t.Error("expected an error from malformed plan JSON")
	}
}

// TestReadPackageNameRejectsMissingDecl protects against the helper
// silently substituting "main" for a malformed Rego file.
func TestReadPackageNameRejectsMissingDecl(t *testing.T) {
	_, err := readPackageName([]byte("// no package here\ndeny[msg] { msg := \"x\" }"))
	if err == nil {
		t.Error("expected error for source without package declaration")
	}
}

// TestReadPackageNameSupportsDottedPackages is the common case (Conftest's
// "terraform.tags" style).
func TestReadPackageNameSupportsDottedPackages(t *testing.T) {
	got, err := readPackageName([]byte("package terraform.tags.s3\n\ndeny[msg] { false }"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "terraform.tags.s3" {
		t.Errorf("got %q, want terraform.tags.s3", got)
	}
}
