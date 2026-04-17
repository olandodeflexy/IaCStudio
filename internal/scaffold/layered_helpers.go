package scaffold

import (
	"fmt"
	"strings"
)

// providerBlock returns the HCL provider block appropriate for the target cloud
// and environment. The region is left as a variable reference so tfvars wins.
func providerBlock(cloud, env string) string {
	switch cloud {
	case "gcp":
		return `provider "google" {
  project = var.project_name
  region  = var.region
}`
	case "azure":
		return `provider "azurerm" {
  features {}
}`
	default: // aws
		return `provider "aws" {
  region = var.region

  default_tags {
    tags = local.common_tags
  }
}`
	}
}

// moduleCallsFor emits a module "<name>" call for each requested module. Inputs
// are wired in the obvious way: downstream modules consume upstream outputs via
// module.<name>.<output>. The generated code is intentionally minimal — users
// are expected to extend it with their own variables.
func moduleCallsFor(modules []string) string {
	var b strings.Builder
	has := make(map[string]bool, len(modules))
	for _, m := range modules {
		has[m] = true
	}

	if has["networking"] {
		b.WriteString(`module "networking" {
  source = "../../modules/networking"

  project_name = var.project_name
  environment  = local.environment
  cidr_block   = "10.0.0.0/16"
}

`)
	}
	if has["security"] {
		b.WriteString(`module "security" {
  source = "../../modules/security"

  project_name = var.project_name
  environment  = local.environment
`)
		if has["networking"] {
			b.WriteString("  vpc_id       = module.networking.vpc_id\n")
		}
		b.WriteString("}\n\n")
	}
	if has["compute"] {
		b.WriteString(`module "compute" {
  source = "../../modules/compute"

  project_name = var.project_name
  environment  = local.environment
`)
		if has["networking"] {
			b.WriteString("  subnet_ids   = module.networking.private_subnet_ids\n")
		}
		if has["security"] {
			b.WriteString("  security_group_ids = [module.security.app_security_group_id]\n")
		}
		b.WriteString("}\n\n")
	}
	if has["database"] {
		b.WriteString(`module "database" {
  source = "../../modules/database"

  project_name = var.project_name
  environment  = local.environment
`)
		if has["networking"] {
			b.WriteString("  subnet_ids   = module.networking.private_subnet_ids\n")
		}
		if has["security"] {
			b.WriteString("  security_group_ids = [module.security.database_security_group_id]\n")
		}
		b.WriteString("}\n\n")
	}
	if has["monitoring"] {
		b.WriteString(`module "monitoring" {
  source = "../../modules/monitoring"

  project_name = var.project_name
  environment  = local.environment
}

`)
	}
	return b.String()
}

// outputsFor re-exports the most useful module outputs at the environment root.
func outputsFor(modules []string) string {
	has := make(map[string]bool, len(modules))
	for _, m := range modules {
		has[m] = true
	}
	var b strings.Builder
	b.WriteString("# Re-exported module outputs. Extend as needed.\n\n")
	if has["networking"] {
		b.WriteString(`output "vpc_id" {
  description = "VPC identifier for this environment."
  value       = module.networking.vpc_id
}

`)
	}
	if has["compute"] {
		b.WriteString(`output "app_endpoints" {
  description = "Application entrypoints."
  value       = module.compute.endpoints
}

`)
	}
	if has["database"] {
		b.WriteString(`output "database_endpoint" {
  description = "Database connection endpoint."
  value       = module.database.endpoint
  sensitive   = true
}

`)
	}
	return b.String()
}

// defaultRegion picks a sensible default region per cloud and env. prod gets
// the canonical primary region; lower envs stay in the same region by default.
func defaultRegion(cloud, env string) string {
	_ = env
	switch cloud {
	case "gcp":
		return "us-central1"
	case "azure":
		return "eastus"
	default:
		return "us-east-1"
	}
}

// backendBlock emits a remote state backend configured for cloud and env.
//
// For s3 the bucket is per-project; the key is per-environment so multiple envs
// share a bucket with isolated state paths. A DynamoDB table locks concurrent
// access. For gcs/azurerm the equivalent separation is in the prefix.
func backendBlock(backend, bucket, region, project, env string) string {
	switch backend {
	case "gcs":
		return fmt.Sprintf(`terraform {
  backend "gcs" {
    bucket = "%s"
    prefix = "%s/%s"
  }
}
`, bucket, project, env)
	case "azurerm":
		return fmt.Sprintf(`terraform {
  backend "azurerm" {
    resource_group_name  = "%s-tfstate"
    storage_account_name = "%s"
    container_name       = "tfstate"
    key                  = "%s/%s.tfstate"
  }
}
`, project, strings.ReplaceAll(bucket, "-", ""), project, env)
	case "none":
		return `# Remote state backend intentionally disabled for this project.
# Local state lives in terraform.tfstate and MUST NOT be committed.
`
	default: // s3
		return fmt.Sprintf(`terraform {
  backend "s3" {
    bucket         = "%s"
    key            = "%s/%s/terraform.tfstate"
    region         = "%s"
    encrypt        = true
    dynamodb_table = "%s-locks"
  }
}
`, bucket, project, env, region, bucket)
	}
}

// versionsBlock pins the Terraform core + primary provider version for modules.
func versionsBlock(cloud string) string {
	var provider, source, version string
	switch cloud {
	case "gcp":
		provider, source, version = "google", "hashicorp/google", "~> 5.0"
	case "azure":
		provider, source, version = "azurerm", "hashicorp/azurerm", "~> 3.0"
	default:
		provider, source, version = "aws", "hashicorp/aws", "~> 5.0"
	}
	return fmt.Sprintf(`terraform {
  required_version = ">= 1.5.0"

  required_providers {
    %s = {
      source  = "%s"
      version = "%s"
    }
  }
}
`, provider, source, version)
}

// moduleMainFor returns a starter main.tf body for the given module. The goal
// is a syntactically valid, fmt-clean skeleton that users extend — not a
// production-ready implementation.
func moduleMainFor(mod, cloud string) string {
	switch mod {
	case "networking":
		return networkingMain(cloud)
	case "compute":
		return computeMain(cloud)
	case "database":
		return databaseMain(cloud)
	case "security":
		return securityMain(cloud)
	case "monitoring":
		return monitoringMain(cloud)
	}
	return "# " + mod + " module — add resources here.\n"
}

func networkingMain(cloud string) string {
	if cloud != "aws" {
		return "# networking module (" + cloud + ") — add VPC/network resources here.\n"
	}
	return `resource "aws_vpc" "this" {
  cidr_block           = var.cidr_block
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name        = "${var.project_name}-${var.environment}"
    Environment = var.environment
  }
}

resource "aws_subnet" "private" {
  count             = length(var.private_subnet_cidrs)
  vpc_id            = aws_vpc.this.id
  cidr_block        = var.private_subnet_cidrs[count.index]
  availability_zone = var.availability_zones[count.index]

  tags = {
    Name = "${var.project_name}-${var.environment}-private-${count.index}"
    Tier = "private"
  }
}

resource "aws_subnet" "public" {
  count                   = length(var.public_subnet_cidrs)
  vpc_id                  = aws_vpc.this.id
  cidr_block              = var.public_subnet_cidrs[count.index]
  availability_zone       = var.availability_zones[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name = "${var.project_name}-${var.environment}-public-${count.index}"
    Tier = "public"
  }
}
`
}

func computeMain(cloud string) string {
	if cloud != "aws" {
		return "# compute module (" + cloud + ") — add compute resources here.\n"
	}
	return `# Extend with your workload: EC2, ECS, EKS, Lambda, etc.
# Skeleton leaves placeholders wired through to outputs.
`
}

func databaseMain(cloud string) string {
	if cloud != "aws" {
		return "# database module (" + cloud + ") — add database resources here.\n"
	}
	return `# Starter: extend with aws_db_instance, aws_rds_cluster, etc.
# Remember: enable storage_encrypted and set deletion_protection in prod.
`
}

func securityMain(cloud string) string {
	if cloud != "aws" {
		return "# security module (" + cloud + ") — add IAM/SG/KMS resources here.\n"
	}
	return `resource "aws_security_group" "app" {
  name        = "${var.project_name}-${var.environment}-app"
  description = "Application security group"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "database" {
  name        = "${var.project_name}-${var.environment}-database"
  description = "Database security group"
  vpc_id      = var.vpc_id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
  }
}
`
}

func monitoringMain(cloud string) string {
	if cloud != "aws" {
		return "# monitoring module (" + cloud + ") — add metrics/logs/alerts here.\n"
	}
	return `resource "aws_cloudwatch_log_group" "app" {
  name              = "/${var.project_name}/${var.environment}/app"
  retention_in_days = 30
}
`
}

func moduleVariablesFor(mod string) string {
	common := `variable "project_name" {
  description = "Project name prefix."
  type        = string
}

variable "environment" {
  description = "Environment name (dev, staging, prod)."
  type        = string
}

`
	switch mod {
	case "networking":
		return common + `variable "cidr_block" {
  description = "VPC CIDR block."
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "AZs to spread subnets across."
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b"]
}

variable "public_subnet_cidrs" {
  description = "Public subnet CIDRs, one per AZ."
  type        = list(string)
  default     = ["10.0.1.0/24", "10.0.2.0/24"]
}

variable "private_subnet_cidrs" {
  description = "Private subnet CIDRs, one per AZ."
  type        = list(string)
  default     = ["10.0.10.0/24", "10.0.11.0/24"]
}
`
	case "security":
		return common + `variable "vpc_id" {
  description = "VPC to attach security groups to."
  type        = string
}
`
	case "compute", "database":
		return common + `variable "subnet_ids" {
  description = "Subnet IDs this module should place resources in."
  type        = list(string)
  default     = []
}

variable "security_group_ids" {
  description = "Security groups to attach."
  type        = list(string)
  default     = []
}
`
	default:
		return common
	}
}

// moduleOutputsFor emits the outputs.tf body for a module.
//
// Outputs must reference only resources that exist in main.tf — today only the
// AWS skeletons declare real resources, so non-AWS renders get placeholder
// outputs (empty list / empty string) that keep the wiring intact without
// breaking `terraform validate`. Once the GCP/Azure module bodies fill in,
// swap the placeholders here for provider-specific references.
func moduleOutputsFor(mod, cloud string) string {
	if cloud != "aws" {
		return moduleOutputsPlaceholder(mod)
	}
	switch mod {
	case "networking":
		return `output "vpc_id" {
  description = "VPC identifier."
  value       = aws_vpc.this.id
}

output "public_subnet_ids" {
  description = "IDs of the public subnets."
  value       = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  description = "IDs of the private subnets."
  value       = aws_subnet.private[*].id
}
`
	case "security":
		return `output "app_security_group_id" {
  description = "Security group ID for application workloads."
  value       = aws_security_group.app.id
}

output "database_security_group_id" {
  description = "Security group ID for database workloads."
  value       = aws_security_group.database.id
}
`
	case "compute":
		return `output "endpoints" {
  description = "Application entrypoints (populated once resources are added)."
  value       = []
}
`
	case "database":
		return `output "endpoint" {
  description = "Database connection endpoint (populated once resources are added)."
  value       = ""
}
`
	case "monitoring":
		return `output "log_group_name" {
  description = "Application log group name."
  value       = aws_cloudwatch_log_group.app.name
}
`
	}
	return ""
}

// moduleOutputsPlaceholder returns outputs whose values are literals rather
// than resource references — used for non-AWS renders where main.tf is still
// a comment-only skeleton. The output NAMES match the AWS versions so root
// modules can keep wiring module.<name>.<output> consistently.
func moduleOutputsPlaceholder(mod string) string {
	switch mod {
	case "networking":
		return `output "vpc_id" {
  description = "VPC identifier (placeholder — populate when module body lands)."
  value       = ""
}

output "public_subnet_ids" {
  description = "IDs of the public subnets (placeholder)."
  value       = []
}

output "private_subnet_ids" {
  description = "IDs of the private subnets (placeholder)."
  value       = []
}
`
	case "security":
		return `output "app_security_group_id" {
  description = "Security group ID for application workloads (placeholder)."
  value       = ""
}

output "database_security_group_id" {
  description = "Security group ID for database workloads (placeholder)."
  value       = ""
}
`
	case "compute":
		return `output "endpoints" {
  description = "Application entrypoints (placeholder)."
  value       = []
}
`
	case "database":
		return `output "endpoint" {
  description = "Database connection endpoint (placeholder)."
  value       = ""
}
`
	case "monitoring":
		return `output "log_group_name" {
  description = "Application log group name (placeholder)."
  value       = ""
}
`
	}
	return ""
}
