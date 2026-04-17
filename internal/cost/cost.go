package cost

import (
	"fmt"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/plan"
)

// Estimator provides rough cost estimates for infrastructure resources.
// This uses a built-in pricing table (no API keys, no cloud calls) so it works
// offline and gives instant feedback. Prices are approximate and US-East-1 based.
//
// The goal isn't exact billing — it's "will this cost $50/month or $5,000/month?"
// so engineers catch cost surprises before terraform apply.
type Estimator struct {
	region string
	prices map[string]ResourcePricing
}

// ResourcePricing holds the cost model for one resource type.
type ResourcePricing struct {
	Type        string  `json:"type"`
	BaseCost    float64 `json:"base_cost"`     // monthly base cost in USD
	Unit        string  `json:"unit"`           // "per hour", "per GB/month", "per request"
	HourlyCost  float64 `json:"hourly_cost"`
	MonthlyCost float64 `json:"monthly_cost"`
	Notes       string  `json:"notes"`
	SizeField   string  `json:"size_field"`     // which field affects pricing (e.g., "instance_type")
}

// CostEstimate is the full cost breakdown for a project.
type CostEstimate struct {
	Resources     []ResourceCost `json:"resources"`
	TotalMonthly  float64        `json:"total_monthly"`
	TotalYearly   float64        `json:"total_yearly"`
	Currency      string         `json:"currency"`
	Region        string         `json:"region"`
	Disclaimer    string         `json:"disclaimer"`
}

// ResourceCost is the cost estimate for a single resource.
type ResourceCost struct {
	Address      string  `json:"address"`      // aws_instance.web
	Type         string  `json:"type"`
	Name         string  `json:"name"`
	MonthlyCost  float64 `json:"monthly_cost"`
	HourlyCost   float64 `json:"hourly_cost"`
	Details      string  `json:"details"`       // "t3.large @ $0.0832/hr"
	IsFree       bool    `json:"is_free"`       // some resources are free-tier
}

// CostDelta shows the cost impact of a plan.
type CostDelta struct {
	CurrentMonthly  float64           `json:"current_monthly"`
	ProposedMonthly float64           `json:"proposed_monthly"`
	DeltaMonthly    float64           `json:"delta_monthly"`  // positive = more expensive
	DeltaYearly     float64           `json:"delta_yearly"`
	ByAction        map[string]float64 `json:"by_action"`     // "create" -> +$150, "delete" -> -$50
	Details         []CostChangeDetail `json:"details"`
}

// CostChangeDetail is the cost impact of one resource change.
type CostChangeDetail struct {
	Address  string  `json:"address"`
	Action   string  `json:"action"`
	OldCost  float64 `json:"old_cost"`
	NewCost  float64 `json:"new_cost"`
	Delta    float64 `json:"delta"`
}

func New(region string) *Estimator {
	e := &Estimator{
		region: region,
		prices: make(map[string]ResourcePricing),
	}
	e.loadPricing()
	return e
}

// EstimateProject calculates the total monthly cost for all resources.
func (e *Estimator) EstimateProject(resources []parser.Resource) *CostEstimate {
	estimate := &CostEstimate{
		Currency:   "USD",
		Region:     e.region,
		Disclaimer: "Estimates are approximate (us-east-1 on-demand pricing). Actual costs depend on usage, data transfer, and reserved capacity.",
	}

	for _, r := range resources {
		rc := e.estimateResource(r)
		estimate.Resources = append(estimate.Resources, rc)
		estimate.TotalMonthly += rc.MonthlyCost
	}

	estimate.TotalYearly = estimate.TotalMonthly * 12
	return estimate
}

// EstimatePlanDelta calculates the cost delta of a terraform plan.
func (e *Estimator) EstimatePlanDelta(planResult *plan.PlanResult, currentResources []parser.Resource) *CostDelta {
	delta := &CostDelta{
		ByAction: make(map[string]float64),
	}

	// Current cost
	for _, r := range currentResources {
		rc := e.estimateResource(r)
		delta.CurrentMonthly += rc.MonthlyCost
	}

	delta.ProposedMonthly = delta.CurrentMonthly

	for _, change := range planResult.Changes {
		pricing, ok := e.prices[change.Type]
		if !ok {
			continue
		}

		detail := CostChangeDetail{
			Address: change.Address,
			Action:  change.Action,
		}

		switch change.Action {
		case "create":
			cost := e.costForProperties(pricing, change.After)
			detail.NewCost = cost
			detail.Delta = cost
			delta.ProposedMonthly += cost

		case "delete":
			cost := e.costForProperties(pricing, change.Before)
			detail.OldCost = cost
			detail.Delta = -cost
			delta.ProposedMonthly -= cost

		case "update":
			oldCost := e.costForProperties(pricing, change.Before)
			newCost := e.costForProperties(pricing, change.After)
			detail.OldCost = oldCost
			detail.NewCost = newCost
			detail.Delta = newCost - oldCost
			delta.ProposedMonthly += (newCost - oldCost)

		case "replace":
			oldCost := e.costForProperties(pricing, change.Before)
			newCost := e.costForProperties(pricing, change.After)
			detail.OldCost = oldCost
			detail.NewCost = newCost
			detail.Delta = newCost - oldCost
			delta.ProposedMonthly += (newCost - oldCost)
		}

		delta.ByAction[change.Action] += detail.Delta
		delta.Details = append(delta.Details, detail)
	}

	delta.DeltaMonthly = delta.ProposedMonthly - delta.CurrentMonthly
	delta.DeltaYearly = delta.DeltaMonthly * 12

	return delta
}

func (e *Estimator) estimateResource(r parser.Resource) ResourceCost {
	rc := ResourceCost{
		Address: r.Type + "." + r.Name,
		Type:    r.Type,
		Name:    r.Name,
	}

	pricing, ok := e.prices[r.Type]
	if !ok {
		rc.IsFree = true
		rc.Details = "no pricing data (may be free or usage-based)"
		return rc
	}

	rc.MonthlyCost = e.costForProperties(pricing, r.Properties)
	rc.HourlyCost = rc.MonthlyCost / 730 // ~730 hours per month
	rc.Details = pricing.Notes

	if rc.MonthlyCost == 0 {
		rc.IsFree = true
	}

	return rc
}

func (e *Estimator) costForProperties(pricing ResourcePricing, props map[string]interface{}) float64 {
	if props == nil {
		return pricing.MonthlyCost
	}

	// Check if there's a size field that affects pricing
	if pricing.SizeField != "" {
		if val, ok := props[pricing.SizeField]; ok {
			sizeStr := fmt.Sprintf("%v", val)
			if cost, ok := instancePricing[sizeStr]; ok {
				return cost * 730 // hourly to monthly
			}
		}
	}

	return pricing.MonthlyCost
}

// instancePricing maps EC2 instance types to hourly on-demand costs (us-east-1).
var instancePricing = map[string]float64{
	// General purpose
	"t3.nano":     0.0052,
	"t3.micro":    0.0104,
	"t3.small":    0.0208,
	"t3.medium":   0.0416,
	"t3.large":    0.0832,
	"t3.xlarge":   0.1664,
	"t3.2xlarge":  0.3328,
	// M-class
	"m5.large":    0.096,
	"m5.xlarge":   0.192,
	"m5.2xlarge":  0.384,
	"m5.4xlarge":  0.768,
	"m6i.large":   0.096,
	"m6i.xlarge":  0.192,
	"m6i.2xlarge": 0.384,
	// Compute optimized
	"c5.large":    0.085,
	"c5.xlarge":   0.170,
	"c5.2xlarge":  0.340,
	"c6i.large":   0.085,
	"c6i.xlarge":  0.170,
	// Memory optimized
	"r5.large":    0.126,
	"r5.xlarge":   0.252,
	"r5.2xlarge":  0.504,
	"r6i.large":   0.126,
	"r6i.xlarge":  0.252,
}

// rdsPricing maps RDS instance types to hourly costs.
var rdsPricing = map[string]float64{
	"db.t3.micro":   0.017,
	"db.t3.small":   0.034,
	"db.t3.medium":  0.068,
	"db.t3.large":   0.136,
	"db.m5.large":   0.171,
	"db.m5.xlarge":  0.342,
	"db.m5.2xlarge": 0.684,
	"db.r5.large":   0.240,
	"db.r5.xlarge":  0.480,
}

func (e *Estimator) loadPricing() {
	e.prices = map[string]ResourcePricing{
		// --- COMPUTE ---
		"aws_instance": {
			Type: "aws_instance", MonthlyCost: 60.74, // t3.large default
			SizeField: "instance_type",
			Notes: "EC2 on-demand (price varies by instance_type)",
		},
		"aws_lambda_function": {
			Type: "aws_lambda_function", MonthlyCost: 0,
			Notes: "free tier: 1M requests + 400K GB-seconds/month, then ~$0.20/1M requests",
		},
		"aws_ecs_service": {
			Type: "aws_ecs_service", MonthlyCost: 0,
			Notes: "Fargate pricing depends on vCPU/memory allocation",
		},
		"aws_autoscaling_group": {
			Type: "aws_autoscaling_group", MonthlyCost: 0,
			Notes: "no charge — you pay for the EC2 instances it manages",
		},

		// --- DATABASE ---
		"aws_db_instance": {
			Type: "aws_db_instance", MonthlyCost: 24.82, // db.t3.small default
			SizeField: "instance_class",
			Notes: "RDS on-demand (price varies by instance_class + engine)",
		},
		"aws_elasticache_cluster": {
			Type: "aws_elasticache_cluster", MonthlyCost: 24.82,
			SizeField: "node_type",
			Notes: "ElastiCache on-demand pricing",
		},
		"aws_dynamodb_table": {
			Type: "aws_dynamodb_table", MonthlyCost: 0,
			Notes: "on-demand: $1.25/million writes, $0.25/million reads; provisioned varies",
		},

		// --- STORAGE ---
		"aws_s3_bucket": {
			Type: "aws_s3_bucket", MonthlyCost: 0,
			Notes: "$0.023/GB/month for Standard — depends on usage",
		},
		"aws_ebs_volume": {
			Type: "aws_ebs_volume", MonthlyCost: 8.00,
			Notes: "gp3: $0.08/GB/month (100GB default)",
		},

		// --- NETWORKING ---
		"aws_vpc": {
			Type: "aws_vpc", MonthlyCost: 0,
			Notes: "no charge for VPC itself",
		},
		"aws_subnet": {
			Type: "aws_subnet", MonthlyCost: 0,
			Notes: "no charge for subnets",
		},
		"aws_internet_gateway": {
			Type: "aws_internet_gateway", MonthlyCost: 0,
			Notes: "no charge — you pay for data transfer",
		},
		"aws_nat_gateway": {
			Type: "aws_nat_gateway", MonthlyCost: 32.40,
			Notes: "$0.045/hr + $0.045/GB processed",
		},
		"aws_lb": {
			Type: "aws_lb", MonthlyCost: 16.20,
			Notes: "ALB: $0.0225/hr + $0.008/LCU-hr",
		},
		"aws_security_group": {
			Type: "aws_security_group", MonthlyCost: 0,
			Notes: "no charge",
		},
		"aws_route_table": {
			Type: "aws_route_table", MonthlyCost: 0,
			Notes: "no charge",
		},
		"aws_eip": {
			Type: "aws_eip", MonthlyCost: 3.60,
			Notes: "$0.005/hr when not attached to running instance",
		},
		"aws_cloudfront_distribution": {
			Type: "aws_cloudfront_distribution", MonthlyCost: 0,
			Notes: "pay per request + data transfer — free tier: 1TB/month",
		},
		"aws_route53_zone": {
			Type: "aws_route53_zone", MonthlyCost: 0.50,
			Notes: "$0.50/hosted zone/month",
		},

		// --- IAM/SECURITY ---
		"aws_iam_role": {
			Type: "aws_iam_role", MonthlyCost: 0,
			Notes: "no charge",
		},
		"aws_iam_policy": {
			Type: "aws_iam_policy", MonthlyCost: 0,
			Notes: "no charge",
		},
		"aws_kms_key": {
			Type: "aws_kms_key", MonthlyCost: 1.00,
			Notes: "$1/key/month + $0.03/10K requests",
		},
		"aws_acm_certificate": {
			Type: "aws_acm_certificate", MonthlyCost: 0,
			Notes: "free for public certificates",
		},

		// --- MONITORING ---
		"aws_cloudwatch_log_group": {
			Type: "aws_cloudwatch_log_group", MonthlyCost: 0,
			Notes: "$0.50/GB ingested + $0.03/GB stored",
		},
		"aws_sns_topic": {
			Type: "aws_sns_topic", MonthlyCost: 0,
			Notes: "free tier: 1M publishes/month",
		},
		"aws_sqs_queue": {
			Type: "aws_sqs_queue", MonthlyCost: 0,
			Notes: "free tier: 1M requests/month",
		},

		// --- SERVERLESS ---
		"aws_api_gateway_rest_api": {
			Type: "aws_api_gateway_rest_api", MonthlyCost: 0,
			Notes: "$3.50/million API calls + data transfer",
		},
	}

	// Add RDS instance-type-specific pricing
	for k, v := range rdsPricing {
		instancePricing[k] = v
	}
}

// FormatCost returns a human-friendly cost string.
func FormatCost(monthly float64) string {
	if monthly == 0 {
		return "Free / Usage-based"
	}
	if monthly < 1 {
		return fmt.Sprintf("~$%.2f/mo", monthly)
	}
	return fmt.Sprintf("~$%.0f/mo", monthly)
}

// FormatDelta returns a human-friendly cost delta string.
func FormatDelta(delta float64) string {
	if delta == 0 {
		return "no cost change"
	}
	sign := "+"
	if delta < 0 {
		sign = "-"
		delta = -delta
	}
	if delta < 1 {
		return fmt.Sprintf("%s$%.2f/mo", sign, delta)
	}
	return fmt.Sprintf("%s$%.0f/mo", sign, delta)
}

