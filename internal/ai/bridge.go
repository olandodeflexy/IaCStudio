package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai/providers"
	"github.com/iac-studio/iac-studio/internal/parser"
)

// defaultTemperature and defaultMaxTokens are the sampling knobs used for all
// IaC-generation calls. Low temperature keeps output structured and JSON-
// friendly; max_tokens is high enough to cover multi-resource topologies.
const (
	defaultTemperature = 0.3
	defaultMaxTokens   = 4096
)

// Client is the high-level AI bridge. It holds a configured Provider and
// exposes IaC-specific operations (chat, fix, topology). All LLM wire-format
// concerns live in the providers package.
type Client struct {
	cfg      providers.Config
	provider providers.Provider
	// providerErr is retained if New returned an error so callers hitting the
	// bridge can fall back to deterministic heuristics (PatternMatch) instead
	// of crashing; every method checks it before attempting a call.
	providerErr error
}

// ProviderConfig is the JSON-friendly view of the currently-configured
// provider returned by /api/ai/settings. The APIKey field is masked on read.
type ProviderConfig struct {
	Type     string `json:"type"`     // "ollama" | "openai" | "anthropic"
	Endpoint string `json:"endpoint"` // API endpoint URL
	Model    string `json:"model"`    // model name
	APIKey   string `json:"api_key"`  // masked on read, full on write
}

// NewClient builds a Client wired to a local Ollama instance by default.
// Use UpdateConfig at runtime to switch providers or supply an API key.
func NewClient(endpoint, model string) *Client {
	c := &Client{}
	c.applyConfig(providers.Config{
		Kind:     providers.KindOllama,
		Endpoint: endpoint,
		Model:    model,
		Timeout:  5 * time.Minute,
	})
	return c
}

// UpdateConfig swaps the active provider. Kind is inferred from credentials
// when not supplied — a non-empty APIKey implies OpenAI-compatible, empty
// implies Ollama. The router validates kind strings before calling so the
// user sees a meaningful error for unsupported backends.
func (c *Client) UpdateConfig(endpoint, model, apiKey string) {
	cfg := c.cfg
	cfg.Endpoint = endpoint
	cfg.Model = model
	cfg.APIKey = apiKey
	// Reset inferred kind so the factory re-detects based on apiKey.
	cfg.Kind = ""
	c.applyConfig(cfg)
}

// UpdateConfigKind is a typed variant of UpdateConfig for callers (the router)
// that know the Kind explicitly — e.g. when the user picks "anthropic" in the
// UI even though they have an OpenAI-compatible key string format.
const maskedAPIKeyPlaceholder = "********"

func (c *Client) UpdateConfigKind(kind providers.Kind, endpoint, model, apiKey string) {
	if apiKey == maskedAPIKeyPlaceholder {
		apiKey = c.cfg.APIKey
	}
	c.applyConfig(providers.Config{
		Kind:     kind,
		Endpoint: endpoint,
		Model:    model,
		APIKey:   apiKey,
		Timeout:  c.cfg.Timeout,
	})
}

func (c *Client) applyConfig(cfg providers.Config) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	c.cfg = cfg
	p, err := providers.New(cfg)
	c.provider = p
	c.providerErr = err
	if err == nil && cfg.Kind == "" && p != nil {
		c.cfg.Kind = p.Kind()
	}
}

// Provider returns the active underlying provider, or nil if construction
// failed. Callers that need provider-specific capabilities (tool-use loop,
// streaming, etc.) type-assert on the returned value.
func (c *Client) Provider() providers.Provider {
	if c.providerErr != nil {
		return nil
	}
	return c.provider
}

// GetConfig returns the current provider config without exposing the current
// API key value. When a key is already configured, it returns a masked
// placeholder so callers can round-trip settings without re-entering the
// secret; UpdateConfigKind treats that placeholder as "keep existing key".
func (c *Client) GetConfig() ProviderConfig {
	kind := c.cfg.Kind
	if kind == "" {
		kind = providers.KindOllama
	}
	apiKey := ""
	if c.cfg.APIKey != "" {
		apiKey = maskedAPIKeyPlaceholder
	}
	return ProviderConfig{
		Type:     string(kind),
		Endpoint: c.cfg.Endpoint,
		Model:    c.cfg.Model,
		APIKey:   apiKey,
	}
}

// callLLM is the single internal chokepoint for every IaC-generation method
// on Client. It attaches the shared sampling knobs, delegates to the active
// Provider, and normalises error shapes.
func (c *Client) callLLM(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if c.providerErr != nil {
		return "", c.providerErr
	}
	return c.provider.Complete(ctx, providers.Request{
		System:      systemPrompt,
		User:        userPrompt,
		Temperature: defaultTemperature,
		MaxTokens:   defaultMaxTokens,
		JSONMode:    true,
		Cacheable:   true,
	})
}

// StreamChat runs the same IaC chat generation as GenerateIaC but emits
// incremental text chunks via onDelta as they arrive from the provider. The
// returned tuple is (full response text, parsed resources, error) — the
// caller is responsible for feeding onDelta to an SSE response writer.
//
// The parsed resources come from the same JSON parser GenerateIaC uses, so
// the contract downstream of this function is identical: callers can render
// tokens live and still get a clean structured result at the end.
func (c *Client) StreamChat(ctx context.Context, req ChatRequest, onDelta providers.DeltaFunc) (string, []parser.Resource, error) {
	if c.providerErr != nil {
		return "", nil, c.providerErr
	}
	systemPrompt := buildSystemPromptWithContext(req.Tool, req.Provider, req.Canvas, req.ProjectContext)
	userPrompt := buildChatUserPrompt(req)

	raw, err := c.provider.Stream(ctx, providers.Request{
		System:      systemPrompt,
		User:        userPrompt,
		Temperature: defaultTemperature,
		MaxTokens:   defaultMaxTokens,
		JSONMode:    true,
		Cacheable:   true,
	}, onDelta)
	if err != nil {
		return raw, nil, err
	}
	msg, resources, err := parseAIResponse(raw)
	return msg, resources, err
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
	// Project, when set, names the active project. The HTTP layer uses it
	// to look up the project's RAG index and populate ProjectContext
	// before calling the bridge.
	Project string `json:"project,omitempty"`
	// ProjectContext, when set, is prepended to the system prompt as
	// retrieved RAG context. The HTTP layer runs the retrieval and fills
	// this in; the bridge is RAG-unaware so tests can exercise the
	// prompt path without an embedder.
	ProjectContext string `json:"-"`
}

// CanvasResource is a simplified view of what's on the canvas for AI context.
type CanvasResource struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// GenerateIaC sends a natural language request to the configured LLM with
// full conversation context, provider awareness, and canvas state.
func (c *Client) GenerateIaC(ctx context.Context, req ChatRequest) (string, []parser.Resource, error) {
	systemPrompt := buildSystemPromptWithContext(req.Tool, req.Provider, req.Canvas, req.ProjectContext)
	userPrompt := buildChatUserPrompt(req)

	raw, err := c.callLLM(ctx, systemPrompt, userPrompt)
	if err != nil {
		return "", nil, err
	}
	return parseAIResponse(raw)
}

// buildChatUserPrompt composes the user-side prompt for a chat turn. It folds
// in up to the last six history messages plus the current user message, and
// reminds the model to emit JSON. Shared between GenerateIaC and StreamChat
// so both paths see identical context.
func buildChatUserPrompt(req ChatRequest) string {
	if len(req.History) == 0 {
		return fmt.Sprintf("User request: %s\n\nRespond with JSON only.", req.Message)
	}
	history := req.History
	if len(history) > 6 {
		history = history[len(history)-6:]
	}
	parts := make([]string, 0, len(history))
	for _, msg := range history {
		if msg.Role == "user" {
			parts = append(parts, "User: "+msg.Content)
		} else {
			parts = append(parts, "Assistant: "+msg.Content)
		}
	}
	return fmt.Sprintf("Conversation so far:\n%s\n\nUser request: %s\n\nRespond with JSON only.",
		strings.Join(parts, "\n"), req.Message)
}

// PlanFixRequest is a request to analyze plan/apply output and suggest fixes.
type PlanFixRequest struct {
	Tool        string           `json:"tool"`
	Provider    string           `json:"provider"`
	Command     string           `json:"command"`     // "plan", "apply", "init", "validate"
	Output      string           `json:"output"`      // raw CLI output
	ExitCode    int              `json:"exit_code"`
	Canvas      []CanvasResource `json:"canvas"`
}

// PlanFix is a suggested fix from the AI.
type PlanFix struct {
	Message    string            `json:"message"`     // explanation of the problem
	Fixes      []ResourceFix     `json:"fixes"`       // specific changes to make
	NewResources []parser.Resource `json:"new_resources"` // resources to add
}

// ResourceFix is a specific property change on an existing resource.
type ResourceFix struct {
	ResourceType string `json:"resource_type"`
	ResourceName string `json:"resource_name"`
	Field        string `json:"field"`
	OldValue     string `json:"old_value"`
	NewValue     string `json:"new_value"`
	Reason       string `json:"reason"`
}

// AnalyzePlanOutput sends terraform plan/apply output to the AI for diagnosis and fix suggestions.
func (c *Client) AnalyzePlanOutput(ctx context.Context, req PlanFixRequest) (*PlanFix, error) {
	output := req.Output
	if len(output) > 4000 {
		output = output[:2000] + "\n\n... (truncated) ...\n\n" + output[len(output)-2000:]
	}

	var canvasLines []string
	for _, r := range req.Canvas {
		canvasLines = append(canvasLines, fmt.Sprintf("  - %s.%s", r.Type, r.Name))
	}
	canvasCtx := ""
	if len(canvasLines) > 0 {
		canvasCtx = "\n\nResources on canvas:\n" + strings.Join(canvasLines, "\n")
	}

	systemPrompt := fmt.Sprintf(`You are an Infrastructure as Code debugging assistant for %s (%s provider).
Analyze command output and respond with a JSON object:
{
  "message": "Clear explanation of what went wrong and how to fix it",
  "fixes": [{"resource_type":"...","resource_name":"...","field":"...","old_value":"...","new_value":"...","reason":"..."}],
  "new_resources": [{"type":"...","name":"...","properties":{}}]
}
Rules: "fixes" change EXISTING resources, "new_resources" are ADDED. Be specific. JSON only.`, req.Tool, req.Provider)

	userPrompt := fmt.Sprintf("The user ran \"%s %s\" and got this output:\n\n---\n%s\n---%s",
		req.Tool, req.Command, output, canvasCtx)

	raw, err := c.callLLM(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	return parsePlanFixResponse(raw)
}

func parsePlanFixResponse(raw string) (*PlanFix, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result struct {
		Message      string `json:"message"`
		Fixes        []ResourceFix `json:"fixes"`
		NewResources []struct {
			Type       string                 `json:"type"`
			Name       string                 `json:"name"`
			Properties map[string]interface{} `json:"properties"`
		} `json:"new_resources"`
	}

	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		// If not valid JSON, return the raw text as the message
		return &PlanFix{Message: raw}, nil
	}

	fix := &PlanFix{
		Message: result.Message,
		Fixes:   result.Fixes,
	}

	for i, r := range result.NewResources {
		fix.NewResources = append(fix.NewResources, parser.Resource{
			ID:         fmt.Sprintf("fix_%d_%d", time.Now().Unix(), i),
			Type:       r.Type,
			Name:       r.Name,
			Properties: r.Properties,
		})
	}

	return fix, nil
}

// AnalyzePlanFallback provides basic error analysis without AI.
func AnalyzePlanFallback(output string, exitCode int) *PlanFix {
	fix := &PlanFix{}
	lower := strings.ToLower(output)

	switch {
	case exitCode == 0 && !strings.Contains(lower, "error"):
		fix.Message = "Command completed successfully. No issues detected."
	case strings.Contains(lower, "no valid credential"):
		fix.Message = "AWS credentials not configured. Run `aws configure` or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables."
	case strings.Contains(lower, "could not find plugin") || strings.Contains(lower, "provider registry"):
		fix.Message = "Terraform provider not initialized. Run `terraform init` first to download the required providers."
	case strings.Contains(lower, "unsupported attribute") || strings.Contains(lower, "unsupported argument"):
		fix.Message = "One or more resource properties are invalid. Check the highlighted fields against the provider documentation."
	case strings.Contains(lower, "error creating") || strings.Contains(lower, "error configuring"):
		fix.Message = "Resource creation failed. Check your provider credentials, region settings, and resource quotas."
	case strings.Contains(lower, "already exists"):
		fix.Message = "A resource with this name already exists. Use `terraform import` to bring it under management, or change the resource name."
	case strings.Contains(lower, "access denied") || strings.Contains(lower, "unauthorized"):
		fix.Message = "Permission denied. Your credentials don't have the required IAM permissions for this operation."
	case strings.Contains(lower, "limit exceeded") || strings.Contains(lower, "quota"):
		fix.Message = "Service quota exceeded. Request a limit increase or reduce the number of resources."
	default:
		fix.Message = "Command failed. Review the output above for specific error details."
	}

	return fix
}

// TopologyRequest describes an architecture to generate.
type TopologyRequest struct {
	Description string `json:"description"` // free-text architecture description
	Tool        string `json:"tool"`
	Provider    string `json:"provider"`
}

// GenerateTopology takes a free-text architecture description and generates
// a full set of resources with connections. This is the "describe your
// infrastructure and we'll build it" feature.
func (c *Client) GenerateTopology(ctx context.Context, req TopologyRequest) (string, []parser.Resource, error) {
	providerGuide := buildProviderGuide(req.Tool, req.Provider)

	systemPrompt := fmt.Sprintf(`You are an expert Infrastructure as Code architect for %s.
Generate a COMPLETE set of resources following IaC best practices.

RULES:
1. %s
2. Include ALL necessary supporting resources (networking, security, IAM)
3. Use descriptive snake_case names that reflect purpose
4. Include sensible production defaults (not just minimal config)
5. Follow the dependency order: networking → security → compute → data → monitoring

Respond with a JSON object:
{
  "message": "Overview of the architecture you designed and key decisions",
  "resources": [
    {
      "type": "resource_type",
      "name": "descriptive_name",
      "properties": {"key": "value", ...}
    }
  ]
}

Be thorough — include VPCs/networks, subnets, security groups, IAM roles,
and any other resources the architecture needs to actually work.
Only respond with valid JSON.`, req.Tool, providerGuide)

	userPrompt := fmt.Sprintf("Build this infrastructure: %s", req.Description)

	raw, err := c.callLLM(ctx, systemPrompt, userPrompt)
	if err != nil {
		return "", nil, err
	}
	return parseAIResponse(raw)
}

// DiagramImage is one image attachment for GenerateFromDiagram.
// It is a type alias for providers.Image so the HTTP layer doesn't
// need to import the providers package directly, while the bridge
// avoids copying the slice before passing it to CompleteWithImages.
type DiagramImage = providers.Image

// GenerateFromDiagram is the vision counterpart to GenerateTopology: it
// takes architecture diagrams (whiteboard photos, lucid exports,
// hand-sketches) + an optional free-text description, sends them to a
// vision-capable provider, and returns the same (message, resources,
// err) tuple GenerateTopology produces so every downstream scaffolding
// path works unchanged.
//
// Returns a descriptive error when the configured provider does not
// implement VisionUser — the HTTP handler surfaces that as a 400 with
// a "switch to Anthropic" hint, mirroring the agent endpoint's pattern.
func (c *Client) GenerateFromDiagram(ctx context.Context, req TopologyRequest, images []DiagramImage) (string, []parser.Resource, error) {
	if c.providerErr != nil {
		return "", nil, c.providerErr
	}
	vu, ok := c.provider.(providers.VisionUser)
	if !ok {
		return "", nil, fmt.Errorf("provider %q does not support vision — switch to Anthropic to enable diagram-to-project", c.provider.Kind())
	}
	if len(images) == 0 {
		return "", nil, fmt.Errorf("no images provided — use GenerateTopology for text-only requests")
	}

	providerGuide := buildProviderGuide(req.Tool, req.Provider)
	systemPrompt := fmt.Sprintf(`You are an expert Infrastructure as Code architect for %s.
The user has uploaded one or more architecture diagrams. Read every diagram
carefully: identify resources, relationships, and inferred constraints
(environments, subnets, public/private tiers, load balancers, data stores).

RULES:
1. %s
2. Translate every component visible in the diagram into a concrete
   resource type. Common diagram shapes: a "VPC" cloud → aws_vpc; subnet
   rectangles → aws_subnet; an EC2/VM icon → aws_instance; a database
   cylinder → aws_db_instance or aws_rds_cluster depending on scale;
   an "ALB"/"ELB" label → aws_lb; anything marked "S3" or a bucket icon
   → aws_s3_bucket.
3. Fill in supporting resources the diagram implies but doesn't draw —
   IAM roles, security groups, route tables, NAT gateways.
4. Preserve any labels from the diagram as names when they're descriptive
   (snake_case'd); otherwise pick purposeful names.
5. Follow dependency order: networking → security → compute → data →
   monitoring.

Respond with a JSON object:
{
  "message": "What you saw in the diagram and how you translated it",
  "resources": [
    {"type": "...", "name": "...", "properties": {...}}
  ]
}
Only respond with valid JSON.`, req.Tool, providerGuide)

	userText := "Build the infrastructure shown in the uploaded diagram."
	if d := strings.TrimSpace(req.Description); d != "" {
		userText = fmt.Sprintf("Build the infrastructure shown in the uploaded diagram. Additional context: %s", d)
	}

	raw, err := vu.CompleteWithImages(ctx, providers.Request{
		System:      systemPrompt,
		User:        userText,
		Temperature: defaultTemperature,
		MaxTokens:   defaultMaxTokens,
		JSONMode:    true,
		// The system prompt is long and stable per tool+provider combo;
		// caching it on Anthropic saves tokens on repeated diagram uploads.
		Cacheable: true,
	}, images)
	if err != nil {
		return "", nil, err
	}
	return parseAIResponse(raw)
}

// GenerateIaCSimple is the legacy single-message interface (used by pattern matching fallback).
func (c *Client) GenerateIaCSimple(ctx context.Context, message, tool string) (string, []parser.Resource, error) {
	return c.GenerateIaC(ctx, ChatRequest{
		Message: message,
		Tool:    tool,
	})
}

// buildSystemPrompt renders the main IaC system prompt from the embedded
// template file (internal/ai/prompts/system.md). The template receives
// Tool, ProviderGuide, and CanvasContext; the caller never hand-composes the
// prompt string any more.
func buildSystemPrompt(tool, provider string, canvas []CanvasResource) string {
	return buildSystemPromptWithContext(tool, provider, canvas, "")
}

// buildSystemPromptWithContext is the RAG-aware variant: projectContext
// is a pre-formatted block of retrieved chunks (via rag.FormatContext)
// and gets injected above the provider guide so the model reads
// project-specific conventions before the generic provider rules.
func buildSystemPromptWithContext(tool, provider string, canvas []CanvasResource, projectContext string) string {
	return renderPrompt("system", map[string]string{
		"Tool":           tool,
		"ProviderGuide":  buildProviderGuide(tool, provider),
		"CanvasContext":  buildCanvasContext(canvas),
		"ProjectContext": projectContext,
	})
}

// buildCanvasContext is extracted so the system prompt template can receive
// pre-rendered text rather than a nested slice. Empty canvases return the
// empty string so the template leaves no dangling whitespace.
func buildCanvasContext(canvas []CanvasResource) string {
	if len(canvas) == 0 {
		return ""
	}
	items := make([]string, 0, len(canvas))
	for _, r := range canvas {
		items = append(items, fmt.Sprintf("  - %s.%s", r.Type, r.Name))
	}
	return fmt.Sprintf(`

EXISTING RESOURCES ON CANVAS (do not duplicate these):
%s

When the user asks for follow-up resources, build on what already exists.
Reference existing resources by their type.name when creating connections.`,
		strings.Join(items, "\n"))
}

// buildProviderGuide picks the right provider-guide prompt file. Ansible
// overrides the provider since modules are shared across clouds. The guide
// text itself lives in internal/ai/prompts/provider_*.md.
func buildProviderGuide(tool, provider string) string {
	id := "provider_aws"
	if tool == "ansible" {
		id = "provider_ansible"
	} else {
		switch provider {
		case "google":
			id = "provider_gcp"
		case "azurerm":
			id = "provider_azurerm"
		}
	}
	return renderPrompt(id, nil)
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
