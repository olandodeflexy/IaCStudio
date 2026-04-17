package scaffold

import (
	"encoding/json"
	"fmt"
	"regexp"
	"unicode"
)

// safeNameRE restricts free-form string inputs to a conservative character set.
// Values are embedded in generated HCL and cloud resource names, so anything
// outside this set would either break HCL parsing or produce invalid provider
// identifiers. S3 buckets are the strictest consumer (3-63 chars, lowercase,
// hyphen-separated); we use that as the common denominator.
var safeNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// validateSafeName rejects user-supplied strings that would either break
// generated HCL or produce invalid cloud resource names.
func validateSafeName(key, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", key)
	}
	if !safeNameRE.MatchString(value) {
		return fmt.Errorf("%s %q is invalid: use lowercase letters, digits, and hyphens; must start with a letter and be 2–63 characters long", key, value)
	}
	return nil
}

// titleCase uppercases the first rune of s. Stands in for the deprecated
// strings.Title — sufficient for single-word module names (networking,
// compute, …) we actually pass in.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// LayeredTerraformBlueprint renders the canonical layered Terraform layout:
//
//	environments/{env}/{main.tf,variables.tf,outputs.tf,terraform.tfvars,backend.tf}
//	modules/{module}/{main.tf,variables.tf,outputs.tf,versions.tf,README.md}
//	policies/{sentinel,opa}/...
//	scripts/{init,plan,apply,destroy,validate}.sh
//	.gitignore, README.md, .iac-studio.json
//
// It mirrors the "Terraform Project Structure: Best Practices" diagram: one
// root per environment with its own backend + tfvars, reusable modules with
// version constraints, a policies layer with both Sentinel and OPA seeds, and
// a scripts layer wiring the standard lifecycle commands.
type LayeredTerraformBlueprint struct{}

func (b *LayeredTerraformBlueprint) ID() string          { return "layered-terraform" }
func (b *LayeredTerraformBlueprint) Name() string        { return "Layered Terraform (best practices)" }
func (b *LayeredTerraformBlueprint) Description() string {
	return "Multi-environment Terraform project with reusable modules, Sentinel/OPA policies, and lifecycle scripts. Matches the canonical layered layout."
}
func (b *LayeredTerraformBlueprint) Tool() string { return "terraform" }

func (b *LayeredTerraformBlueprint) Inputs() []Input {
	return []Input{
		{Key: "project_name", Label: "Project name", Type: "string", Required: true},
		{Key: "cloud", Label: "Primary cloud provider", Type: "select",
			Options: []string{"aws", "gcp", "azure"}, Default: "aws", Required: true},
		{Key: "environments", Label: "Environments", Type: "multiselect",
			Options: []string{"dev", "staging", "prod"}, Default: []string{"dev", "prod"}},
		{Key: "modules", Label: "Module scaffolds", Type: "multiselect",
			Options: []string{"networking", "compute", "database", "security", "monitoring"},
			Default: []string{"networking", "compute", "database", "security", "monitoring"}},
		{Key: "backend", Label: "Remote state backend", Type: "select",
			Options: []string{"s3", "gcs", "azurerm", "none"}, Default: "s3"},
		{Key: "state_bucket", Label: "State bucket/container name", Type: "string",
			Description: "Used in backend.tf — leave blank to derive from project name."},
		{Key: "state_region", Label: "State backend region", Type: "string", Default: "us-east-1"},
		{Key: "owner_tag", Label: "Owner tag", Type: "string", Default: "platform"},
		{Key: "cost_center_tag", Label: "Cost center tag", Type: "string", Default: "shared"},
	}
}

func (b *LayeredTerraformBlueprint) Render(values map[string]any) ([]File, error) {
	name := stringInput(values, "project_name", "")
	if err := validateSafeName("project_name", name); err != nil {
		return nil, err
	}
	cloud := stringInput(values, "cloud", "aws")
	envs := stringSliceInput(values, "environments", []string{"dev", "prod"})
	modules := stringSliceInput(values, "modules", []string{"networking", "compute", "database", "security", "monitoring"})
	backend := stringInput(values, "backend", "s3")
	stateBucket := stringInput(values, "state_bucket", name+"-tfstate")
	if err := validateSafeName("state_bucket", stateBucket); err != nil {
		return nil, err
	}
	stateRegion := stringInput(values, "state_region", "us-east-1")
	owner := stringInput(values, "owner_tag", "platform")
	costCenter := stringInput(values, "cost_center_tag", "shared")

	ctx := layeredCtx{
		Name: name, Cloud: cloud, Envs: envs, Modules: modules,
		Backend: backend, StateBucket: stateBucket, StateRegion: stateRegion,
		Owner: owner, CostCenter: costCenter,
	}

	var files []File
	files = append(files, ctx.rootFiles()...)
	for _, env := range envs {
		files = append(files, ctx.envFiles(env)...)
	}
	for _, mod := range modules {
		files = append(files, ctx.moduleFiles(mod)...)
	}
	files = append(files, ctx.policyFiles()...)
	files = append(files, ctx.scriptFiles()...)
	return files, nil
}

// layeredCtx carries resolved inputs through the per-file renderers.
type layeredCtx struct {
	Name        string
	Cloud       string
	Envs        []string
	Modules     []string
	Backend     string
	StateBucket string
	StateRegion string
	Owner       string
	CostCenter  string
}

func (c layeredCtx) rootFiles() []File {
	readme := fmt.Sprintf(`# %s

Layered Terraform project scaffolded by IaC Studio.

## Layout

- environments/ — per-environment roots (dev, prod, …). Each has its own backend and tfvars.
- modules/ — reusable modules (networking, compute, database, security, monitoring).
- policies/ — Sentinel and OPA policy bundles evaluated before apply.
- scripts/ — lifecycle wrappers (init, plan, apply, destroy, validate).

## Usage

    cd environments/dev
    ../../scripts/init.sh
    ../../scripts/plan.sh
    ../../scripts/apply.sh

State is stored remotely (%s). Never commit terraform.tfstate or terraform.tfvars.
`, c.Name, c.Backend)

	gitignore := `# Terraform
.terraform/
*.tfstate
*.tfstate.*
*.tfplan
crash.log
crash.*.log

# Sensitive inputs — never commit
*.tfvars
*.tfvars.json
override.tf
override.tf.json
*_override.tf
*_override.tf.json

# OS
.DS_Store
Thumbs.db
`

	metaObj := struct {
		Layout       string            `json:"layout"`
		Tool         string            `json:"tool"`
		Cloud        string            `json:"cloud"`
		Environments []string          `json:"environments"`
		Modules      []string          `json:"modules"`
		Tags         map[string]string `json:"tags"`
	}{
		Layout:       "layered-v1",
		Tool:         "terraform",
		Cloud:        c.Cloud,
		Environments: c.Envs,
		Modules:      c.Modules,
		Tags: map[string]string{
			"Owner":      c.Owner,
			"CostCenter": c.CostCenter,
			"ManagedBy":  "iac-studio",
		},
	}
	studioMetaBytes, err := json.MarshalIndent(metaObj, "", "  ")
	if err != nil {
		// Unreachable: all fields are plain strings/slices with no custom marshalers.
		// If triggered, it indicates a programming error or data corruption — please file a bug.
		panic(fmt.Sprintf("scaffold: failed to marshal .iac-studio.json (this is a bug, please report it): %v", err))
	}
	studioMeta := string(studioMetaBytes) + "\n"

	return []File{
		{Path: "README.md", Content: []byte(readme)},
		{Path: ".gitignore", Content: []byte(gitignore)},
		{Path: ".iac-studio.json", Content: []byte(studioMeta)},
	}
}

func (c layeredCtx) envFiles(env string) []File {
	base := "environments/" + env

	main := fmt.Sprintf(`# %s environment root — wires modules together.

%s

locals {
  environment = "%s"
  common_tags = {
    Environment = local.environment
    Project     = var.project_name
    Owner       = var.owner
    CostCenter  = var.cost_center
    ManagedBy   = "iac-studio"
  }
}

`, env, providerBlock(c.Cloud, env), env) + moduleCallsFor(c.Modules)

	variables := fmt.Sprintf(`variable "project_name" {
  description = "Name of the project — used as a prefix for named resources."
  type        = string
  default     = "%s"
}

variable "region" {
  description = "Primary region for this environment."
  type        = string
}

variable "owner" {
  description = "Owning team. Applied as an Owner tag on every taggable resource."
  type        = string
  default     = "%s"
}

variable "cost_center" {
  description = "Cost center for chargeback. Applied as a CostCenter tag."
  type        = string
  default     = "%s"
}
`, c.Name, c.Owner, c.CostCenter)

	outputs := outputsFor(c.Modules)

	tfvars := fmt.Sprintf(`# %s environment values. Ignored by git (see .gitignore).
project_name = "%s"
region       = "%s"
owner        = "%s"
cost_center  = "%s"
`, env, c.Name, defaultRegion(c.Cloud, env), c.Owner, c.CostCenter)

	backend := backendBlock(c.Backend, c.StateBucket, c.StateRegion, c.Name, env)

	return []File{
		{Path: base + "/main.tf", Content: []byte(main)},
		{Path: base + "/variables.tf", Content: []byte(variables)},
		{Path: base + "/outputs.tf", Content: []byte(outputs)},
		{Path: base + "/terraform.tfvars", Content: []byte(tfvars)},
		{Path: base + "/backend.tf", Content: []byte(backend)},
	}
}

func (c layeredCtx) moduleFiles(mod string) []File {
	base := "modules/" + mod

	main := moduleMainFor(mod, c.Cloud)
	variables := moduleVariablesFor(mod)
	outputs := moduleOutputsFor(mod, c.Cloud)
	versions := versionsBlock(c.Cloud)
	readme := fmt.Sprintf(`# %s module

Reusable module — called from each environment root with environment-specific inputs.

## Inputs

See variables.tf for the full list.

## Outputs

See outputs.tf. Downstream modules consume these via root-level references (e.g. module.networking.vpc_id).
`, titleCase(mod))

	return []File{
		{Path: base + "/main.tf", Content: []byte(main)},
		{Path: base + "/variables.tf", Content: []byte(variables)},
		{Path: base + "/outputs.tf", Content: []byte(outputs)},
		{Path: base + "/versions.tf", Content: []byte(versions)},
		{Path: base + "/README.md", Content: []byte(readme)},
	}
}

func (c layeredCtx) policyFiles() []File {
	sentinelCost := `# Restricts the total monthly cost of a plan.
import "tfplan/v2" as tfplan
import "decimal"

# Adjust this cap once cost estimation is wired.
monthly_cost_limit = decimal.new(5000)

main = rule {
  true
}
`

	sentinelSecurity := `# Baseline security rules.
import "tfplan/v2" as tfplan

# Every S3 bucket must have server-side encryption.
s3_buckets = filter tfplan.resource_changes as _, rc {
  rc.type is "aws_s3_bucket" and
  (rc.change.actions contains "create" or rc.change.actions contains "update")
}

encrypted_buckets = rule {
  all s3_buckets as _, b {
    b.change.after.server_side_encryption_configuration is not null
  }
}

main = rule { encrypted_buckets }
`

	sentinelNaming := `# Naming convention: all named resources must be lowercase with hyphens.
import "tfplan/v2" as tfplan
import "strings"

valid_name = func(n) {
  return n matches "^[a-z][a-z0-9-]*$"
}

main = rule {
  all tfplan.resource_changes as _, rc {
    rc.change.after.name is null or valid_name(rc.change.after.name)
  }
}
`

	opaTagging := `package terraform.tags

# Every taggable resource must carry Owner, CostCenter, Environment.
required_tags := {"Owner", "CostCenter", "Environment"}

deny[msg] {
  resource := input.resource_changes[_]
  resource.change.after.tags
  missing := required_tags - {tag | resource.change.after.tags[tag]}
  count(missing) > 0
  msg := sprintf("resource %q is missing required tags: %v", [resource.address, missing])
}
`

	opaNetwork := `package terraform.network

# No security group may expose port 22 to 0.0.0.0/0.
deny[msg] {
  resource := input.resource_changes[_]
  resource.type == "aws_security_group"
  rule := resource.change.after.ingress[_]
  rule.from_port <= 22
  rule.to_port >= 22
  rule.cidr_blocks[_] == "0.0.0.0/0"
  msg := sprintf("security group %q exposes SSH to the world", [resource.address])
}
`

	opaCompliance := `package terraform.compliance

# Every RDS instance must have storage encryption.
deny[msg] {
  resource := input.resource_changes[_]
  resource.type == "aws_db_instance"
  not resource.change.after.storage_encrypted
  msg := sprintf("RDS instance %q has unencrypted storage", [resource.address])
}
`

	return []File{
		{Path: "policies/sentinel/cost-control.sentinel", Content: []byte(sentinelCost)},
		{Path: "policies/sentinel/security-baseline.sentinel", Content: []byte(sentinelSecurity)},
		{Path: "policies/sentinel/naming-conventions.sentinel", Content: []byte(sentinelNaming)},
		{Path: "policies/opa/resource-tagging.rego", Content: []byte(opaTagging)},
		{Path: "policies/opa/network-rules.rego", Content: []byte(opaNetwork)},
		{Path: "policies/opa/compliance.rego", Content: []byte(opaCompliance)},
	}
}

func (c layeredCtx) scriptFiles() []File {
	initSh := `#!/usr/bin/env bash
# Initialize a Terraform environment root.
# Usage: scripts/init.sh <env>  (run from project root)
set -euo pipefail
env="${1:-${TF_ENV:-dev}}"
cd "$(dirname "$0")/../environments/$env"
terraform init -upgrade
terraform validate
`

	planSh := `#!/usr/bin/env bash
# Generate and review a Terraform plan for an environment.
# Usage: scripts/plan.sh <env>
set -euo pipefail
env="${1:-${TF_ENV:-dev}}"
cd "$(dirname "$0")/../environments/$env"
terraform plan -out=tfplan.binary -var-file=terraform.tfvars
terraform show -no-color tfplan.binary > tfplan.txt
echo "Plan written to $(pwd)/tfplan.txt"
`

	applySh := `#!/usr/bin/env bash
# Apply a previously generated plan. Refuses without tfplan.binary.
# Usage: scripts/apply.sh <env>
set -euo pipefail
env="${1:-${TF_ENV:-dev}}"
cd "$(dirname "$0")/../environments/$env"
if [[ ! -f tfplan.binary ]]; then
  echo "No plan found — run scripts/plan.sh $env first" >&2
  exit 1
fi
terraform apply tfplan.binary
rm -f tfplan.binary tfplan.txt
`

	destroySh := `#!/usr/bin/env bash
# Destroy the infrastructure for an environment. Prompts for confirmation.
# Usage: scripts/destroy.sh <env>
set -euo pipefail
env="${1:-${TF_ENV:-dev}}"
cd "$(dirname "$0")/../environments/$env"
read -r -p "Type the environment name '$env' to confirm destruction: " confirm
if [[ "$confirm" != "$env" ]]; then
  echo "aborted" >&2
  exit 1
fi
terraform destroy -var-file=terraform.tfvars
`

	validateSh := `#!/usr/bin/env bash
# Format and validate all Terraform code. Useful in pre-commit and CI.
# Both envs and modules are init'd with -backend=false so this script works
# on a fresh checkout without touching remote state.
set -euo pipefail
cd "$(dirname "$0")/.."
terraform fmt -recursive
for env in environments/*/; do
  (cd "$env" && terraform init -backend=false -input=false >/dev/null && terraform validate)
done
for mod in modules/*/; do
  (cd "$mod" && terraform init -backend=false -input=false >/dev/null && terraform validate)
done
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
	Default.Register(&LayeredTerraformBlueprint{})
}
