package agentproviders

const (
	ConnectionCategoryAPI        = "api"
	ConnectionCategoryEnterprise = "enterprise_gateway"

	ConnectionCredentialSecretStore     = "secret_store"
	ConnectionCredentialCloudConnection = "cloud_connection"
	ConnectionCredentialEnterpriseSSO   = "enterprise_sso"
)

// ConnectionProviderDefinition describes an API or enterprise model-provider
// connection path. It is metadata only: credentials are represented as field
// names and storage hints, never as resolved secret values.
type ConnectionProviderDefinition struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Family            string   `json:"family"`
	Category          string   `json:"category"`
	CredentialMode    string   `json:"credential_mode"`
	RequiredFields    []string `json:"required_fields"`
	SecretFields      []string `json:"secret_fields"`
	Capabilities      []string `json:"capabilities"`
	CostControls      []string `json:"cost_controls"`
	BillingHint       string   `json:"billing_hint"`
	DataHandlingHint  string   `json:"data_handling_hint"`
	SecretStorageHint string   `json:"secret_storage_hint"`
	SetupHint         string   `json:"setup_hint"`
}

func DefaultConnectionProviders() []ConnectionProviderDefinition {
	return []ConnectionProviderDefinition{
		{
			ID:             "openai-api",
			Name:           "OpenAI API",
			Family:         "openai",
			Category:       ConnectionCategoryAPI,
			CredentialMode: ConnectionCredentialSecretStore,
			RequiredFields: []string{"model"},
			SecretFields:   []string{"api_key"},
			Capabilities: []string{
				"chat",
				"code_editing",
				"iac_assistance",
				"tool_calling",
				"vision",
			},
			CostControls: []string{
				"monthly_budget",
				"per_run_token_limit",
				"allowed_models",
			},
			BillingHint:       "Billed through the OpenAI Platform API account, separate from ChatGPT subscriptions.",
			DataHandlingHint:  "Prompts and selected project context are sent to the configured OpenAI API endpoint.",
			SecretStorageHint: "Store API keys through IaC Studio secret stores; keys are never returned to the browser after save.",
			SetupHint:         "Use for automation, hosted workflows, or centrally billed platform usage.",
		},
		{
			ID:             "anthropic-api",
			Name:           "Anthropic API",
			Family:         "anthropic",
			Category:       ConnectionCategoryAPI,
			CredentialMode: ConnectionCredentialSecretStore,
			RequiredFields: []string{"model"},
			SecretFields:   []string{"api_key"},
			Capabilities: []string{
				"chat",
				"code_editing",
				"iac_assistance",
				"tool_calling",
				"vision",
			},
			CostControls: []string{
				"monthly_budget",
				"per_run_token_limit",
				"allowed_models",
			},
			BillingHint:       "Billed through Anthropic API usage, separate from Claude or Claude Code subscriptions.",
			DataHandlingHint:  "Prompts and selected project context are sent to the configured Anthropic API endpoint.",
			SecretStorageHint: "Store API keys through IaC Studio secret stores; keys are never returned to the browser after save.",
			SetupHint:         "Use for hosted runs, service accounts, or team automation.",
		},
		{
			ID:             "azure-openai",
			Name:           "Azure OpenAI",
			Family:         "openai",
			Category:       ConnectionCategoryAPI,
			CredentialMode: ConnectionCredentialSecretStore,
			RequiredFields: []string{"endpoint", "deployment"},
			SecretFields:   []string{"api_key"},
			Capabilities: []string{
				"chat",
				"code_editing",
				"iac_assistance",
				"tool_calling",
			},
			CostControls: []string{
				"monthly_budget",
				"deployment_allowlist",
				"per_run_token_limit",
			},
			BillingHint:       "Billed through the selected Azure subscription and Azure OpenAI resource.",
			DataHandlingHint:  "Prompts and selected project context are sent to the configured Azure OpenAI endpoint.",
			SecretStorageHint: "Store Azure OpenAI keys through IaC Studio secret stores or a future Azure Key Vault adapter.",
			SetupHint:         "Use when teams need Azure tenant controls, private networking, or centralized Azure billing.",
		},
		{
			ID:             "aws-bedrock",
			Name:           "Amazon Bedrock",
			Family:         "bedrock",
			Category:       ConnectionCategoryAPI,
			CredentialMode: ConnectionCredentialCloudConnection,
			RequiredFields: []string{"region", "model_id", "cloud_connection_id"},
			SecretFields:   []string{},
			Capabilities: []string{
				"chat",
				"iac_assistance",
				"tool_calling",
			},
			CostControls: []string{
				"monthly_budget",
				"allowed_model_ids",
				"per_run_token_limit",
			},
			BillingHint:       "Billed through the selected AWS account and Bedrock model usage.",
			DataHandlingHint:  "Prompts and selected project context are sent to Bedrock in the selected AWS region.",
			SecretStorageHint: "Reuse an approved AWS Cloud Connection instead of storing model-provider keys.",
			SetupHint:         "Use for AWS-governed deployments that need IAM, region, and account controls.",
		},
		{
			ID:             "vertex-ai",
			Name:           "Vertex AI",
			Family:         "vertex",
			Category:       ConnectionCategoryAPI,
			CredentialMode: ConnectionCredentialCloudConnection,
			RequiredFields: []string{"project_id", "location", "model", "cloud_connection_id"},
			SecretFields:   []string{},
			Capabilities: []string{
				"chat",
				"iac_assistance",
				"tool_calling",
				"vision",
			},
			CostControls: []string{
				"monthly_budget",
				"allowed_models",
				"per_run_token_limit",
			},
			BillingHint:       "Billed through the selected Google Cloud project and Vertex AI usage.",
			DataHandlingHint:  "Prompts and selected project context are sent to Vertex AI in the selected project and location.",
			SecretStorageHint: "Reuse an approved GCP Cloud Connection instead of storing model-provider keys.",
			SetupHint:         "Use for GCP-governed deployments that need project, region, and IAM controls.",
		},
		{
			ID:             "enterprise-gateway",
			Name:           "Enterprise Gateway",
			Family:         "gateway",
			Category:       ConnectionCategoryEnterprise,
			CredentialMode: ConnectionCredentialEnterpriseSSO,
			RequiredFields: []string{"endpoint", "tenant"},
			SecretFields:   []string{},
			Capabilities: []string{
				"chat",
				"code_editing",
				"iac_assistance",
				"tool_calling",
				"audit_controls",
				"policy_routing",
			},
			CostControls: []string{
				"workspace_budget",
				"allowed_models",
				"team_quota",
			},
			BillingHint:       "Billed through the organization's gateway or enterprise model platform.",
			DataHandlingHint:  "Prompts and selected project context follow the configured enterprise gateway routing policy.",
			SecretStorageHint: "Use SSO or gateway-managed credentials; IaC Studio should not collect individual API keys for this path.",
			SetupHint:         "Use for private routing, SSO, audit, and platform-team rollouts.",
		},
	}
}
