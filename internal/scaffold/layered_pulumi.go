package scaffold

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/pulumi"
)

// tagValueRE is a tool-agnostic accept pattern for free-form tag/
// label values. Allows letters, digits, spaces, _.:/=+-@ — the
// Terraform HCL-safe set without emphasising HCL in the error
// message. Kept separate from validateHCLSafeValue (which exists in
// the terraform blueprint with terraform-specific wording) so Pulumi
// users don't see "generated HCL" hints when a value fails.
var tagValueRE = regexp.MustCompile(`^[A-Za-z0-9 _.:/=+\-@]{1,128}$`)

// gcpLabelRE tightens tag validation for GCP labels: values must be
// [a-z0-9_-], ≤63 chars, non-empty. The Pulumi blueprint normalises
// to this subset via sanitizeGCPLabel so a user-entered "Team A"
// doesn't reach the provider as an invalid label.
var gcpLabelRE = regexp.MustCompile(`^[a-z0-9_-]{0,63}$`)

func validateTagValue(key, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", key)
	}
	if !tagValueRE.MatchString(value) {
		return fmt.Errorf("%s %q is invalid: use letters, digits, spaces, and _.:/=+-@ (≤128 chars)", key, value)
	}
	return nil
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
		{Key: "region", Label: "Primary region / location", Type: "string", Default: "us-east-1",
			Description: "Baked into each Pulumi.<env>.yaml using the cloud-specific config key: aws:region / gcp:region / azure-native:location."},
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
	region := stringInput(values, "region", "us-east-1")
	owner := stringInput(values, "owner_tag", "platform")
	if err := validateTagValue("owner_tag", owner); err != nil {
		return nil, err
	}

	// A minimal seed resource per cloud so the generated program isn't
	// empty. Users replace/extend via the canvas; the seed exists so
	// `pulumi preview` succeeds immediately after scaffolding without
	// requiring the user to edit index.ts by hand. owner_tag flows
	// onto the seed's tags so the configured value round-trips to
	// real infrastructure instead of being a decorative input. region
	// is threaded through so the seed's location matches the stack
	// config (previously hardcoded to US / WestUS2 regardless).
	seed := seedResourcesFor(cloud, name, owner, region)

	var files []File
	for _, env := range envs {
		proj := pulumi.ProjectConfig{
			Name:         fmt.Sprintf("%s-%s", name, env),
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
// owner is stamped onto the resource's tags (or GCP-native labels) so
// the scaffold's owner_tag input isn't a decorative no-op. region
// drives the seed's location value too — the stack config already
// carries it, and forcing a hardcoded "US" / "WestUS2" would create a
// confusing mismatch between stack yaml and resource location.
func seedResourcesFor(cloud, projectName, owner, region string) []parser.Resource {
	// AWS/Azure accept PascalCase tag keys; GCP labels must be
	// lowercase [a-z0-9_-] so values get sanitised rather than just
	// lowercased — "Team A" → "team_a" so the provider doesn't reject
	// at apply time.
	awsAzureTags := map[string]any{"Owner": owner, "ManagedBy": "iac-studio"}
	gcpLabels := map[string]any{
		"owner":      sanitizeGCPLabel(owner),
		"managed_by": "iac-studio",
	}
	switch cloud {
	case "aws":
		return []parser.Resource{{
			ID:   "aws_s3_bucket.seed",
			Type: "aws_s3_bucket",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"bucket": projectName + "-seed",
				"tags":   awsAzureTags,
			},
		}}
	case "gcp":
		// GCS bucket locations use short names (US, EU, ASIA) or
		// specific multi-regions. If the user provided a canonical
		// region like us-central1 we fall back to US so the seed
		// doesn't reject; hand-editable after scaffold.
		loc := "US"
		if region != "" && !strings.Contains(region, "-") {
			loc = strings.ToUpper(region)
		}
		return []parser.Resource{{
			ID:   "google_storage_bucket.seed",
			Type: "google_storage_bucket",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"name":     projectName + "-seed",
				"location": loc,
				"labels":   gcpLabels,
			},
		}}
	case "azure":
		loc := region
		if loc == "" {
			loc = "WestUS2"
		}
		return []parser.Resource{{
			ID:   "azurerm_resource_group.seed",
			Type: "azurerm_resource_group",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"name":     projectName + "-rg",
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
# cloning. Requires: pulumi, node, npm. The SafeRunner calls the
# equivalent on the 'init' command when driving commands from the UI.
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
		fmt.Fprintf(&b, "pulumi stack init %s             # once per env — creates the Pulumi stack\n", env)
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
