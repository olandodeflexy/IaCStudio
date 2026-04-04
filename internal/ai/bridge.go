package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// OllamaClient communicates with a local Ollama instance.
type OllamaClient struct {
	endpoint string
	model    string
	client   *http.Client
}

func NewOllamaClient(endpoint, model string) *OllamaClient {
	return &OllamaClient{
		endpoint: endpoint,
		model:    model,
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Format string `json:"format"`
}

type ollamaResponse struct {
	Response string `json:"response"`
}

// ChatMessage represents one message in a conversation.
type ChatMessage struct {
	Role    string `json:"role"`    // "user" or "ai"
	Content string `json:"content"`
}

// ChatRequest is the full request from the frontend including conversation context.
type ChatRequest struct {
	Message  string            `json:"message"`
	Tool     string            `json:"tool"`    // terraform | opentofu | ansible
	Provider string            `json:"provider"` // aws | google | azurerm (auto-detected)
	History  []ChatMessage     `json:"history"`  // conversation history for context
	Canvas   []CanvasResource  `json:"canvas"`   // what's currently on the canvas
}

// CanvasResource is a simplified view of what's on the canvas for AI context.
type CanvasResource struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// GenerateIaC sends a natural language request to Ollama with full conversation
// context, provider awareness, and canvas state.
func (c *OllamaClient) GenerateIaC(ctx context.Context, req ChatRequest) (string, []parser.Resource, error) {
	systemPrompt := buildSystemPrompt(req.Tool, req.Provider, req.Canvas)

	// Build conversation context
	var conversationParts []string
	for _, msg := range req.History {
		if msg.Role == "user" {
			conversationParts = append(conversationParts, fmt.Sprintf("User: %s", msg.Content))
		} else {
			conversationParts = append(conversationParts, fmt.Sprintf("Assistant: %s", msg.Content))
		}
	}

	var prompt string
	if len(conversationParts) > 0 {
		// Include last 6 messages of conversation for context
		history := conversationParts
		if len(history) > 6 {
			history = history[len(history)-6:]
		}
		prompt = fmt.Sprintf("%s\n\nConversation so far:\n%s\n\nUser request: %s\n\nRespond with JSON only.",
			systemPrompt, strings.Join(history, "\n"), req.Message)
	} else {
		prompt = fmt.Sprintf("%s\n\nUser request: %s\n\nRespond with JSON only.", systemPrompt, req.Message)
	}

	reqBody := ollamaRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
		Format: "json",
	}

	body, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("ollama unavailable: %w", err)
	}
	defer resp.Body.Close()

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", nil, err
	}

	return parseAIResponse(ollamaResp.Response)
}

// GenerateIaCSimple is the legacy single-message interface (used by pattern matching fallback).
func (c *OllamaClient) GenerateIaCSimple(ctx context.Context, message, tool string) (string, []parser.Resource, error) {
	return c.GenerateIaC(ctx, ChatRequest{
		Message: message,
		Tool:    tool,
	})
}

func buildSystemPrompt(tool, provider string, canvas []CanvasResource) string {
	// Determine the correct provider context
	providerGuide := buildProviderGuide(tool, provider)

	// Build canvas context so the AI knows what exists
	canvasContext := ""
	if len(canvas) > 0 {
		var items []string
		for _, r := range canvas {
			items = append(items, fmt.Sprintf("  - %s.%s", r.Type, r.Name))
		}
		canvasContext = fmt.Sprintf(`

EXISTING RESOURCES ON CANVAS (do not duplicate these):
%s

When the user asks for follow-up resources, build on what already exists.
Reference existing resources by their type.name when creating connections.`, strings.Join(items, "\n"))
	}

	return fmt.Sprintf(`You are an Infrastructure as Code assistant for %s.

CRITICAL RULES:
1. ONLY use %s resource types. NEVER mix providers in a single response.
2. Follow the user's conversation context — if they started with a specific cloud provider, STAY with that provider.
3. If resources already exist on the canvas, build on them rather than creating duplicates.

%s%s

When the user describes infrastructure, respond with a JSON object:
{
  "message": "Brief explanation of what you created and why",
  "resources": [
    {
      "type": "resource_type",
      "name": "descriptive_name",
      "properties": {"key": "value"}
    }
  ]
}

IMPORTANT:
- Use descriptive snake_case names (web_server, not main or default)
- Include sensible default properties for each resource
- If the user asks a question rather than requesting resources, set "resources" to an empty array
- Only respond with valid JSON. No markdown, no code fences.`, tool, providerGuide, providerGuide, canvasContext)
}

func buildProviderGuide(tool, provider string) string {
	if tool == "ansible" {
		return `Ansible modules. Use official module names:
- Package management: apt, yum, dnf, pip
- System: service, systemd, user, cron, hostname
- Files: copy, template, file, lineinfile
- Containers: docker_container, docker_image, k8s
- Cloud (AWS): amazon.aws.ec2_instance, amazon.aws.s3_bucket
- Cloud (GCP): google.cloud.gcp_compute_instance
- Cloud (Azure): azure.azcollection.azure_rm_virtualmachine`
	}

	switch provider {
	case "google":
		return `Google Cloud (GCP) resource types ONLY. Examples:
- Networking: google_compute_network, google_compute_subnetwork, google_compute_firewall, google_compute_router
- Compute: google_compute_instance, google_container_cluster, google_cloud_run_service, google_cloudfunctions_function
- Storage: google_storage_bucket, google_compute_disk
- Database: google_sql_database_instance, google_redis_instance, google_spanner_instance, google_firestore_database
- Security: google_service_account, google_kms_key_ring, google_secret_manager_secret
- Messaging: google_pubsub_topic, google_pubsub_subscription
- Data: google_bigquery_dataset, google_bigquery_table
NEVER use aws_ or azurerm_ prefixed resources`

	case "azurerm":
		return `Azure resource types ONLY. Examples:
- Core: azurerm_resource_group (required for all Azure resources)
- Networking: azurerm_virtual_network, azurerm_subnet, azurerm_network_security_group, azurerm_public_ip
- Compute: azurerm_linux_virtual_machine, azurerm_kubernetes_cluster, azurerm_function_app, azurerm_linux_web_app
- Storage: azurerm_storage_account, azurerm_storage_container
- Database: azurerm_mssql_server, azurerm_mssql_database, azurerm_postgresql_flexible_server, azurerm_cosmosdb_account
- Security: azurerm_key_vault, azurerm_user_assigned_identity
ALWAYS create an azurerm_resource_group first. NEVER use aws_ or google_ prefixed resources`

	default: // aws is default
		return `AWS resource types ONLY. Examples:
- Networking: aws_vpc, aws_subnet, aws_internet_gateway, aws_nat_gateway, aws_security_group, aws_route_table
- Compute: aws_instance, aws_lambda_function, aws_ecs_cluster, aws_eks_cluster
- Storage: aws_s3_bucket, aws_ebs_volume, aws_ecr_repository
- Database: aws_db_instance, aws_dynamodb_table, aws_elasticache_cluster, aws_rds_cluster
- Security: aws_iam_role, aws_iam_policy, aws_kms_key, aws_secretsmanager_secret
- Load Balancing: aws_lb, aws_lb_target_group
NEVER use google_ or azurerm_ prefixed resources`
	}
}

func parseAIResponse(raw string) (string, []parser.Resource, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result struct {
		Message   string `json:"message"`
		Resources []struct {
			Type       string                 `json:"type"`
			Name       string                 `json:"name"`
			Properties map[string]interface{} `json:"properties"`
		} `json:"resources"`
	}

	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return raw, nil, nil
	}

	var resources []parser.Resource
	for i, r := range result.Resources {
		resources = append(resources, parser.Resource{
			ID:         fmt.Sprintf("ai_%d_%d", time.Now().Unix(), i),
			Type:       r.Type,
			Name:       r.Name,
			Properties: r.Properties,
		})
	}

	return result.Message, resources, nil
}

// ─── Fallback Pattern Matching (when Ollama is unavailable) ───

// PatternMatch provides basic resource generation without an AI model.
// Now provider-aware — generates correct resource types per cloud.
func PatternMatch(message, tool, provider string) (string, []parser.Resource) {
	msg := strings.ToLower(message)

	if tool == "ansible" {
		return patternMatchAnsible(msg)
	}

	switch provider {
	case "google":
		return patternMatchGCP(msg)
	case "azurerm":
		return patternMatchAzure(msg)
	default:
		return patternMatchAWS(msg)
	}
}

func patternMatchAWS(msg string) (string, []parser.Resource) {
	patterns := []struct {
		keywords []string
		resource parser.Resource
		response string
	}{
		{[]string{"vpc", "network"}, parser.Resource{Type: "aws_vpc", Name: "main_vpc", Properties: map[string]interface{}{
			"cidr_block": "10.0.0.0/16", "enable_dns_support": true, "enable_dns_hostnames": true,
		}}, "Added an AWS VPC with DNS support enabled."},
		{[]string{"subnet"}, parser.Resource{Type: "aws_subnet", Name: "main_subnet", Properties: map[string]interface{}{
			"cidr_block": "10.0.1.0/24", "availability_zone": "us-east-1a",
		}}, "Added an AWS subnet in us-east-1a."},
		{[]string{"ec2", "instance", "server", "vm"}, parser.Resource{Type: "aws_instance", Name: "web_server", Properties: map[string]interface{}{
			"ami": "ami-0c55b159cbfafe1f0", "instance_type": "t3.micro",
		}}, "Added an EC2 instance (t3.micro)."},
		{[]string{"s3", "bucket", "storage"}, parser.Resource{Type: "aws_s3_bucket", Name: "app_storage", Properties: map[string]interface{}{
			"bucket": "my-app-storage",
		}}, "Added an S3 bucket with private access."},
		{[]string{"database", "rds", "db", "postgres", "mysql"}, parser.Resource{Type: "aws_db_instance", Name: "main_db", Properties: map[string]interface{}{
			"engine": "postgres", "engine_version": "15.4", "instance_class": "db.t3.micro", "allocated_storage": 20,
		}}, "Added a PostgreSQL RDS instance."},
		{[]string{"lambda", "function", "serverless"}, parser.Resource{Type: "aws_lambda_function", Name: "api_handler", Properties: map[string]interface{}{
			"function_name": "api_handler", "runtime": "nodejs20.x", "memory_size": 256,
		}}, "Added a Lambda function with Node.js 20."},
		{[]string{"load balancer", "alb", "elb"}, parser.Resource{Type: "aws_lb", Name: "web_alb", Properties: map[string]interface{}{
			"name": "web-alb", "internal": false, "load_balancer_type": "application",
		}}, "Added an Application Load Balancer."},
		{[]string{"security group", "firewall", "sg"}, parser.Resource{Type: "aws_security_group", Name: "web_sg", Properties: map[string]interface{}{
			"name": "web-sg", "description": "Allow HTTP/HTTPS traffic",
		}}, "Added a security group. Configure ingress/egress rules in properties."},
		{[]string{"eks", "kubernetes", "k8s"}, parser.Resource{Type: "aws_eks_cluster", Name: "main_cluster", Properties: map[string]interface{}{
			"name": "main-cluster", "version": "1.29",
		}}, "Added an EKS cluster."},
	}
	return matchPatterns(msg, patterns)
}

func patternMatchGCP(msg string) (string, []parser.Resource) {
	patterns := []struct {
		keywords []string
		resource parser.Resource
		response string
	}{
		{[]string{"vpc", "network"}, parser.Resource{Type: "google_compute_network", Name: "main_vpc", Properties: map[string]interface{}{
			"name": "main-vpc", "auto_create_subnetworks": false,
		}}, "Added a GCP VPC network."},
		{[]string{"subnet"}, parser.Resource{Type: "google_compute_subnetwork", Name: "main_subnet", Properties: map[string]interface{}{
			"name": "main-subnet", "ip_cidr_range": "10.0.1.0/24", "region": "us-central1",
		}}, "Added a GCP subnet in us-central1."},
		{[]string{"vm", "instance", "server", "compute"}, parser.Resource{Type: "google_compute_instance", Name: "web_server", Properties: map[string]interface{}{
			"name": "web-server", "machine_type": "e2-medium", "zone": "us-central1-a",
		}}, "Added a GCP VM instance (e2-medium)."},
		{[]string{"gke", "kubernetes", "k8s", "cluster"}, parser.Resource{Type: "google_container_cluster", Name: "main_cluster", Properties: map[string]interface{}{
			"name": "main-cluster", "location": "us-central1", "initial_node_count": 3,
		}}, "Added a GKE cluster with 3 nodes."},
		{[]string{"cloud run", "serverless", "run"}, parser.Resource{Type: "google_cloud_run_service", Name: "api_service", Properties: map[string]interface{}{
			"name": "api-service", "location": "us-central1",
		}}, "Added a Cloud Run service."},
		{[]string{"function", "cloud function"}, parser.Resource{Type: "google_cloudfunctions_function", Name: "api_function", Properties: map[string]interface{}{
			"name": "api-function", "runtime": "nodejs20", "entry_point": "handler",
		}}, "Added a Cloud Function with Node.js 20."},
		{[]string{"bucket", "storage", "gcs"}, parser.Resource{Type: "google_storage_bucket", Name: "app_storage", Properties: map[string]interface{}{
			"name": "my-app-storage", "location": "US", "uniform_bucket_level_access": true,
		}}, "Added a Cloud Storage bucket."},
		{[]string{"sql", "database", "db", "postgres", "mysql"}, parser.Resource{Type: "google_sql_database_instance", Name: "main_db", Properties: map[string]interface{}{
			"name": "main-db", "database_version": "POSTGRES_15", "region": "us-central1",
		}}, "Added a Cloud SQL PostgreSQL instance."},
		{[]string{"firewall", "rule"}, parser.Resource{Type: "google_compute_firewall", Name: "allow_http", Properties: map[string]interface{}{
			"name": "allow-http", "direction": "INGRESS",
		}}, "Added a firewall rule."},
		{[]string{"pubsub", "topic", "queue", "messaging"}, parser.Resource{Type: "google_pubsub_topic", Name: "events", Properties: map[string]interface{}{
			"name": "events",
		}}, "Added a Pub/Sub topic."},
	}
	return matchPatterns(msg, patterns)
}

func patternMatchAzure(msg string) (string, []parser.Resource) {
	patterns := []struct {
		keywords []string
		resource parser.Resource
		response string
	}{
		{[]string{"resource group", "rg"}, parser.Resource{Type: "azurerm_resource_group", Name: "main_rg", Properties: map[string]interface{}{
			"name": "main-rg", "location": "eastus",
		}}, "Added an Azure Resource Group in East US."},
		{[]string{"vnet", "network", "vpc"}, parser.Resource{Type: "azurerm_virtual_network", Name: "main_vnet", Properties: map[string]interface{}{
			"name": "main-vnet", "location": "eastus", "address_space": "10.0.0.0/16",
		}}, "Added an Azure Virtual Network."},
		{[]string{"subnet"}, parser.Resource{Type: "azurerm_subnet", Name: "main_subnet", Properties: map[string]interface{}{
			"name": "main-subnet", "address_prefixes": "10.0.1.0/24",
		}}, "Added an Azure subnet."},
		{[]string{"vm", "virtual machine", "server", "instance"}, parser.Resource{Type: "azurerm_linux_virtual_machine", Name: "web_server", Properties: map[string]interface{}{
			"name": "web-server", "location": "eastus", "size": "Standard_B2s", "admin_username": "azureuser",
		}}, "Added an Azure Linux VM (Standard_B2s)."},
		{[]string{"aks", "kubernetes", "k8s", "cluster"}, parser.Resource{Type: "azurerm_kubernetes_cluster", Name: "main_aks", Properties: map[string]interface{}{
			"name": "main-aks", "location": "eastus", "dns_prefix": "mainaks",
		}}, "Added an AKS cluster."},
		{[]string{"function", "serverless"}, parser.Resource{Type: "azurerm_function_app", Name: "api_func", Properties: map[string]interface{}{
			"name": "api-func", "location": "eastus",
		}}, "Added an Azure Function App."},
		{[]string{"storage", "blob", "bucket"}, parser.Resource{Type: "azurerm_storage_account", Name: "app_storage", Properties: map[string]interface{}{
			"name": "appstorageacct", "location": "eastus", "account_tier": "Standard", "account_replication_type": "LRS",
		}}, "Added an Azure Storage Account."},
		{[]string{"sql", "database", "db"}, parser.Resource{Type: "azurerm_mssql_database", Name: "main_db", Properties: map[string]interface{}{
			"name": "main-db", "sku_name": "S0",
		}}, "Added an Azure SQL Database."},
		{[]string{"cosmos", "nosql", "mongo"}, parser.Resource{Type: "azurerm_cosmosdb_account", Name: "main_cosmos", Properties: map[string]interface{}{
			"name": "main-cosmos", "location": "eastus", "offer_type": "Standard", "kind": "GlobalDocumentDB",
		}}, "Added a Cosmos DB account."},
		{[]string{"key vault", "secrets", "vault"}, parser.Resource{Type: "azurerm_key_vault", Name: "main_kv", Properties: map[string]interface{}{
			"name": "main-kv", "location": "eastus", "sku_name": "standard",
		}}, "Added an Azure Key Vault."},
		{[]string{"nsg", "security group", "firewall"}, parser.Resource{Type: "azurerm_network_security_group", Name: "web_nsg", Properties: map[string]interface{}{
			"name": "web-nsg", "location": "eastus",
		}}, "Added a Network Security Group."},
	}
	return matchPatterns(msg, patterns)
}

func patternMatchAnsible(msg string) (string, []parser.Resource) {
	patterns := []struct {
		keywords []string
		resource parser.Resource
		response string
	}{
		{[]string{"nginx", "web server"}, parser.Resource{Type: "apt", Name: "Install nginx", Properties: map[string]interface{}{
			"name": "nginx", "state": "present", "update_cache": true,
		}}, "Added nginx installation task."},
		{[]string{"docker", "container"}, parser.Resource{Type: "docker_container", Name: "Run container", Properties: map[string]interface{}{
			"name": "webapp", "image": "nginx:latest", "ports": "80:80",
		}}, "Added a Docker container task."},
		{[]string{"user", "account"}, parser.Resource{Type: "user", Name: "Create user", Properties: map[string]interface{}{
			"name": "deploy", "shell": "/bin/bash", "groups": "sudo",
		}}, "Added user creation task."},
		{[]string{"copy", "file"}, parser.Resource{Type: "copy", Name: "Copy config", Properties: map[string]interface{}{
			"src": "files/app.conf", "dest": "/etc/app/app.conf", "mode": "0644",
		}}, "Added file copy task."},
		{[]string{"service", "start", "enable"}, parser.Resource{Type: "service", Name: "Enable service", Properties: map[string]interface{}{
			"name": "nginx", "state": "started", "enabled": true,
		}}, "Added service management task."},
		{[]string{"package", "install", "apt"}, parser.Resource{Type: "apt", Name: "Install package", Properties: map[string]interface{}{
			"name": "curl", "state": "present",
		}}, "Added package installation task."},
	}
	return matchPatterns(msg, patterns)
}

func matchPatterns(msg string, patterns []struct {
	keywords []string
	resource parser.Resource
	response string
}) (string, []parser.Resource) {
	for _, p := range patterns {
		for _, kw := range p.keywords {
			if strings.Contains(msg, kw) {
				r := p.resource
				r.ID = fmt.Sprintf("pm_%d", time.Now().UnixNano())
				return p.response, []parser.Resource{r}
			}
		}
	}
	return "I can help you add infrastructure. Try asking for a VPC, VM, database, storage, load balancer, or Kubernetes cluster.", nil
}

// ─── Smart Suggestions ───

// SuggestNext returns resource suggestions based on what's already on the canvas.
// Uses IaC best practices to predict what the user likely needs next.
func SuggestNext(tool, provider string, canvas []CanvasResource) []Suggestion {
	if tool == "ansible" {
		return suggestAnsible(canvas)
	}

	types := make(map[string]bool)
	for _, r := range canvas {
		types[r.Type] = true
	}

	switch provider {
	case "google":
		return suggestGCP(types)
	case "azurerm":
		return suggestAzure(types)
	default:
		return suggestAWS(types)
	}
}

// Suggestion is a recommended next resource to add.
type Suggestion struct {
	Type     string `json:"type"`
	Label    string `json:"label"`
	Reason   string `json:"reason"`
	Priority int    `json:"priority"` // 1 = high, 2 = medium, 3 = low
}

func suggestAWS(has map[string]bool) []Suggestion {
	var s []Suggestion

	// Foundation: VPC first
	if !has["aws_vpc"] {
		s = append(s, Suggestion{"aws_vpc", "VPC", "Every AWS architecture starts with a VPC", 1})
		return s // Don't suggest more until VPC exists
	}

	// VPC exists — suggest networking
	if !has["aws_subnet"] {
		s = append(s, Suggestion{"aws_subnet", "Subnet", "Your VPC needs at least one subnet for resources", 1})
	}
	if !has["aws_internet_gateway"] {
		s = append(s, Suggestion{"aws_internet_gateway", "Internet Gateway", "Required for public internet access", 1})
	}
	if !has["aws_security_group"] {
		s = append(s, Suggestion{"aws_security_group", "Security Group", "Firewall rules needed before launching instances", 1})
	}

	// Has networking — suggest compute
	if has["aws_subnet"] && has["aws_security_group"] {
		if !has["aws_instance"] && !has["aws_eks_cluster"] && !has["aws_lambda_function"] {
			s = append(s, Suggestion{"aws_instance", "EC2 Instance", "Ready for compute — add a server", 2})
			s = append(s, Suggestion{"aws_eks_cluster", "EKS Cluster", "Or a Kubernetes cluster for containerized workloads", 2})
		}
	}

	// Has compute — suggest database and storage
	if has["aws_instance"] || has["aws_eks_cluster"] || has["aws_lambda_function"] {
		if !has["aws_db_instance"] && !has["aws_dynamodb_table"] {
			s = append(s, Suggestion{"aws_db_instance", "RDS Database", "Most apps need a database", 2})
		}
		if !has["aws_s3_bucket"] {
			s = append(s, Suggestion{"aws_s3_bucket", "S3 Bucket", "Object storage for files, backups, or static assets", 3})
		}
		if !has["aws_lb"] {
			s = append(s, Suggestion{"aws_lb", "Load Balancer", "Distribute traffic across instances", 3})
		}
	}

	// Security hardening
	if has["aws_instance"] && !has["aws_iam_role"] {
		s = append(s, Suggestion{"aws_iam_role", "IAM Role", "Instance needs an IAM role for AWS service access", 2})
	}
	if has["aws_s3_bucket"] && !has["aws_kms_key"] {
		s = append(s, Suggestion{"aws_kms_key", "KMS Key", "Encrypt your S3 bucket with a customer-managed key", 3})
	}

	return s
}

func suggestGCP(has map[string]bool) []Suggestion {
	var s []Suggestion

	if !has["google_compute_network"] {
		s = append(s, Suggestion{"google_compute_network", "VPC Network", "Every GCP project needs a VPC", 1})
		return s
	}

	if !has["google_compute_subnetwork"] {
		s = append(s, Suggestion{"google_compute_subnetwork", "Subnet", "Add a subnet to your VPC", 1})
	}
	if !has["google_compute_firewall"] {
		s = append(s, Suggestion{"google_compute_firewall", "Firewall Rule", "Allow traffic to your resources", 1})
	}

	if has["google_compute_subnetwork"] {
		if !has["google_compute_instance"] && !has["google_container_cluster"] {
			s = append(s, Suggestion{"google_compute_instance", "VM Instance", "Add a compute instance", 2})
			s = append(s, Suggestion{"google_container_cluster", "GKE Cluster", "Or a Kubernetes cluster", 2})
		}
	}

	if has["google_compute_instance"] || has["google_container_cluster"] {
		if !has["google_sql_database_instance"] {
			s = append(s, Suggestion{"google_sql_database_instance", "Cloud SQL", "Add a managed database", 2})
		}
		if !has["google_storage_bucket"] {
			s = append(s, Suggestion{"google_storage_bucket", "Cloud Storage", "Object storage for your app", 3})
		}
	}

	if !has["google_service_account"] && len(has) > 2 {
		s = append(s, Suggestion{"google_service_account", "Service Account", "Dedicated identity for your workloads", 3})
	}

	return s
}

func suggestAzure(has map[string]bool) []Suggestion {
	var s []Suggestion

	if !has["azurerm_resource_group"] {
		s = append(s, Suggestion{"azurerm_resource_group", "Resource Group", "Required for all Azure resources", 1})
		return s
	}

	if !has["azurerm_virtual_network"] {
		s = append(s, Suggestion{"azurerm_virtual_network", "Virtual Network", "Add networking for your resources", 1})
	}

	if has["azurerm_virtual_network"] && !has["azurerm_subnet"] {
		s = append(s, Suggestion{"azurerm_subnet", "Subnet", "Add a subnet to your VNet", 1})
	}

	if has["azurerm_subnet"] {
		if !has["azurerm_linux_virtual_machine"] && !has["azurerm_kubernetes_cluster"] {
			s = append(s, Suggestion{"azurerm_linux_virtual_machine", "Linux VM", "Add a virtual machine", 2})
			s = append(s, Suggestion{"azurerm_kubernetes_cluster", "AKS Cluster", "Or a Kubernetes cluster", 2})
		}
		if !has["azurerm_network_security_group"] {
			s = append(s, Suggestion{"azurerm_network_security_group", "NSG", "Firewall rules for your subnet", 1})
		}
	}

	if has["azurerm_linux_virtual_machine"] || has["azurerm_kubernetes_cluster"] {
		if !has["azurerm_mssql_database"] && !has["azurerm_postgresql_flexible_server"] {
			s = append(s, Suggestion{"azurerm_postgresql_flexible_server", "PostgreSQL", "Add a managed database", 2})
		}
		if !has["azurerm_storage_account"] {
			s = append(s, Suggestion{"azurerm_storage_account", "Storage Account", "Blob/file storage for your app", 3})
		}
	}

	if !has["azurerm_key_vault"] && len(has) > 3 {
		s = append(s, Suggestion{"azurerm_key_vault", "Key Vault", "Secure your secrets and keys", 3})
	}

	return s
}

func suggestAnsible(canvas []CanvasResource) []Suggestion {
	types := make(map[string]bool)
	for _, r := range canvas {
		types[r.Type] = true
	}

	var s []Suggestion
	if !types["apt"] && !types["yum"] && !types["dnf"] {
		s = append(s, Suggestion{"apt", "Install Packages", "Start by installing required packages", 1})
	}
	if (types["apt"] || types["yum"]) && !types["service"] {
		s = append(s, Suggestion{"service", "Manage Service", "Start/enable the installed service", 1})
	}
	if !types["user"] && len(types) > 0 {
		s = append(s, Suggestion{"user", "Create User", "Add a deploy/application user", 2})
	}
	if !types["copy"] && !types["template"] {
		s = append(s, Suggestion{"template", "Deploy Config", "Deploy configuration files", 2})
	}
	if !types["ufw"] && !types["firewalld"] {
		s = append(s, Suggestion{"ufw", "Firewall", "Configure firewall rules", 3})
	}
	return s
}
