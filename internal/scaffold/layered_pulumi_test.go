package scaffold

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLayeredPulumi_RendersPerEnvProject(t *testing.T) {
	bp := &LayeredPulumiBlueprint{}
	files, err := bp.Render(map[string]any{
		"project_name": "acme-infra",
		"cloud":        "aws",
		"environments": []string{"dev", "prod"},
		"region":       "eu-west-1",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Every env gets its own Pulumi project files.
	mustHave := []string{
		"environments/dev/Pulumi.yaml",
		"environments/dev/Pulumi.dev.yaml",
		"environments/dev/index.ts",
		"environments/dev/package.json",
		"environments/prod/Pulumi.yaml",
		"environments/prod/Pulumi.prod.yaml",
		"environments/prod/index.ts",
		"policies/crossguard/PulumiPolicy.yaml",
		"policies/crossguard/index.ts",
		"policies/crossguard/package.json",
		"policies/crossguard/tsconfig.json",
		"scripts/init.sh",
		"scripts/plan.sh",
		"scripts/apply.sh",
		"scripts/destroy.sh",
		"README.md",
		".iac-studio.json",
		".gitignore",
	}
	present := map[string]bool{}
	for _, f := range files {
		present[filepath.ToSlash(f.Path)] = true
	}
	for _, path := range mustHave {
		if !present[path] {
			t.Errorf("missing expected file %q", path)
		}
	}
}

func TestLayeredPulumi_ProjectDescriptorCarriesLayoutHint(t *testing.T) {
	bp := &LayeredPulumiBlueprint{}
	files, err := bp.Render(map[string]any{
		"project_name": "acme",
		"cloud":        "gcp",
		"environments": []string{"dev"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// .iac-studio.json should carry the tool + layered-v1 marker so
	// the frontend swimlane view picks the right classifier.
	var descriptor string
	for _, f := range files {
		if f.Path == ".iac-studio.json" {
			descriptor = string(f.Content)
		}
	}
	if descriptor == "" {
		t.Fatal(".iac-studio.json not emitted")
	}
	for _, want := range []string{
		`"tool": "pulumi"`,
		`"blueprint": "layered-pulumi"`,
		`"layout": "layered-v1"`,
		`"cloud": "gcp"`,
	} {
		if !strings.Contains(descriptor, want) {
			t.Errorf("descriptor missing %q in:\n%s", want, descriptor)
		}
	}
}

func TestLayeredPulumi_SeedResourceMatchesCloud(t *testing.T) {
	cases := []struct {
		cloud       string
		wantInIndex string
	}{
		{"aws", "aws.s3.Bucket"},
		{"gcp", "gcp.storage.Bucket"},
		{"azure", "azure.resources.ResourceGroup"},
	}
	for _, tc := range cases {
		t.Run(tc.cloud, func(t *testing.T) {
			bp := &LayeredPulumiBlueprint{}
			files, err := bp.Render(map[string]any{
				"project_name": "seed-test",
				"cloud":        tc.cloud,
				"environments": []string{"dev"},
			})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			for _, f := range files {
				if f.Path == "environments/dev/index.ts" {
					if !strings.Contains(string(f.Content), tc.wantInIndex) {
						t.Errorf("index.ts for %s missing %q:\n%s", tc.cloud, tc.wantInIndex, f.Content)
					}
					return
				}
			}
			t.Error("environments/dev/index.ts not emitted")
		})
	}
}

func TestLayeredPulumi_RejectsBadName(t *testing.T) {
	bp := &LayeredPulumiBlueprint{}
	_, err := bp.Render(map[string]any{
		"project_name": "Bad NAME!!",
		"cloud":        "aws",
		"environments": []string{"dev"},
	})
	if err == nil {
		t.Error("expected validation error for bad project name")
	}
}

func TestLayeredPulumi_LifecycleScriptsAreExecutable(t *testing.T) {
	bp := &LayeredPulumiBlueprint{}
	files, err := bp.Render(map[string]any{
		"project_name": "acme",
		"cloud":        "aws",
		"environments": []string{"dev"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	execBits := map[string]bool{}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "scripts/") {
			execBits[f.Path] = f.Executable
		}
	}
	for path, isExec := range execBits {
		if !isExec {
			t.Errorf("%s should be executable", path)
		}
	}
	if len(execBits) < 4 {
		t.Errorf("want ≥ 4 lifecycle scripts, got %d", len(execBits))
	}
}

func TestLayeredPulumi_RegisteredInDefaultRegistry(t *testing.T) {
	// Default registers both layered-terraform and layered-pulumi at
	// init time — the API layer lists all Blueprints via the registry.
	found := false
	for _, bp := range Default.List() {
		if bp.ID() == "layered-pulumi" {
			found = true
			if bp.Tool() != "pulumi" {
				t.Errorf("registered blueprint Tool() = %q, want 'pulumi'", bp.Tool())
			}
		}
	}
	if !found {
		t.Error("layered-pulumi not registered in Default registry")
	}
}
