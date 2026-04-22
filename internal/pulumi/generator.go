// Package pulumi generates a full Pulumi project layout from an IaC
// Studio resource graph. The existing internal/exporter only emitted a
// single-file index.ts preview — this package builds a runnable
// project with the typescript runtime config, per-stack YAML files,
// and the dependency manifests Pulumi expects.
//
// Sibling to internal/scaffold/layered_terraform.go: same blueprint
// shape (environments × modules), same naming conventions, but TS
// instead of HCL. The dual-stack scaffolder consumes both so a single
// canvas can render some environments as Terraform and others as
// Pulumi.
package pulumi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// ProjectConfig is the input to GenerateProject. Name becomes the
// Pulumi project name; Environments enumerate the stack configs we
// emit per-environment (Pulumi.<env>.yaml). Resources is the flat
// resource graph to render into the program file.
type ProjectConfig struct {
	Name         string
	Description  string
	Environments []string
	// Region is baked into each Pulumi.<env>.yaml as a config value.
	// When empty, defaults to us-east-1 for AWS / us-central1 for GCP.
	Region string
	// Runtime pins which Pulumi runtime the project targets. Only
	// "nodejs" (TypeScript) is supported today; keeping the field so
	// future Python/Go runtimes can fork here cleanly.
	Runtime string
	// Resources is the flat graph the program file will emit.
	Resources []parser.Resource
}

// ProjectFile is one output artefact. The scaffold package walks the
// returned slice and writes each file relative to the project root.
// Content is []byte rather than string so we can emit binary artefacts
// later (lockfiles, asset bundles) without breaking the contract.
type ProjectFile struct {
	Path    string
	Content []byte
}

// GenerateProject renders a complete Pulumi TypeScript project:
//
//	Pulumi.yaml                    project manifest
//	Pulumi.<env>.yaml              per-stack config (one per Environment)
//	package.json                   npm deps (@pulumi/pulumi + provider SDKs)
//	tsconfig.json                  TS compiler settings
//	index.ts                       resource program
//	.gitignore                     exclude node_modules / Pulumi state
//
// Errors return a non-nil slice with the files produced so far — same
// convention as the Terraform scaffold. Callers decide whether a
// partial render is worth persisting.
func GenerateProject(cfg ProjectConfig) ([]ProjectFile, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("pulumi.GenerateProject: Name is required")
	}
	if cfg.Runtime == "" {
		cfg.Runtime = "nodejs"
	}
	if cfg.Runtime != "nodejs" {
		return nil, fmt.Errorf("pulumi.GenerateProject: runtime %q not supported yet (only nodejs)", cfg.Runtime)
	}
	if len(cfg.Environments) == 0 {
		cfg.Environments = []string{"dev"}
	}

	files := []ProjectFile{
		{Path: "Pulumi.yaml", Content: []byte(renderPulumiYaml(cfg))},
		{Path: "package.json", Content: []byte(renderPackageJSON(cfg))},
		{Path: "tsconfig.json", Content: []byte(renderTSConfig())},
		{Path: "index.ts", Content: []byte(renderProgram(cfg))},
		{Path: ".gitignore", Content: []byte(renderGitignore())},
	}
	for _, env := range cfg.Environments {
		files = append(files, ProjectFile{
			Path:    fmt.Sprintf("Pulumi.%s.yaml", env),
			Content: []byte(renderStackYaml(cfg, env)),
		})
	}

	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// renderPulumiYaml is the Pulumi project manifest. Runtime is nodejs
// (TypeScript) and the project name must match Pulumi's own naming
// rules (lowercase + hyphens); callers upstream enforce that.
func renderPulumiYaml(cfg ProjectConfig) string {
	desc := cfg.Description
	if desc == "" {
		desc = "IaC Studio — generated Pulumi project"
	}
	return fmt.Sprintf(`name: %s
description: %s
runtime:
  name: nodejs
  options:
    typescript: true
`, cfg.Name, desc)
}

// renderStackYaml emits one Pulumi.<env>.yaml with the config values
// every resource program needs — primarily the cloud region. Real
// projects will layer in secrets (Config.requireSecret) and stack
// references; we stop at region today and let the scaffold composer
// add environment-specific blocks.
func renderStackYaml(cfg ProjectConfig, env string) string {
	hasAWS, hasGCP, hasAzure := detectProviders(cfg.Resources)
	region := cfg.Region

	var lines []string
	lines = append(lines, "config:")
	if hasAWS {
		if region == "" {
			region = "us-east-1"
		}
		lines = append(lines, "  aws:region: "+region)
	}
	if hasGCP {
		if region == "" {
			region = "us-central1"
		}
		lines = append(lines, "  gcp:region: "+region)
	}
	if hasAzure {
		lines = append(lines, "  azure-native:location: WestUS2")
	}
	lines = append(lines, fmt.Sprintf("  %s:environment: %s", cfg.Name, env))
	return strings.Join(lines, "\n") + "\n"
}

// renderPackageJSON emits a minimal package.json with @pulumi/pulumi
// plus whichever provider SDKs the resource graph actually needs.
// Pinning at ^3 for pulumi and latest majors for providers keeps the
// scaffold installable without being too aggressive about fresh
// releases that might break.
func renderPackageJSON(cfg ProjectConfig) string {
	hasAWS, hasGCP, hasAzure := detectProviders(cfg.Resources)

	deps := []string{`"@pulumi/pulumi": "^3.0.0"`}
	if hasAWS {
		deps = append(deps, `"@pulumi/aws": "^7.0.0"`)
	}
	if hasGCP {
		deps = append(deps, `"@pulumi/gcp": "^9.0.0"`)
	}
	if hasAzure {
		deps = append(deps, `"@pulumi/azure-native": "^3.0.0"`)
	}

	return fmt.Sprintf(`{
  "name": "%s",
  "private": true,
  "main": "index.ts",
  "dependencies": {
    %s
  },
  "devDependencies": {
    "@types/node": "^22.0.0",
    "typescript": "^5.5.0"
  }
}
`, cfg.Name, strings.Join(deps, ",\n    "))
}

// renderTSConfig is the tsconfig every Pulumi nodejs project ships
// with. Strict mode is on — catches missing required inputs at compile
// time. Target ES2022 matches Pulumi's own docs.
func renderTSConfig() string {
	return `{
  "compilerOptions": {
    "strict": true,
    "outDir": "bin",
    "target": "es2022",
    "module": "commonjs",
    "moduleResolution": "node",
    "sourceMap": true,
    "experimentalDecorators": true,
    "pretty": true,
    "noFallthroughCasesInSwitch": true,
    "noImplicitReturns": true,
    "forceConsistentCasingInFileNames": true
  },
  "files": [
    "index.ts"
  ]
}
`
}

// renderProgram emits the TypeScript program that creates every
// resource in cfg.Resources. Provider imports are conditionally added
// based on resource prefixes. toCamelCase + typeTerraformToPulumi +
// tsPropValue mirror the helpers already in internal/exporter, copied
// here to keep the package boundary clean (exporter stays preview-
// only; pulumi owns the full-project generator).
func renderProgram(cfg ProjectConfig) string {
	var b strings.Builder

	b.WriteString(`import * as pulumi from "@pulumi/pulumi";
`)
	hasAWS, hasGCP, hasAzure := detectProviders(cfg.Resources)
	if hasAWS {
		b.WriteString(`import * as aws from "@pulumi/aws";
`)
	}
	if hasGCP {
		b.WriteString(`import * as gcp from "@pulumi/gcp";
`)
	}
	if hasAzure {
		b.WriteString(`import * as azure from "@pulumi/azure-native";
`)
	}
	b.WriteString("\n")
	b.WriteString("// Config is populated from Pulumi.<stack>.yaml — see `pulumi config set` to modify.\n")
	b.WriteString(fmt.Sprintf("const config = new pulumi.Config(%q);\n", cfg.Name))
	b.WriteString("const environment = config.get(\"environment\") ?? \"dev\";\n\n")

	for _, r := range cfg.Resources {
		varName := toCamelCase(r.Name)
		pType := terraformToPulumi(r.Type)
		b.WriteString(fmt.Sprintf("const %s = new %s(%q, {\n", varName, pType, r.Name))

		// Stable property ordering so the generated program is
		// deterministic across runs with the same input.
		keys := make([]string, 0, len(r.Properties))
		for k := range r.Properties {
			if strings.HasPrefix(k, "__") {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("    %s: %s,\n", toCamelCase(k), tsPropValue(r.Properties[k])))
		}
		// Tag each resource with the environment so downstream cost
		// allocation / policy tags work out of the box. Only applies
		// when the property isn't already set.
		if _, ok := r.Properties["tags"]; !ok && isTaggableAWS(r.Type) {
			b.WriteString("    tags: { Environment: environment, ManagedBy: \"iac-studio\" },\n")
		}
		b.WriteString("});\n\n")
	}

	b.WriteString("// Exports — consumed by stack references + CI assertions.\n")
	for _, r := range cfg.Resources {
		varName := toCamelCase(r.Name)
		b.WriteString(fmt.Sprintf("export const %sId = %s.id;\n", varName, varName))
	}
	return b.String()
}

// renderGitignore mirrors Pulumi's own scaffold template. Keeping
// node_modules and the local state dir out of VCS is table stakes.
func renderGitignore() string {
	return `node_modules/
bin/
*.log
*.tsbuildinfo
/.pulumi/
`
}
