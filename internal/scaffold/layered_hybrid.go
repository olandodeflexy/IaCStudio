package scaffold

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iac-studio/iac-studio/internal/pulumi"
)

// LayeredHybridBlueprint renders a layered-v1 project where each
// environment can choose Terraform or Pulumi independently.
type LayeredHybridBlueprint struct{}

func (b *LayeredHybridBlueprint) ID() string   { return "layered-hybrid" }
func (b *LayeredHybridBlueprint) Name() string { return "Layered Hybrid (Terraform + Pulumi)" }
func (b *LayeredHybridBlueprint) Description() string {
	return "Multi-environment project with a per-environment tool selector: Terraform roots and Pulumi TypeScript projects can live side by side."
}
func (b *LayeredHybridBlueprint) Tool() string { return "multi" }

func (b *LayeredHybridBlueprint) Inputs() []Input {
	return []Input{
		{Key: "project_name", Label: "Project name", Type: "string", Required: true},
		{Key: "cloud", Label: "Primary cloud provider", Type: "select",
			Options: []string{"aws", "gcp", "azure"}, Default: "aws", Required: true},
		{Key: "environments", Label: "Environments", Type: "multiselect",
			Options: []string{"dev", "staging", "prod"}, Default: []string{"dev", "prod"}},
		{Key: "pulumi_environments", Label: "Pulumi environments", Type: "multiselect",
			Options: []string{"dev", "staging", "prod"}, Default: []string{"dev"},
			Description: "Selected environments render as Pulumi TypeScript; the remaining environments render as Terraform."},
		{Key: "modules", Label: "Terraform module scaffolds", Type: "multiselect",
			Options: []string{"networking", "compute", "database", "security", "monitoring"},
			Default: []string{"networking", "compute", "database", "security", "monitoring"}},
		{Key: "backend", Label: "Terraform remote state backend", Type: "select",
			Options: []string{"s3", "gcs", "azurerm", "none"}, Default: "s3"},
		{Key: "state_bucket", Label: "Terraform state bucket/container name", Type: "string",
			Description: "Used in Terraform backend.tf — leave blank to derive from project name."},
		{Key: "state_region", Label: "Terraform state backend region", Type: "string", Default: "us-east-1"},
		{Key: "region", Label: "Pulumi primary region / location", Type: "string", Default: "",
			Description: "Baked into Pulumi.<env>.yaml for Pulumi environments. Leave empty to use the per-cloud default."},
		{Key: "owner_tag", Label: "Owner tag", Type: "string", Default: "platform"},
		{Key: "cost_center_tag", Label: "Cost center tag", Type: "string", Default: "shared"},
	}
}

func (b *LayeredHybridBlueprint) Render(values map[string]any) ([]File, error) {
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
	if len(envs) == 0 {
		return nil, fmt.Errorf("at least one environment is required")
	}
	envSet := make(map[string]struct{}, len(envs))
	for _, env := range envs {
		if err := validatePathSegment("environment", env); err != nil {
			return nil, err
		}
		envSet[env] = struct{}{}
	}

	pulumiEnvInputs := stringSliceInput(values, "pulumi_environments", nil)
	if len(pulumiEnvInputs) == 0 {
		pulumiEnvInputs = []string{envs[0]}
	}
	pulumiEnvSet := make(map[string]struct{}, len(pulumiEnvInputs))
	for _, env := range pulumiEnvInputs {
		if err := validatePathSegment("pulumi environment", env); err != nil {
			return nil, err
		}
		if _, ok := envSet[env]; !ok {
			return nil, fmt.Errorf("pulumi environment %q is not in environments", env)
		}
		pulumiEnvSet[env] = struct{}{}
	}

	modules := stringSliceInput(values, "modules", []string{"networking", "compute", "database", "security", "monitoring"})
	for _, mod := range modules {
		if err := validatePathSegment("module", mod); err != nil {
			return nil, err
		}
	}
	backend := stringInput(values, "backend", "s3")
	switch backend {
	case "s3", "gcs", "azurerm", "none":
	default:
		return nil, fmt.Errorf("backend %q is unsupported: must be one of s3, gcs, azurerm, none", backend)
	}
	stateBucket := stringInput(values, "state_bucket", defaultStateBucket(name, backend))
	switch backend {
	case "none":
	case "azurerm":
		if !azureStorageAccountRE.MatchString(stateBucket) {
			return nil, fmt.Errorf("state_bucket %q is invalid for azurerm backend: must be 3-24 lowercase letters/digits with no hyphens", stateBucket)
		}
	default:
		if err := validateSafeName("state_bucket", stateBucket); err != nil {
			return nil, err
		}
	}
	stateRegion := stringInput(values, "state_region", "us-east-1")
	if err := validateSafeName("state_region", stateRegion); err != nil {
		return nil, err
	}
	region, err := pulumiRegionInput(values, cloud)
	if err != nil {
		return nil, err
	}
	owner := stringInput(values, "owner_tag", "platform")
	if err := validateTagValue("owner_tag", owner); err != nil {
		return nil, err
	}
	costCenter := stringInput(values, "cost_center_tag", "shared")
	if err := validateHCLSafeValue("cost_center_tag", costCenter); err != nil {
		return nil, err
	}

	envTools := make(map[string]string, len(envs))
	terraformEnvs := make([]string, 0, len(envs))
	pulumiEnvs := make([]string, 0, len(envs))
	for _, env := range envs {
		if _, ok := pulumiEnvSet[env]; ok {
			envTools[env] = "pulumi"
			pulumiEnvs = append(pulumiEnvs, env)
			continue
		}
		envTools[env] = "terraform"
		terraformEnvs = append(terraformEnvs, env)
	}

	ctx := layeredCtx{
		Name: name, Cloud: cloud, Envs: terraformEnvs, Modules: modules,
		Backend: backend, StateBucket: stateBucket, StateRegion: stateRegion,
		Owner: owner, CostCenter: costCenter,
	}

	descriptorModules := modules
	if len(terraformEnvs) == 0 {
		descriptorModules = nil
	}

	var files []File
	files = append(files, hybridRootFiles(name, cloud, envs, envTools, descriptorModules, owner, costCenter)...)
	for _, env := range terraformEnvs {
		files = append(files, ctx.envFiles(env)...)
	}
	if len(terraformEnvs) > 0 {
		for _, mod := range modules {
			files = append(files, ctx.moduleFiles(mod)...)
		}
		files = append(files, ctx.policyFiles()...)
	}
	for _, env := range pulumiEnvs {
		envFiles, err := renderPulumiEnvFiles(name, cloud, env, owner, region)
		if err != nil {
			return nil, err
		}
		files = append(files, envFiles...)
	}
	if len(pulumiEnvs) > 0 {
		files = append(files, pulumiCrossGuardPolicyFiles()...)
	}
	files = append(files, hybridLifecycleScripts()...)
	return files, nil
}

func pulumiRegionInput(values map[string]any, cloud string) (string, error) {
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
	if cloud == "gcp" && !gcpRegionRE.MatchString(region) {
		switch strings.ToUpper(region) {
		case "US", "EU", "ASIA":
			region = strings.ToUpper(region)
		default:
			return "", fmt.Errorf("region %q is not a valid GCP region (expected canonical form like us-central1 or short multi-region US/EU/ASIA)", region)
		}
	}
	return region, nil
}

func renderPulumiEnvFiles(name, cloud, env, owner, region string) ([]File, error) {
	const maxBase = 60
	projName := truncateForBucketName(name, maxBase) + "-" + env
	proj := pulumi.ProjectConfig{
		Name:         projName,
		Description:  fmt.Sprintf("%s - %s environment (Pulumi)", name, env),
		Environments: []string{env},
		Region:       region,
		Resources:    seedResourcesFor(cloud, name, env, owner, region),
	}
	emitted, err := pulumi.GenerateProject(proj)
	if err != nil {
		return nil, fmt.Errorf("generate pulumi project for env %q: %w", env, err)
	}
	files := make([]File, 0, len(emitted))
	for _, f := range emitted {
		files = append(files, File{
			Path:    fmt.Sprintf("environments/%s/%s", env, f.Path),
			Content: f.Content,
		})
	}
	return files, nil
}

func hybridRootFiles(name, cloud string, envs []string, envTools map[string]string, modules []string, owner, costCenter string) []File {
	readme := renderHybridProjectReadme(name, cloud, envs, envTools)
	gitignore := `# Terraform
.terraform/
*.tfstate
*.tfstate.*
*.tfplan
tfplan.json
crash.log
crash.*.log

# Sensitive inputs
*.tfvars
*.tfvars.json
override.tf
override.tf.json
*_override.tf
*_override.tf.json

# Pulumi / Node
node_modules/
bin/
*.log
*.tsbuildinfo
.pulumi/

# OS
.DS_Store
Thumbs.db
`
	metaObj := struct {
		Layout           string            `json:"layout"`
		Tool             string            `json:"tool"`
		Blueprint        string            `json:"blueprint"`
		ProjectName      string            `json:"project_name"`
		Cloud            string            `json:"cloud"`
		Environments     []string          `json:"environments"`
		EnvironmentTools map[string]string `json:"environment_tools"`
		Modules          []string          `json:"modules,omitempty"`
		Tags             map[string]string `json:"tags"`
	}{
		Layout:           "layered-v1",
		Tool:             "multi",
		Blueprint:        "layered-hybrid",
		ProjectName:      name,
		Cloud:            cloud,
		Environments:     envs,
		EnvironmentTools: envTools,
		Modules:          modules,
		Tags: map[string]string{
			"Owner":      owner,
			"CostCenter": costCenter,
			"ManagedBy":  "iac-studio",
		},
	}
	studioMetaBytes, err := json.MarshalIndent(metaObj, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("scaffold: failed to marshal .iac-studio.json (this is a bug, please report it): %v", err))
	}
	return []File{
		{Path: "README.md", Content: []byte(readme)},
		{Path: ".gitignore", Content: []byte(gitignore)},
		{Path: ".iac-studio.json", Content: append(studioMetaBytes, '\n')},
	}
}

func renderHybridProjectReadme(name, cloud string, envs []string, envTools map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", name)
	fmt.Fprintf(&b, "Hybrid layered project scaffolded by IaC Studio, targeting %s.\n\n", cloud)
	b.WriteString("## Environment Tools\n\n")
	for _, env := range envs {
		fmt.Fprintf(&b, "- `%s`: %s\n", env, envTools[env])
	}
	b.WriteString("\n## Layout\n\n")
	b.WriteString("- `environments/{env}/` - Terraform roots or Pulumi projects, depending on `.iac-studio.json`.\n")
	b.WriteString("- `modules/` - reusable Terraform modules for Terraform environments.\n")
	b.WriteString("- `policies/` - OPA/Sentinel for Terraform and CrossGuard packs for Pulumi.\n")
	b.WriteString("- `scripts/` - lifecycle wrappers that detect each environment's tool.\n\n")
	b.WriteString("## Daily use\n\n")
	b.WriteString("```bash\n")
	b.WriteString("./scripts/init.sh dev\n")
	b.WriteString("./scripts/plan.sh dev\n")
	b.WriteString("./scripts/apply.sh dev\n")
	b.WriteString("```\n")
	return b.String()
}

func hybridLifecycleScripts() []File {
	helper := `env="${1:?env required - usage: $0 <env>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
dir="$root/environments/$env"
if [[ ! -d "$dir" ]]; then
  echo "Unknown environment: $env" >&2
  exit 1
fi
if [[ -f "$dir/Pulumi.yaml" ]]; then
  tool="pulumi"
elif compgen -G "$dir/*.tf" > /dev/null; then
  tool="terraform"
else
  echo "Could not infer IaC tool for environment: $env" >&2
  exit 1
fi
`
	initSh := "#!/usr/bin/env bash\nset -euo pipefail\n" + helper + `if [[ "$tool" == "pulumi" ]]; then
  if [[ -f "$root/policies/crossguard/package.json" ]]; then
    (cd "$root/policies/crossguard" && npm install)
  fi
  (cd "$dir" && npm install)
else
  (cd "$dir" && terraform init -upgrade && terraform validate)
fi
`
	planSh := "#!/usr/bin/env bash\nset -euo pipefail\n" + helper + `if [[ "$tool" == "pulumi" ]]; then
  (cd "$dir" && pulumi preview --stack "$env" --non-interactive --color=never)
else
  (cd "$dir" && terraform plan -out=tfplan.binary -var-file=terraform.tfvars && terraform show -json tfplan.binary > tfplan.json && terraform show -no-color tfplan.binary > tfplan.txt)
  echo "Plan written to $dir/tfplan.txt"
fi
`
	applySh := "#!/usr/bin/env bash\nset -euo pipefail\n" + helper + `if [[ "$tool" == "pulumi" ]]; then
  (cd "$dir" && pulumi up --stack "$env" --yes --non-interactive --color=never)
else
  if [[ ! -f "$dir/tfplan.binary" ]]; then
    echo "No plan found - run scripts/plan.sh $env first" >&2
    exit 1
  fi
  (cd "$dir" && terraform apply tfplan.binary && rm -f tfplan.binary tfplan.txt tfplan.json)
fi
`
	destroySh := "#!/usr/bin/env bash\nset -euo pipefail\n" + helper + `if [[ "$tool" == "pulumi" ]]; then
  (cd "$dir" && pulumi destroy --stack "$env" --yes --non-interactive --color=never)
else
  read -r -p "Type the environment name '$env' to confirm destruction: " confirm
  if [[ "$confirm" != "$env" ]]; then
    echo "aborted" >&2
    exit 1
  fi
  (cd "$dir" && terraform destroy -var-file=terraform.tfvars)
fi
`
	validateSh := `#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
for dir in "$root"/environments/*/; do
  [[ -d "$dir" ]] || continue
  if [[ -f "$dir/Pulumi.yaml" ]]; then
    (cd "$dir" && npx tsc --noEmit)
  elif compgen -G "$dir/*.tf" > /dev/null; then
    (cd "$dir" && terraform init -backend=false -input=false >/dev/null && terraform validate)
  fi
done
if [[ -d "$root/modules" ]]; then
  terraform -chdir="$root" fmt -recursive
  for mod in "$root"/modules/*/; do
    [[ -d "$mod" ]] || continue
    (cd "$mod" && terraform init -backend=false -input=false >/dev/null && terraform validate)
  done
fi
`
	return []File{
		{Path: "scripts/init.sh", Content: []byte(initSh), Executable: true},
		{Path: "scripts/plan.sh", Content: []byte(planSh), Executable: true},
		{Path: "scripts/apply.sh", Content: []byte(applySh), Executable: true},
		{Path: "scripts/destroy.sh", Content: []byte(destroySh), Executable: true},
		{Path: "scripts/validate.sh", Content: []byte(validateSh), Executable: true},
	}
}

func init() {
	Default.Register(&LayeredHybridBlueprint{})
}
