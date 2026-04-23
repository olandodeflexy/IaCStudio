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
	// Unknown AWS/GCP/Azure types all fall through to the same
	// (<ns> as any).<pkg>.<Type> shape so the TS compiler accepts
	// them even when the guessed subpackage is wrong.
	cases := map[string]string{
		"aws_weirdthing_newservice":    "(aws as any).",
		"google_bogus_service":         "(gcp as any).",
		"azurerm_fictional_thing":      "(azure as any).",
	}
	for in, wantPrefix := range cases {
		got := terraformToPulumi(in)
		if !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("terraformToPulumi(%q) = %q, want prefix %q", in, got, wantPrefix)
		}
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
		if got := tsPropValue(tc.in, ""); got != tc.want {
			t.Errorf("tsPropValue(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderProgram_SanitizesHyphenatedProjectNames(t *testing.T) {
	// Resource names derived from hyphenated project names used to
	// produce `const acme-infraSeed = ...` which is a TS parse error.
	// Every identifier we emit must pass through sanitizeTSIdent.
	prog := renderProgram(ProjectConfig{
		Name: "acme-infra",
		Resources: []parser.Resource{{
			ID: "aws_s3_bucket.seed", Type: "aws_s3_bucket",
			Name:       "acme-infra_seed",
			Properties: map[string]any{"bucket": "x"},
		}},
	})
	// Hyphen replaced with underscore after camelCase; the identifier
	// is legal TS. We don't pin the exact form, just that no hyphen
	// appears in a `const` or `export const` identifier.
	for _, line := range strings.Split(prog, "\n") {
		if strings.HasPrefix(line, "const ") || strings.HasPrefix(line, "export const ") {
			// Everything between the keyword and "=" is the identifier
			// (with any type annotation); strip to that window.
			trimmed := strings.TrimPrefix(strings.TrimPrefix(line, "export "), "const ")
			if idx := strings.IndexByte(trimmed, '='); idx > 0 {
				trimmed = trimmed[:idx]
			}
			if strings.Contains(trimmed, "-") {
				t.Errorf("emitted hyphenated identifier: %q", line)
			}
		}
	}
}

func TestSanitizeTSIdent(t *testing.T) {
	cases := map[string]string{
		"webServer":      "webServer",
		"acme-infraSeed": "acme_infraSeed",
		"1_starts_digit": "_1_starts_digit",
		"name with space": "name_with_space",
		"":                "_",
	}
	for in, want := range cases {
		if got := sanitizeTSIdent(in); got != want {
			t.Errorf("sanitizeTSIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderStackYaml_PerProviderDefaults(t *testing.T) {
	// AWS + GCP resources with empty cfg.Region — each provider must
	// pick up its own default, not share the first one applied.
	y := renderStackYaml(ProjectConfig{
		Name: "mix",
		Resources: []parser.Resource{
			{ID: "aws_s3_bucket.a", Type: "aws_s3_bucket", Name: "a"},
			{ID: "google_storage_bucket.b", Type: "google_storage_bucket", Name: "b"},
		},
	}, "dev")
	mustContain(t, y, "aws:region: us-east-1")
	mustContain(t, y, "gcp:region: us-central1")
}

func TestRenderStackYaml_AzureHonorsCfgRegion(t *testing.T) {
	y := renderStackYaml(ProjectConfig{
		Name:   "x",
		Region: "EastUS2",
		Resources: []parser.Resource{
			{ID: "azurerm_resource_group.a", Type: "azurerm_resource_group", Name: "a"},
		},
	}, "dev")
	mustContain(t, y, "azure-native:location: EastUS2")
	// Azure default kicks in only when Region is empty.
	yDefault := renderStackYaml(ProjectConfig{
		Name: "x",
		Resources: []parser.Resource{
			{ID: "azurerm_resource_group.a", Type: "azurerm_resource_group", Name: "a"},
		},
	}, "dev")
	mustContain(t, yDefault, "azure-native:location: WestUS2")
}

func TestYamlScalar(t *testing.T) {
	cases := map[string]string{
		"simple":                  "simple",
		"":                        `""`,
		"has:colon":               `"has:colon"`,
		"has#hash":                `"has#hash"`,
		"with\nnewline":           `"with\nnewline"`,
		"true":                    `"true"`,   // reserved bool
		"null":                    `"null"`,   // reserved null
		" leadingspace":           `" leadingspace"`,
		"trailingspace ":          `"trailingspace "`,
		`has"quote`:               `"has\"quote"`,
		`back\slash`:              `"back\\slash"`,
	}
	for in, want := range cases {
		if got := yamlScalar(in); got != want {
			t.Errorf("yamlScalar(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderPulumiYaml_QuotesRiskyValues(t *testing.T) {
	// A description containing a colon must be quoted — otherwise YAML
	// would parse "description: note: see docs" as a mapping.
	y := renderPulumiYaml(ProjectConfig{
		Name:        "simple-name",
		Description: "note: see docs",
	})
	mustContain(t, y, `description: "note: see docs"`)
	mustContain(t, y, "name: simple-name") // plain scalar is fine
}

func TestIsTaggableAWS_IsAllowlist(t *testing.T) {
	// Unknown AWS types default to false now — previously they
	// defaulted to true and would auto-inject `tags:` blocks into
	// resources that don't accept them.
	if isTaggableAWS("aws_route_table_association") {
		t.Error("aws_route_table_association should not be taggable (unknown types default false)")
	}
	if isTaggableAWS("aws_iam_instance_profile") {
		t.Error("aws_iam_instance_profile should not be taggable")
	}
	// Known taggable types still return true.
	if !isTaggableAWS("aws_vpc") {
		t.Error("aws_vpc should be taggable")
	}
	if !isTaggableAWS("aws_s3_bucket") {
		t.Error("aws_s3_bucket should be taggable")
	}
}

func TestFallbackPulumiType_AzureUsesAsAnyCast(t *testing.T) {
	// (azure as any).<pkg>.<Type> parses cleanly in TS even when the
	// namespace is wrong — better than azure.unknown.<Type> which
	// hard-fails tsc at compile time.
	got := terraformToPulumi("azurerm_fictional_thing")
	if !strings.Contains(got, "(azure as any).") {
		t.Errorf("Azure fallback should use `as any` cast, got %q", got)
	}
}

func TestRenderProgram_DedupesSharedNamesAcrossTypes(t *testing.T) {
	// Two resources of different types share the name "main". Without
	// dedup we'd emit `const main = ...` twice (TS redeclaration
	// error). Output should carry mainVpc + mainSubnet (Type-derived
	// suffix) so the program compiles.
	prog := renderProgram(ProjectConfig{
		Name: "acme",
		Resources: []parser.Resource{
			{ID: "aws_vpc.main", Type: "aws_vpc", Name: "main",
				Properties: map[string]any{"cidr_block": "10.0.0.0/16"}},
			{ID: "aws_subnet.main", Type: "aws_subnet", Name: "main",
				Properties: map[string]any{"cidr_block": "10.0.1.0/24"}},
		},
	})
	mainCount := strings.Count(prog, "const main = ")
	if mainCount > 1 {
		t.Errorf("duplicate `const main` redeclaration — want at most 1, got %d\n%s", mainCount, prog)
	}
	// Second occurrence gets a type suffix so both resources end up
	// with distinct identifiers.
	if !strings.Contains(prog, "const main ") && !strings.Contains(prog, "const mainVpc ") {
		t.Error("neither main nor mainVpc emitted")
	}
}

func TestTsPropValue_CamelCasesNestedPropertyKeys(t *testing.T) {
	// Terraform-style nested block — keys should camelCase so Pulumi
	// sees 'networkAcl' + 'cidrBlocks' instead of the snake_case
	// forms (which would be unrecognised SDK properties).
	got := tsPropValue(map[string]any{
		"network_acl": map[string]any{
			"cidr_blocks": []any{"10.0.0.0/24"},
		},
	}, "ingress_rule")
	if !strings.Contains(got, `"networkAcl"`) {
		t.Errorf("expected camelCased 'networkAcl' in %q", got)
	}
	if !strings.Contains(got, `"cidrBlocks"`) {
		t.Errorf("expected camelCased 'cidrBlocks' in %q", got)
	}
}

func TestTsPropValue_PreservesTagAndLabelKeys(t *testing.T) {
	// tags/labels keys ARE the tag name — a camelCase rewrite would
	// silently change the infrastructure-level tag identifier.
	got := tsPropValue(map[string]any{
		"Owner":      "team_alpha",
		"ManagedBy":  "iac-studio",
		"cost_center": "platform",
	}, "tags")
	for _, key := range []string{`"Owner"`, `"ManagedBy"`, `"cost_center"`} {
		if !strings.Contains(got, key) {
			t.Errorf("tags key should be preserved: %q missing in %q", key, got)
		}
	}
	// And labels get the same treatment.
	gotLabels := tsPropValue(map[string]any{
		"k8s-app":    "web",
		"snake_case": "value",
	}, "labels")
	for _, key := range []string{`"k8s-app"`, `"snake_case"`} {
		if !strings.Contains(gotLabels, key) {
			t.Errorf("labels key should be preserved: %q missing in %q", key, gotLabels)
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
