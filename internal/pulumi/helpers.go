package pulumi

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// Helpers live in their own file so the generator stays readable.
// Several are near-duplicates of helpers in internal/exporter; we
// intentionally don't share them — the exporter is a single-file
// preview surface, pulumi is a full project scaffold, and coupling
// them would make a change in one break the other's output.

// detectProviders reports which cloud SDKs the resource list requires.
// Used to gate imports + package.json deps + stack config.
func detectProviders(resources []parser.Resource) (aws, gcp, azure bool) {
	for _, r := range resources {
		switch {
		case strings.HasPrefix(r.Type, "aws_"):
			aws = true
		case strings.HasPrefix(r.Type, "google_"):
			gcp = true
		case strings.HasPrefix(r.Type, "azurerm_"):
			azure = true
		}
	}
	return aws, gcp, azure
}

// terraformToPulumi maps a Terraform resource type to the Pulumi
// constructor. The mapping is deterministic:
//
//	aws_vpc           → aws.ec2.Vpc
//	aws_s3_bucket     → aws.s3.Bucket
//	google_storage_*  → gcp.storage.*
//
// When a specific mapping isn't registered we fall back to the
// convention: drop the provider prefix, split the rest on underscore,
// PascalCase each segment, and lookup a best-effort package name.
// Real completeness across all ~800 AWS resource types lives in a
// future "provider catalog" commit; this covers the subset the
// scaffold + canvas demo cares about.
func terraformToPulumi(tfType string) string {
	if m, ok := pulumiTypeOverrides[tfType]; ok {
		return m
	}
	// Unknown — fall back to a best-effort guess so the scaffold
	// compiles as long as the user hand-fixes the import path later.
	return fallbackPulumiType(tfType)
}

// pulumiTypeOverrides maps the common resources the canvas + fallback
// palette emit. Keep this list tight — a 500-entry dictionary here
// would rot faster than the scaffold can benefit.
var pulumiTypeOverrides = map[string]string{
	// AWS — networking
	"aws_vpc":             "aws.ec2.Vpc",
	"aws_subnet":          "aws.ec2.Subnet",
	"aws_internet_gateway": "aws.ec2.InternetGateway",
	"aws_nat_gateway":     "aws.ec2.NatGateway",
	"aws_route_table":     "aws.ec2.RouteTable",
	"aws_security_group":  "aws.ec2.SecurityGroup",
	// AWS — compute
	"aws_instance":        "aws.ec2.Instance",
	"aws_lambda_function": "aws.lambda.Function",
	"aws_ecs_cluster":     "aws.ecs.Cluster",
	"aws_eks_cluster":     "aws.eks.Cluster",
	// AWS — storage / data
	"aws_s3_bucket":        "aws.s3.Bucket",
	"aws_ebs_volume":       "aws.ebs.Volume",
	"aws_ecr_repository":   "aws.ecr.Repository",
	"aws_db_instance":      "aws.rds.Instance",
	"aws_dynamodb_table":   "aws.dynamodb.Table",
	"aws_elasticache_cluster": "aws.elasticache.Cluster",
	// AWS — security / IAM
	"aws_iam_role":            "aws.iam.Role",
	"aws_iam_policy":          "aws.iam.Policy",
	"aws_kms_key":             "aws.kms.Key",
	"aws_secretsmanager_secret": "aws.secretsmanager.Secret",
	// AWS — lb
	"aws_lb":              "aws.lb.LoadBalancer",
	"aws_lb_target_group": "aws.lb.TargetGroup",
	// GCP
	"google_compute_network": "gcp.compute.Network",
	"google_compute_subnetwork": "gcp.compute.Subnetwork",
	"google_compute_instance": "gcp.compute.Instance",
	"google_container_cluster": "gcp.container.Cluster",
	"google_storage_bucket":   "gcp.storage.Bucket",
	"google_sql_database_instance": "gcp.sql.DatabaseInstance",
	// Azure (azure-native uses service.Resource, not PascalCased TF suffix)
	"azurerm_resource_group":   "azure.resources.ResourceGroup",
	"azurerm_virtual_network":  "azure.network.VirtualNetwork",
	"azurerm_subnet":           "azure.network.Subnet",
	"azurerm_storage_account":  "azure.storage.StorageAccount",
	"azurerm_linux_virtual_machine": "azure.compute.VirtualMachine",
}

// fallbackPulumiType guesses a Pulumi constructor when there's no
// explicit override. It returns something that compiles even when the
// package name is a guess — the user sees the TS compiler's missing-
// module error and knows to either register an override upstream or
// hand-edit the import.
func fallbackPulumiType(tfType string) string {
	switch {
	case strings.HasPrefix(tfType, "aws_"):
		rest := strings.TrimPrefix(tfType, "aws_")
		return "aws." + guessAWSPackage(rest) + "." + pascalCaseFromSnake(rest)
	case strings.HasPrefix(tfType, "google_"):
		rest := strings.TrimPrefix(tfType, "google_")
		return "gcp." + guessGCPPackage(rest) + "." + pascalCaseFromSnake(rest)
	case strings.HasPrefix(tfType, "azurerm_"):
		rest := strings.TrimPrefix(tfType, "azurerm_")
		// (azure as any) sidesteps the per-package typing for
		// unknown Azure resources. TS still parses the expression
		// and the program compiles; the user hand-fixes the import
		// when they actually wire the resource up. Without the
		// `as any` cast, `azure.fictional.Thing` would hard-fail
		// tsc at the unknown namespace reference.
		return "(azure as any)." + guessAzurePackage(rest) + "." + pascalCaseFromSnake(rest)
	}
	return pascalCaseFromSnake(tfType)
}

// guessAWSPackage picks a reasonable Pulumi AWS subpackage from the
// first token of the resource type's suffix. These are the ~30 most-
// used; unknowns default to "ec2" which at least compiles.
func guessAWSPackage(rest string) string {
	head := strings.SplitN(rest, "_", 2)[0]
	pkgByHead := map[string]string{
		"s3": "s3", "ec2": "ec2", "vpc": "ec2", "subnet": "ec2",
		"rds": "rds", "dynamodb": "dynamodb", "lambda": "lambda",
		"iam": "iam", "kms": "kms", "eks": "eks", "ecs": "ecs",
		"ecr": "ecr", "lb": "lb", "elb": "elb", "cloudwatch": "cloudwatch",
		"sns": "sns", "sqs": "sqs", "apigateway": "apigateway",
	}
	if p, ok := pkgByHead[head]; ok {
		return p
	}
	return "ec2"
}

func guessGCPPackage(rest string) string {
	head := strings.SplitN(rest, "_", 2)[0]
	pkgByHead := map[string]string{
		"storage": "storage", "compute": "compute", "container": "container",
		"sql": "sql", "pubsub": "pubsub", "bigquery": "bigquery",
	}
	if p, ok := pkgByHead[head]; ok {
		return p
	}
	return "compute"
}

// guessAzurePackage maps the first token after "azurerm_" to an
// azure-native subpackage guess. Azure's namespacing doesn't line up
// as cleanly as AWS/GCP — many TF types collapse unrelated services
// (azurerm_key_vault vs. azurerm_storage_account) — so the fallback
// defaults to "resources" which at least parses in TS with the
// `(azure as any)` cast.
func guessAzurePackage(rest string) string {
	head := strings.SplitN(rest, "_", 2)[0]
	pkgByHead := map[string]string{
		"resource": "resources", "storage": "storage", "virtual": "network",
		"subnet": "network", "network": "network", "key": "keyvault",
		"linux": "compute", "windows": "compute", "container": "containerservice",
		"managed": "containerservice", "postgresql": "dbforpostgresql",
		"mysql": "dbformysql", "redis": "cache", "app": "web",
	}
	if p, ok := pkgByHead[head]; ok {
		return p
	}
	return "resources"
}

// pascalCaseFromSnake: "nat_gateway" → "NatGateway". Pulumi
// constructors are PascalCase; Terraform types are snake_case. Drops
// empty segments so double-underscores don't produce an empty camel.
func pascalCaseFromSnake(s string) string {
	parts := strings.Split(s, "_")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		out = append(out, string(runes))
	}
	return strings.Join(out, "")
}

// toCamelCase: "web_server" → "webServer". Used for both variable
// names and property keys; Pulumi's TypeScript SDK follows JS
// conventions so camelCase is idiomatic.
func toCamelCase(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 0 {
		return s
	}
	out := parts[0]
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		out += string(runes)
	}
	return out
}

// sanitizeTSIdent turns an arbitrary resource name into a valid
// TypeScript identifier. camelCase on resource names like
// "<project>_seed" is fine when the project is a valid identifier,
// but project names are allowed to contain hyphens ("acme-infra"),
// which would produce `const acme-infraSeed = …` — parse error. We
// replace any non-[A-Za-z0-9_] with "_" and prefix a "_" when the
// first rune is a digit so the result is always a legal identifier.
func sanitizeTSIdent(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	// Use []rune so a multibyte UTF-8 leading character isn't
	// misidentified by a naive byte check. IsDigit on the first rune
	// decides the underscore prefix.
	runes := []rune(out)
	if len(runes) > 0 && unicode.IsDigit(runes[0]) {
		out = "_" + out
	}
	return out
}

// tsPropValue renders a resource property as TypeScript source. Maps
// and slices recurse; primitive scalars emit their literal form. The
// output is deterministic (sorted map keys) so two runs with the same
// input produce byte-identical programs — important for CI diffing.
//
// parentKey lets callers signal the semantic of the map being rendered:
// map values under "tags" / "labels" / "environment" / similar keep
// their literal key names (those ARE the tag/label/env-var identifier,
// not a Pulumi property name). All other nested maps camelCase their
// keys to match Pulumi TypeScript SDK conventions — a Terraform-style
// nested block like `{network_acl: {cidr_blocks: […]}}` renders as
// `{networkAcl: {cidrBlocks: […]}}`. Pass "" (or use tsPropValueTop)
// at the outermost call site.
func tsPropValue(v any, parentKey string) string {
	switch x := v.(type) {
	case nil:
		return "undefined"
	case bool:
		return fmt.Sprintf("%t", x)
	case int, int32, int64, float32, float64:
		return fmt.Sprintf("%v", x)
	case string:
		return fmt.Sprintf("%q", x)
	case []any:
		items := make([]string, 0, len(x))
		for _, el := range x {
			items = append(items, tsPropValue(el, parentKey))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		// Sort for deterministic output (Go maps iterate randomly).
		sort.Strings(keys)
		preserve := preservesLiteralKeys(parentKey)
		items := make([]string, 0, len(keys))
		for _, k := range keys {
			renderedKey := k
			if !preserve {
				renderedKey = toCamelCase(k)
			}
			items = append(items, fmt.Sprintf("%q: %s", renderedKey, tsPropValue(x[k], k)))
		}
		return "{ " + strings.Join(items, ", ") + " }"
	}
	return fmt.Sprintf("%q", fmt.Sprint(v))
}

// preservesLiteralKeys reports whether a map value whose parent key
// matches one of the known literal-keyed wrappers (tags, labels,
// environment variables, metadata, annotations) should keep its child
// keys verbatim instead of camelCasing them. These are the places
// where the child key IS the real-world tag / label / env-var name,
// not a Pulumi SDK property. Getting this wrong produces subtly
// incorrect infrastructure — e.g. "Owner" → "owner" silently changes
// the tag key in the cloud.
func preservesLiteralKeys(parentKey string) bool {
	switch parentKey {
	case "tags", "labels", "metadata", "annotations",
		"environment", "environment_variables", "env":
		return true
	}
	return false
}

// isTaggableAWS reports whether the given AWS resource type accepts
// a flat map[string]string `tags` property. Allowlist rather than
// denylist: many AWS resources don't accept tags at all
// (aws_route_table_association, aws_iam_instance_profile, …) and a
// previous denylist approach auto-injected tags into them, which
// then fails tsc or the provider at plan time. Unknown types default
// to false — safer to drop the convenience tags than to emit
// resource programs that don't compile.
func isTaggableAWS(tfType string) bool {
	_, ok := taggableAWSResources[tfType]
	return ok
}

// taggableAWSResources lists AWS resource types that accept the flat
// tags = { K: V, ... } shape. Covers the common set the canvas + fall-
// back palette emit — not exhaustive across AWS's ~800 types, but
// extending is a one-line addition as new resources are added to the
// catalog.
var taggableAWSResources = map[string]struct{}{
	"aws_vpc":                   {},
	"aws_subnet":                {},
	"aws_internet_gateway":      {},
	"aws_nat_gateway":           {},
	"aws_security_group":        {},
	"aws_instance":              {},
	"aws_lambda_function":       {},
	"aws_ecs_cluster":           {},
	"aws_eks_cluster":           {},
	"aws_s3_bucket":             {},
	"aws_ebs_volume":            {},
	"aws_ecr_repository":        {},
	"aws_db_instance":           {},
	"aws_dynamodb_table":        {},
	"aws_elasticache_cluster":   {},
	"aws_kms_key":               {},
	"aws_secretsmanager_secret": {},
	"aws_lb":                    {},
	"aws_lb_target_group":       {},
	"aws_autoscaling_group":     {},
	"aws_cloudwatch_log_group":  {},
}
