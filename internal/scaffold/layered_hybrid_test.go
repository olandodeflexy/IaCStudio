package scaffold

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLayeredHybridRendersPerEnvironmentTools(t *testing.T) {
	bp := &LayeredHybridBlueprint{}
	files, err := bp.Render(map[string]any{
		"project_name":        "acme",
		"cloud":               "aws",
		"environments":        []any{"dev", "prod"},
		"pulumi_environments": []any{"dev"},
		"modules":             []any{"networking"},
		"backend":             "s3",
		"state_bucket":        "acme-tfstate",
		"state_region":        "us-east-1",
		"owner_tag":           "platform",
		"cost_center_tag":     "shared",
	})
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	got := map[string]File{}
	for _, file := range files {
		got[file.Path] = file
	}
	for _, path := range []string{
		".iac-studio.json",
		"environments/dev/Pulumi.yaml",
		"environments/dev/index.ts",
		"environments/prod/main.tf",
		"environments/prod/backend.tf",
		"modules/networking/main.tf",
		"policies/crossguard/PulumiPolicy.yaml",
		"policies/opa/resource-tagging.rego",
		"scripts/plan.sh",
	} {
		if _, ok := got[path]; !ok {
			t.Fatalf("expected file missing from render output: %s", path)
		}
	}
	for _, path := range []string{
		"environments/dev/main.tf",
		"environments/prod/Pulumi.yaml",
	} {
		if _, ok := got[path]; ok {
			t.Fatalf("unexpected file rendered for wrong env tool: %s", path)
		}
	}

	var descriptor struct {
		Tool             string            `json:"tool"`
		Blueprint        string            `json:"blueprint"`
		Layout           string            `json:"layout"`
		EnvironmentTools map[string]string `json:"environment_tools"`
	}
	if err := json.Unmarshal(got[".iac-studio.json"].Content, &descriptor); err != nil {
		t.Fatalf("descriptor json: %v", err)
	}
	if descriptor.Tool != "multi" || descriptor.Blueprint != "layered-hybrid" || descriptor.Layout != "layered-v1" {
		t.Fatalf("unexpected descriptor header: %+v", descriptor)
	}
	if descriptor.EnvironmentTools["dev"] != "pulumi" || descriptor.EnvironmentTools["prod"] != "terraform" {
		t.Fatalf("unexpected environment tool map: %+v", descriptor.EnvironmentTools)
	}
	if !strings.Contains(string(got["scripts/plan.sh"].Content), "Pulumi.yaml") {
		t.Fatalf("hybrid plan script should detect Pulumi envs:\n%s", string(got["scripts/plan.sh"].Content))
	}
}

func TestLayeredHybridRejectsPulumiEnvironmentOutsideSelectedEnvs(t *testing.T) {
	bp := &LayeredHybridBlueprint{}
	_, err := bp.Render(map[string]any{
		"project_name":        "acme",
		"environments":        []any{"prod"},
		"pulumi_environments": []any{"dev"},
	})
	if err == nil {
		t.Fatal("expected mismatch validation error, got nil")
	}
}

func TestLayeredHybridRejectsInvalidPulumiGCPRegion(t *testing.T) {
	bp := &LayeredHybridBlueprint{}
	_, err := bp.Render(map[string]any{
		"project_name":        "acme",
		"cloud":               "gcp",
		"environments":        []any{"dev"},
		"pulumi_environments": []any{"dev"},
		"region":              "us-east-1",
	})
	if err == nil {
		t.Fatal("expected GCP region validation error, got nil")
	}
}

func TestLayeredHybridAllPulumiSkipsTerraformOnlyInputsAndDocs(t *testing.T) {
	bp := &LayeredHybridBlueprint{}
	files, err := bp.Render(map[string]any{
		"project_name":        "acme",
		"cloud":               "aws",
		"environments":        []any{"dev", "prod"},
		"pulumi_environments": []any{"dev", "prod"},
		"modules":             []any{"../unused"},
		"backend":             "azurerm",
		"state_bucket":        "invalid-hyphenated-azure-name",
		"state_region":        "not a region",
	})
	if err != nil {
		t.Fatalf("all-Pulumi hybrid render should ignore unused Terraform inputs, got: %v", err)
	}

	got := map[string]File{}
	for _, file := range files {
		got[file.Path] = file
	}
	for _, path := range []string{
		"environments/dev/Pulumi.yaml",
		"environments/prod/Pulumi.yaml",
		"policies/crossguard/PulumiPolicy.yaml",
	} {
		if _, ok := got[path]; !ok {
			t.Fatalf("expected all-Pulumi file missing: %s", path)
		}
	}
	for _, path := range []string{
		"environments/dev/backend.tf",
		"modules/networking/main.tf",
		"policies/opa/resource-tagging.rego",
		"policies/sentinel/cost-control.sentinel",
	} {
		if _, ok := got[path]; ok {
			t.Fatalf("unexpected Terraform-only file rendered for all-Pulumi hybrid: %s", path)
		}
	}

	var descriptor struct {
		Modules []string `json:"modules"`
	}
	if err := json.Unmarshal(got[".iac-studio.json"].Content, &descriptor); err != nil {
		t.Fatalf("descriptor json: %v", err)
	}
	if len(descriptor.Modules) != 0 {
		t.Fatalf("all-Pulumi descriptor should omit Terraform modules, got %+v", descriptor.Modules)
	}

	readme := string(got["README.md"].Content)
	for _, unexpected := range []string{"`modules/`", "OPA/Sentinel", "policies/opa", "policies/sentinel"} {
		if strings.Contains(readme, unexpected) {
			t.Fatalf("all-Pulumi README should not mention %q:\n%s", unexpected, readme)
		}
	}
	if !strings.Contains(readme, "policies/crossguard") {
		t.Fatalf("all-Pulumi README should mention CrossGuard policies:\n%s", readme)
	}
}

func TestLayeredHybridValidatesTerraformBackendWhenTerraformEnvExists(t *testing.T) {
	bp := &LayeredHybridBlueprint{}
	_, err := bp.Render(map[string]any{
		"project_name":        "acme",
		"environments":        []any{"dev", "prod"},
		"pulumi_environments": []any{"dev"},
		"backend":             "azurerm",
		"state_bucket":        "invalid-hyphenated-azure-name",
	})
	if err == nil {
		t.Fatal("expected Terraform backend validation error, got nil")
	}
	if !strings.Contains(err.Error(), "state_bucket") {
		t.Fatalf("expected state_bucket validation error, got: %v", err)
	}
}

func TestLayeredHybridRegisteredInDefaultRegistry(t *testing.T) {
	bp, ok := Default.Get("layered-hybrid")
	if !ok {
		t.Fatal("layered-hybrid blueprint not registered in Default registry")
	}
	if bp.Tool() != "multi" {
		t.Fatalf("registered blueprint Tool() = %q, want multi", bp.Tool())
	}
}
