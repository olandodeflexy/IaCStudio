package scaffold

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/pulumi"
)

// gcpRegionRE matches Google Cloud's canonical region shape —
// lowercase alphabetic segments separated by hyphens, ending in a
// digit suffix. Covers us-central1, europe-west2, asia-northeast3,
// AND the longer forms like northamerica-northeast1 / southamerica-
// east1. Deliberately excludes zones (us-central1-a, trailing -a/b/c)
// since GCS bucket locations take regions, not zones.
var gcpRegionRE = regexp.MustCompile(`^[a-z]+(-[a-z]+)+[0-9]+$`)

// tagValueRE is a tool-agnostic accept pattern for free-form tag/
// label values. Allows letters, digits, spaces, _.:/=+-@ — the
// Terraform HCL-safe set without emphasising HCL in the error
// message. Kept separate from validateHCLSafeValue (which exists in
// the terraform blueprint with terraform-specific wording) so Pulumi
// users don't see "generated HCL" hints when a value fails.
var tagValueRE = regexp.MustCompile(`^[A-Za-z0-9 _.:/=+\-@]{1,128}$`)

func validateTagValue(key, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", key)
	}
	if !tagValueRE.MatchString(value) {
		return fmt.Errorf("%s %q is invalid: use letters, digits, spaces, and _.:/=+-@ (≤128 chars)", key, value)
	}
	return nil
}

// truncateForBucketName clamps a base name to max chars while keeping
// the name valid as an S3/GCS bucket (must end in an alphanumeric,
// no trailing '-' or '_'). Used when composing `<project>-seed` +
// `<project>-<env>` so long project names don't blow the 63-char
// provider cap.
func truncateForBucketName(base string, max int) string {
	if len(base) <= max {
		return base
	}
	out := base[:max]
	for len(out) > 0 {
		last := out[len(out)-1]
		if (last >= 'a' && last <= 'z') || (last >= '0' && last <= '9') {
			break
		}
		out = out[:len(out)-1]
	}
	return out
}

// sanitizeGCPLabel coerces an arbitrary value to the GCP label
// charset. Lowercases, replaces anything outside [a-z0-9_-] with "_",
// and clamps to 63 chars. Empty input returns "_" so the provider
// accepts it (GCP rejects empty label values).
func sanitizeGCPLabel(v string) string {
	v = strings.ToLower(v)
	var b strings.Builder
	for _, r := range v {
		switch {
		case (r >= 'a' && r <= 'z'), (r >= '0' && r <= '9'), r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_"
	}
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

// LayeredPulumiBlueprint renders a multi-environment Pulumi
// TypeScript project mirroring the layered-terraform structure:
//
//	environments/{env}/{index.ts, Pulumi.yaml, Pulumi.<env>.yaml,
//	                    package.json, tsconfig.json, .gitignore}
//	scripts/{init,plan,apply,destroy}.sh
//	README.md, .iac-studio.json, .gitignore
//
// Each environment is a self-contained Pulumi project — same pattern as
// Terraform's per-env root directory. True dual-stack (some envs on
// Terraform, others on Pulumi in the same project) is a follow-up that
// extends this blueprint with a per-env tool selector. For now the
// user picks Pulumi OR Terraform at project creation time.
//
// Reusable modules aren't emitted today: in Pulumi those are
// ComponentResources rather than separate directory scaffolds, so
// they belong in a later commit that ships a component helper library.
type LayeredPulumiBlueprint struct{}

func (b *LayeredPulumiBlueprint) ID() string          { return "layered-pulumi" }
func (b *LayeredPulumiBlueprint) Name() string        { return "Layered Pulumi (TypeScript)" }
func (b *LayeredPulumiBlueprint) Description() string {
	return "Multi-environment Pulumi TypeScript project with per-stack configs and lifecycle scripts. Parallel to the layered-terraform blueprint but uses Pulumi instead of HCL."
}
func (b *LayeredPulumiBlueprint) Tool() string { return "pulumi" }

func (b *LayeredPulumiBlueprint) Inputs() []Input {
	return []Input{
		{Key: "project_name", Label: "Project name", Type: "string", Required: true},
		{Key: "cloud", Label: "Primary cloud provider", Type: "select",
			Options: []string{"aws", "gcp", "azure"}, Default: "aws", Required: true},
		{Key: "environments", Label: "Environments", Type: "multiselect",
			Options: []string{"dev", "staging", "prod"}, Default: []string{"dev", "prod"}},
		{Key: "region", Label: "Primary region / location", Type: "string", Default: "",
			Description: "Baked into each Pulumi.<env>.yaml using the cloud-specific config key: aws:region / gcp:region / azure-native:location. Leave empty to use the per-cloud default (us-east-1 for AWS, us-central1 for GCP, WestUS2 for Azure)."},
		{Key: "owner_tag", Label: "Owner tag", Type: "string", Default: "platform"},
	}
}

func (b *LayeredPulumiBlueprint) Render(values map[string]any) ([]File, error) {
	name := stringInput(values, "project_name", "")
	if err := validateSafeName("project_name", name); err != nil {
		return nil, err
	}
	cloud := stringInput(values, "cloud", "aws")
	switch cloud {
	case "aws", "gcp", "azure":
	default:
		return nil, fmt.Errorf("cloud %q is unsupported: must be one of aws, gcp, azure", cloud)
	}
	envs := stringSliceInput(values, "environments", []string{"dev", "prod"})
	for _, env := range envs {
		if err := validatePathSegment("environment", env); err != nil {
			return nil, err
		}
	}
	// Default is empty — per-cloud fallback happens inside Pulumi's
	// generator (aws: us-east-1, gcp: us-central1, azure: WestUS2).
	// Only substitute a cloud-appropriate default at scaffold time so
	// the seed resource locations match what ends up in Pulumi.<env>.yaml.
	region := stringInput(values, "region", "")
	if region == "" {
		switch cloud {
		case "gcp":
			region = "us-central1"
		case "azure":
			region = "WestUS2"
		default:
			region = "us-east-1"
		}
	}
	owner := stringInput(values, "owner_tag", "platform")
	if err := validateTagValue("owner_tag", owner); err != nil {
		return nil, err
	}

	var files []File
	for _, env := range envs {
		// Pulumi project names must stay within 100 chars (validated
		// by pulumi.ValidateProjectName) but the canonical Pulumi docs
		// recommend keeping them short. Clamp base to leave room for
		// the "-<env>" suffix. Env is at most 32 chars (scaffold-level
		// validation) so a 60-char base leaves comfortable margin.
		const maxBase = 60
		projName := truncateForBucketName(name, maxBase) + "-" + env

		// Seed resources are generated PER-ENV so cloud-global names
		// (S3 / GCS buckets, Azure resource groups) stay unique across
		// stacks in the same account. A shared <project>-seed bucket
		// would make `pulumi up` in prod collide with dev.
		seed := seedResourcesFor(cloud, name, env, owner, region)

		proj := pulumi.ProjectConfig{
			Name:         projName,
			Description:  fmt.Sprintf("%s — %s environment (Pulumi)", name, env),
			Environments: []string{env},
			Region:       region,
			Resources:    seed,
		}
		emitted, err := pulumi.GenerateProject(proj)
		if err != nil {
			return files, fmt.Errorf("generate pulumi project for env %q: %w", env, err)
		}
		for _, f := range emitted {
			files = append(files, File{
				Path:    fmt.Sprintf("environments/%s/%s", env, f.Path),
				Content: f.Content,
			})
		}
	}

	// Top-level docs + lifecycle scripts. Scripts wrap the Pulumi CLI
	// so a user can run `./scripts/plan.sh dev` without memorising the
	// per-env cd dance.
	files = append(files, File{
		Path:    "README.md",
		Content: []byte(renderPulumiProjectReadme(name, cloud, envs)),
	})
	descriptor, err := json.MarshalIndent(map[string]any{
		"tool":         "pulumi",
		"blueprint":    "layered-pulumi",
		"project_name": name,
		"cloud":        cloud,
		"environments": envs,
		"layout":       "layered-v1",
	}, "", "  ")
	if err != nil {
		return files, fmt.Errorf("marshal .iac-studio.json: %w", err)
	}
	files = append(files, File{
		Path:    ".iac-studio.json",
		Content: append(descriptor, '\n'),
	})
	files = append(files, pulumiLifecycleScripts()...)
	files = append(files, File{
		Path:    ".gitignore",
		Content: []byte("node_modules/\nbin/\n*.log\n*.tsbuildinfo\n.pulumi/\n"),
	})

	return files, nil
}

// seedResourcesFor returns one token resource per supported cloud so
// the scaffolded program is runnable out of the box. The canvas layer
// replaces these as the user designs real infrastructure; without a
// seed, `pulumi preview` would succeed with zero resources which
// looks like an empty project and misleads new users.
//
// env is suffixed to every cloud-global name (S3/GCS bucket, Azure
// resource group) so running `pulumi up` in multiple stacks from the
// same account doesn't collide on a shared "<project>-seed" identifier
// — dev apply and prod apply must produce distinct cloud resources.
//
// owner is stamped onto the resource's tags (or GCP-native labels)
// so the scaffold's owner_tag input isn't a decorative no-op. region
// drives the seed's location value too — the stack config already
// carries it, and forcing a hardcoded "US" / "WestUS2" would create a
// confusing mismatch between stack yaml and resource location.
func seedResourcesFor(cloud, projectName, env, owner, region string) []parser.Resource {
	// AWS/Azure accept PascalCase tag keys; GCP labels must be
	// lowercase [a-z0-9_-] so values get sanitised rather than just
	// lowercased — "Team A" → "team_a" so the provider doesn't reject
	// at apply time.
	awsAzureTags := map[string]any{"Owner": owner, "ManagedBy": "iac-studio", "Environment": env}
	gcpLabels := map[string]any{
		"owner":       sanitizeGCPLabel(owner),
		"managed_by":  "iac-studio",
		"environment": sanitizeGCPLabel(env),
	}
	// Bucket names are capped at 63 chars across S3 and GCS. Leave
	// room for the "-<env>-seed" suffix so long project names plus
	// long env names don't produce invalid bucket names at apply time.
	suffix := "-" + env + "-seed"
	maxBucketBase := 63 - len(suffix)
	if maxBucketBase < 3 {
		maxBucketBase = 3 // S3 bucket-name floor
	}
	bucketBase := truncateForBucketName(projectName, maxBucketBase)
	switch cloud {
	case "aws":
		return []parser.Resource{{
			ID:   "aws_s3_bucket.seed",
			Type: "aws_s3_bucket",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"bucket": bucketBase + suffix,
				"tags":   awsAzureTags,
			},
		}}
	case "gcp":
		// GCS accepts both canonical regions (us-central1, europe-
		// west1) and short multi-regions (US, EU, ASIA). Pass the
		// user's value through when it looks like a canonical GCP
		// region, uppercase it when it's already a short multi-
		// region, and fall back to US for AWS-shaped inputs
		// (us-east-1, us-west-2) or empty.
		loc := "US"
		switch {
		case region == "":
			// fall through to default
		case gcpRegionRE.MatchString(region):
			loc = region
		case len(region) <= 4 && region == strings.ToUpper(region):
			// Short multi-region as-is (US, EU, ASIA).
			loc = region
		case len(region) <= 4:
			loc = strings.ToUpper(region)
		}
		return []parser.Resource{{
			ID:   "google_storage_bucket.seed",
			Type: "google_storage_bucket",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"name":     bucketBase + suffix,
				"location": loc,
				"labels":   gcpLabels,
			},
		}}
	case "azure":
		loc := region
		if loc == "" {
			loc = "WestUS2"
		}
		// Resource-group names are scoped per-subscription and capped
		// at 90 chars. Env-scope the name so dev + prod can coexist.
		rgName := projectName + "-" + env + "-rg"
		return []parser.Resource{{
			ID:   "azurerm_resource_group.seed",
			Type: "azurerm_resource_group",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"name":     rgName,
				"location": loc,
				"tags":     awsAzureTags,
			},
		}}
	}
	return nil
}

// pulumiLifecycleScripts emits scripts/{init,plan,apply,destroy}.sh
// that wrap the Pulumi CLI with the per-env cd boilerplate. Matches
// the shape of the layered-terraform scripts so muscle memory carries
// over between tools.
func pulumiLifecycleScripts() []File {
	initSh := `#!/usr/bin/env bash
set -euo pipefail
# Installs node_modules for every environment project. Run once after
# cloning. Requires: pulumi, node, npm.
#
# Note: this wrapper performs a per-environment install for layered
# projects. The SafeRunner's 'init' command for Pulumi only runs 'npm
# install' in the project root (suitable for flat Pulumi layouts);
# layered users should run this script directly for now.
cd "$(dirname "$0")/.."
for dir in environments/*/; do
  echo "→ npm install in $dir"
  (cd "$dir" && npm install)
done
`
	planSh := `#!/usr/bin/env bash
set -euo pipefail
# Usage: ./scripts/plan.sh <env>
env="${1:?env required — usage: ./scripts/plan.sh <env>}"
cd "$(dirname "$0")/../environments/$env"
pulumi preview --non-interactive --color=never
`
	applySh := `#!/usr/bin/env bash
set -euo pipefail
# Usage: ./scripts/apply.sh <env>
env="${1:?env required — usage: ./scripts/apply.sh <env>}"
cd "$(dirname "$0")/../environments/$env"
pulumi up --yes --non-interactive --color=never
`
	destroySh := `#!/usr/bin/env bash
set -euo pipefail
# Usage: ./scripts/destroy.sh <env>
env="${1:?env required — usage: ./scripts/destroy.sh <env>}"
cd "$(dirname "$0")/../environments/$env"
pulumi destroy --yes --non-interactive --color=never
`
	return []File{
		{Path: "scripts/init.sh", Content: []byte(initSh), Executable: true},
		{Path: "scripts/plan.sh", Content: []byte(planSh), Executable: true},
		{Path: "scripts/apply.sh", Content: []byte(applySh), Executable: true},
		{Path: "scripts/destroy.sh", Content: []byte(destroySh), Executable: true},
	}
}

func renderPulumiProjectReadme(name, cloud string, envs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", name)
	fmt.Fprintf(&b, "Pulumi TypeScript project scaffolded by IaC Studio, targeting **%s**.\n\n", cloud)
	b.WriteString("## Layout\n\n")
	b.WriteString("- `environments/{env}/` — one self-contained Pulumi project per environment.\n")
	b.WriteString("- `scripts/{init,plan,apply,destroy}.sh` — lifecycle wrappers that cd into the selected environment and run the Pulumi CLI.\n\n")
	b.WriteString("## Bootstrap\n\n")
	b.WriteString("```bash\n")
	b.WriteString("pulumi login                     # one-time — pick a backend (local / Pulumi Cloud)\n")
	b.WriteString("./scripts/init.sh                # npm install in each environments/<env>\n")
	for _, env := range envs {
		// `pulumi stack init` reads Pulumi.yaml from the working dir,
		// which under layered-pulumi lives at environments/<env>/,
		// not the repo root. Using --cwd keeps the commands runnable
		// as-is from the README without a per-step cd.
		fmt.Fprintf(&b, "pulumi --cwd environments/%s stack init %s   # once per env — creates the Pulumi stack\n", env, env)
	}
	b.WriteString("```\n\n")
	b.WriteString("## Daily use\n\n")
	b.WriteString("```bash\n")
	b.WriteString("./scripts/plan.sh dev            # pulumi preview in environments/dev\n")
	b.WriteString("./scripts/apply.sh dev           # pulumi up --yes\n")
	b.WriteString("```\n")
	return b.String()
}

func init() {
	Default.Register(&LayeredPulumiBlueprint{})
}
