package scaffold

import (
	"fmt"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/pulumi"
)

// LayeredPulumiBlueprint renders a multi-environment Pulumi
// TypeScript project mirroring the layered-terraform structure:
//
//	environments/{env}/{index.ts, Pulumi.yaml, Pulumi.<env>.yaml,
//	                    package.json, tsconfig.json, .gitignore}
//	modules/{module}/README.md            (stub — TS modules ship as
//	                                       ComponentResources in a later
//	                                       commit)
//	scripts/{init,plan,apply,destroy}.sh
//	README.md, .iac-studio.json
//
// Each environment is a self-contained Pulumi project — same pattern as
// Terraform's per-env root directory. True dual-stack (some envs on
// Terraform, others on Pulumi in the same project) is a follow-up that
// extends this blueprint with a per-env tool selector. For now the
// user picks Pulumi OR Terraform at project creation time.
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
		{Key: "region", Label: "Primary region", Type: "string", Default: "us-east-1",
			Description: "Baked into each Pulumi.<env>.yaml as <provider>:region."},
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
	if err := validateHCLSafeValue("owner_tag", owner); err != nil {
		return nil, err
	}

	// A minimal seed resource per cloud so the generated program isn't
	// empty. Users replace/extend via the canvas; the seed exists so
	// `pulumi preview` succeeds immediately after scaffolding without
	// requiring the user to edit index.ts by hand.
	seed := seedResourcesFor(cloud, name)

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
	files = append(files, File{
		Path:    ".iac-studio.json",
		Content: []byte(fmt.Sprintf(`{
  "tool": "pulumi",
  "blueprint": "layered-pulumi",
  "project_name": %q,
  "cloud": %q,
  "environments": %s,
  "layout": "layered-v1"
}
`, name, cloud, jsonStringSlice(envs))),
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
func seedResourcesFor(cloud, projectName string) []parser.Resource {
	switch cloud {
	case "aws":
		return []parser.Resource{{
			ID:   "aws_s3_bucket.seed",
			Type: "aws_s3_bucket",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"bucket": projectName + "-seed",
			},
		}}
	case "gcp":
		return []parser.Resource{{
			ID:   "google_storage_bucket.seed",
			Type: "google_storage_bucket",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"name":     projectName + "-seed",
				"location": "US",
			},
		}}
	case "azure":
		return []parser.Resource{{
			ID:   "azurerm_resource_group.seed",
			Type: "azurerm_resource_group",
			Name: projectName + "_seed",
			Properties: map[string]any{
				"name":     projectName + "-rg",
				"location": "WestUS2",
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

// jsonStringSlice renders a string slice as a JSON array literal —
// used inside the .iac-studio.json content template which is already
// a hand-written JSON string. Saves pulling in encoding/json just for
// this one-line formatter.
func jsonStringSlice(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func init() {
	Default.Register(&LayeredPulumiBlueprint{})
}
