// Package pulumi generates a full Pulumi project layout from an IaC
// Studio resource graph. The existing internal/exporter only emitted a
// single-file index.ts preview — this package builds a runnable
// project with the typescript runtime config, per-stack YAML files,
// and the dependency manifests Pulumi expects.
//
// Sibling to internal/scaffold/layered_terraform.go: same blueprint
// shape (environments × modules), same naming conventions, but TS
// instead of HCL. This package provides the Pulumi-side generator
// for that blueprint shape; mixed per-environment Terraform+Pulumi
// in a single project is a planned follow-up (see issue #5 — parser
// + dual-stack mode).
package pulumi

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// ValidateProjectName enforces Pulumi's project-name rules AND the
// tighter npm package-name rules (the generator writes cfg.Name into
// both Pulumi.yaml and package.json). Joint constraint: lowercase
// letters, digits, hyphens, underscores; must start with a letter;
// 1-100 chars. Anything stricter belongs in the caller's own input
// validator (e.g. scaffold's safeNameRE) — we stay permissive here
// so GenerateProject accepts whatever a blueprint validated upstream.
func ValidateProjectName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 100 {
		return fmt.Errorf("name %q exceeds Pulumi's 100-char limit", name)
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			// ok
		default:
			return fmt.Errorf("name %q has invalid character %q at position %d (lowercase letters, digits, hyphens, underscores only)", name, r, i)
		}
	}
	if name[0] < 'a' || name[0] > 'z' {
		return fmt.Errorf("name %q must start with a lowercase letter", name)
	}
	return nil
}

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
// Validation and unsupported-runtime errors return (nil, err). On
// success the returned slice contains the complete rendered file set;
// there are no partial-return paths today.
func GenerateProject(cfg ProjectConfig) ([]ProjectFile, error) {
	if err := ValidateProjectName(cfg.Name); err != nil {
		return nil, fmt.Errorf("pulumi.GenerateProject: %w", err)
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
//
// Name + description go through yamlScalar so values containing
// colons, hashes, leading/trailing spaces, or newlines don't produce
// invalid YAML. Pulumi's own manifests tend to stay simple so the
// naive path worked in practice, but user-supplied descriptions are
// the weak link and worth escaping.
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
`, yamlScalar(cfg.Name), yamlScalar(desc))
}

// yamlScalar returns a YAML-safe representation of s. If s contains
// YAML 1.2 plain-scalar metacharacters (: # newline, ! & * etc.),
// matches a reserved keyword (true/false/null/yes/no/on/off/~), or
// parses as a number/int/float (which YAML would coerce to a typed
// value), we wrap in double quotes and escape backslashes + quotes.
// Otherwise emit plain.
//
// Not a full YAML encoder — we only emit simple scalars here, and
// pulling in a yaml dependency just for two fields is overkill. The
// check list is conservative: anything the spec treats specially
// triggers the quoted form.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(s, ":#\n\r\t!&*%@`\"'[]{},|>\\")
	if !needsQuote && (s[0] == ' ' || s[len(s)-1] == ' ') {
		needsQuote = true
	}
	// Pure digits / reserved keywords would be parsed as numbers or
	// booleans if emitted plain.
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off", "~":
		needsQuote = true
	}
	// Numeric-looking scalars ("123", "3.14", "-0.5", "1e6") — YAML
	// would coerce these to int/float, so a version string like "1.0"
	// would round-trip as a float and silently change shape.
	if !needsQuote && looksNumeric(s) {
		needsQuote = true
	}
	if !needsQuote {
		return s
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	escaped = strings.ReplaceAll(escaped, "\t", `\t`)
	return `"` + escaped + `"`
}

// looksNumeric reports whether s would be parsed as a YAML number.
// Matches the common forms: signed/unsigned ints, decimals, scientific
// notation. Kept tight — anything false-positive wouldn't hurt (we
// only add quotes) but false-negatives corrupt shape, so err on the
// side of matching.
func looksNumeric(s string) bool {
	if s == "" {
		return false
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return false
}

// renderStackYaml emits one Pulumi.<env>.yaml with the config values
// every resource program needs — primarily the cloud region. Real
// projects will layer in secrets (Config.requireSecret) and stack
// references; we stop at region today and let the scaffold composer
// add environment-specific blocks.
//
// Region handling keeps a separate default per provider so a project
// mixing AWS + GCP resources with an empty cfg.Region gets both
// us-east-1 for AWS and us-central1 for GCP, instead of AWS's value
// leaking into GCP's config. Azure honours cfg.Region too (treating
// it as a location name) and falls back to WestUS2.
func renderStackYaml(cfg ProjectConfig, env string) string {
	hasAWS, hasGCP, hasAzure := detectProviders(cfg.Resources)
	awsRegion := cfg.Region
	gcpRegion := cfg.Region
	azureLocation := cfg.Region

	var lines []string
	lines = append(lines, "config:")
	// Route every user-supplied value through yamlScalar so a Region
	// containing colons or whitespace (legal for some Azure location
	// names) can't break the manifest.
	if hasAWS {
		if awsRegion == "" {
			awsRegion = "us-east-1"
		}
		lines = append(lines, "  aws:region: "+yamlScalar(awsRegion))
	}
	if hasGCP {
		if gcpRegion == "" {
			gcpRegion = "us-central1"
		}
		lines = append(lines, "  gcp:region: "+yamlScalar(gcpRegion))
	}
	if hasAzure {
		if azureLocation == "" {
			azureLocation = "WestUS2"
		}
		lines = append(lines, "  azure-native:location: "+yamlScalar(azureLocation))
	}
	lines = append(lines, fmt.Sprintf("  %s:environment: %s", cfg.Name, yamlScalar(env)))
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

	// Sort a copy of the resource slice so upstream sources with
	// non-deterministic ordering (filesystem walks, map iteration)
	// can't produce different programs across runs. Ordering by Type
	// then Name then ID mirrors the stable sort the property keys
	// already use and is stable across any upstream input.
	ordered := make([]parser.Resource, len(cfg.Resources))
	copy(ordered, cfg.Resources)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Type != ordered[j].Type {
			return ordered[i].Type < ordered[j].Type
		}
		if ordered[i].Name != ordered[j].Name {
			return ordered[i].Name < ordered[j].Name
		}
		return ordered[i].ID < ordered[j].ID
	})

	// used tracks which TS identifiers have been emitted in this
	// program so we can suffix a counter when two resources share a
	// Name (aws_vpc.main + aws_subnet.main would both camelCase to
	// "main" and produce a redeclaration error). We'd rather emit
	// slightly ugly `mainVpc` / `mainSubnet` than a program that
	// doesn't compile.
	used := make(map[string]int)
	varNames := make([]string, len(ordered))
	for i, r := range ordered {
		// Sanitize before camel-casing because project names can
		// contain hyphens ("acme-infra") which would otherwise produce
		// invalid TypeScript identifiers like `acme-infraSeed`.
		varName := uniqueVarName(sanitizeTSIdent(toCamelCase(r.Name)), r.Type, used)
		varNames[i] = varName
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
			b.WriteString(fmt.Sprintf("    %s: %s,\n", toCamelCase(k), tsPropValue(r.Properties[k], k)))
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
	for i := range ordered {
		varName := varNames[i]
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
