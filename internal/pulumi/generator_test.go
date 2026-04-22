package pulumi

import (
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/parser"
)

func sampleResources() []parser.Resource {
	return []parser.Resource{
		{
			ID: "aws_vpc.main", Type: "aws_vpc", Name: "main",
			Properties: map[string]any{"cidr_block": "10.0.0.0/16"},
		},
		{
			ID: "aws_s3_bucket.logs", Type: "aws_s3_bucket", Name: "logs",
			Properties: map[string]any{"bucket": "acme-logs", "versioning": map[string]any{"enabled": true}},
		},
	}
}

func TestGenerateProject_ProducesFullLayout(t *testing.T) {
	files, err := GenerateProject(ProjectConfig{
		Name:         "acme-infra",
		Environments: []string{"dev", "prod"},
		Resources:    sampleResources(),
	})
	if err != nil {
		t.Fatalf("GenerateProject: %v", err)
	}

	want := map[string]bool{
		"Pulumi.yaml":     false,
		"Pulumi.dev.yaml": false,
		"Pulumi.prod.yaml": false,
		"package.json":    false,
		"tsconfig.json":   false,
		"index.ts":        false,
		".gitignore":      false,
	}
	for _, f := range files {
		want[f.Path] = true
	}
	for path, ok := range want {
		if !ok {
			t.Errorf("missing expected file %q", path)
		}
	}
}

func TestGenerateProject_RejectsEmptyName(t *testing.T) {
	_, err := GenerateProject(ProjectConfig{Environments: []string{"dev"}, Resources: sampleResources()})
	if err == nil {
		t.Error("want error for empty Name")
	}
}

func TestGenerateProject_RejectsUnknownRuntime(t *testing.T) {
	_, err := GenerateProject(ProjectConfig{Name: "x", Runtime: "python", Resources: sampleResources()})
	if err == nil {
		t.Error("want error for non-nodejs runtime")
	}
}

func TestGenerateProject_DefaultsToDevEnvironmentWhenEmpty(t *testing.T) {
	files, err := GenerateProject(ProjectConfig{Name: "x", Resources: sampleResources()})
	if err != nil {
		t.Fatalf("GenerateProject: %v", err)
	}
	for _, f := range files {
		if f.Path == "Pulumi.dev.yaml" {
			return
		}
	}
	t.Error("expected implicit Pulumi.dev.yaml when Environments is empty")
}

func TestRenderProgram_ImportsMatchProviders(t *testing.T) {
	prog := renderProgram(ProjectConfig{
		Name: "acme", Resources: sampleResources(),
	})

	mustContain(t, prog, `import * as pulumi from "@pulumi/pulumi"`)
	mustContain(t, prog, `import * as aws from "@pulumi/aws"`)
	// No GCP / Azure resources → no corresponding imports.
	if strings.Contains(prog, `@pulumi/gcp`) {
		t.Error("program should not import gcp when no google_ resources are present")
	}
	if strings.Contains(prog, `@pulumi/azure-native`) {
		t.Error("program should not import azure-native when no azurerm_ resources are present")
	}
}

func TestRenderProgram_EmitsResourceConstructorsAndExports(t *testing.T) {
	prog := renderProgram(ProjectConfig{
		Name: "acme", Resources: sampleResources(),
	})
	mustContain(t, prog, `new aws.ec2.Vpc("main"`)
	mustContain(t, prog, `new aws.s3.Bucket("logs"`)
	mustContain(t, prog, `export const mainId = main.id;`)
	mustContain(t, prog, `export const logsId = logs.id;`)
	// Taggable AWS resources get the default Environment / ManagedBy
	// tags auto-injected so cost + policy enforcement work out of the
	// box.
	mustContain(t, prog, `tags: { Environment: environment, ManagedBy: "iac-studio" }`)
}

func TestRenderProgram_IsDeterministic(t *testing.T) {
	// Two runs with the same input must produce identical output —
	// critical for review diffs and CI caching.
	a := renderProgram(ProjectConfig{Name: "a", Resources: sampleResources()})
	b := renderProgram(ProjectConfig{Name: "a", Resources: sampleResources()})
	if a != b {
		t.Error("renderProgram is non-deterministic across runs")
	}
}

func TestRenderStackYaml_IncludesRegionPerProvider(t *testing.T) {
	// AWS resources → aws:region line.
	y := renderStackYaml(ProjectConfig{
		Name: "acme", Region: "eu-west-1", Resources: sampleResources(),
	}, "stage")
	mustContain(t, y, "aws:region: eu-west-1")
	mustContain(t, y, "acme:environment: stage")

	// GCP-only resources → gcp:region, no aws:region.
	yg := renderStackYaml(ProjectConfig{
		Name: "gcp-proj",
		Resources: []parser.Resource{
			{ID: "google_storage_bucket.x", Type: "google_storage_bucket", Name: "x"},
		},
	}, "dev")
	mustContain(t, yg, "gcp:region: us-central1")
	if strings.Contains(yg, "aws:region") {
		t.Errorf("aws:region should not appear in GCP-only stack yaml:\n%s", yg)
	}
}

func TestRenderPackageJSON_IncludesProviderSDKsInUse(t *testing.T) {
	p := renderPackageJSON(ProjectConfig{
		Name:      "acme",
		Resources: sampleResources(),
	})
	mustContain(t, p, `"@pulumi/pulumi"`)
	mustContain(t, p, `"@pulumi/aws"`)
	if strings.Contains(p, "@pulumi/gcp") || strings.Contains(p, "@pulumi/azure-native") {
		t.Error("package.json should only pull in SDKs we use")
	}
}

// ─── Helpers under test ─────────────────────────────────────────

func TestTerraformToPulumi_Overrides(t *testing.T) {
	cases := map[string]string{
		"aws_vpc":           "aws.ec2.Vpc",
		"aws_s3_bucket":     "aws.s3.Bucket",
		"aws_lambda_function": "aws.lambda.Function",
		"google_storage_bucket": "gcp.storage.Bucket",
		"azurerm_virtual_network": "azure.network.VirtualNetwork",
	}
	for tf, want := range cases {
		if got := terraformToPulumi(tf); got != want {
			t.Errorf("terraformToPulumi(%q) = %q, want %q", tf, got, want)
		}
	}
}

func TestTerraformToPulumi_FallbackCompiles(t *testing.T) {
	// Unknown type falls through to a best-effort guess. We don't
	// enforce an exact string — just that it produces a dotted
	// identifier that TS will parse (and the user can hand-fix if
	// needed).
	got := terraformToPulumi("aws_weirdthing_newservice")
	if !strings.Contains(got, ".") || !strings.HasPrefix(got, "aws.") {
		t.Errorf("unexpected fallback shape: %q", got)
	}
}

func TestTsPropValue_HandlesAllScalarTypes(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "undefined"},
		{true, "true"},
		{42, "42"},
		{3.14, "3.14"},
		{"hello", `"hello"`},
		{[]any{"a", 1, true}, `["a", 1, true]`},
		{map[string]any{"b": 2, "a": 1}, `{ "a": 1, "b": 2 }`}, // sorted keys
	}
	for _, tc := range cases {
		if got := tsPropValue(tc.in); got != tc.want {
			t.Errorf("tsPropValue(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestToCamelCase(t *testing.T) {
	cases := map[string]string{
		"web_server":      "webServer",
		"main":            "main",
		"cidr_block":      "cidrBlock",
		"multi_word_name": "multiWordName",
	}
	for in, want := range cases {
		if got := toCamelCase(in); got != want {
			t.Errorf("toCamelCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// mustContain fails the test when haystack doesn't contain needle;
// keeps the test bodies terse.
func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected %q in:\n%s", needle, haystack)
	}
}
