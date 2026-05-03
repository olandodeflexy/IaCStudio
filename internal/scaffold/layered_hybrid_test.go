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

func TestLayeredHybridRegisteredInDefaultRegistry(t *testing.T) {
	bp, ok := Default.Get("layered-hybrid")
	if !ok {
		t.Fatal("layered-hybrid blueprint not registered in Default registry")
	}
	if bp.Tool() != "multi" {
		t.Fatalf("registered blueprint Tool() = %q, want multi", bp.Tool())
	}
}
