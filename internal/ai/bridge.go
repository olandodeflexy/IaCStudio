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
		client:   &http.Client{Timeout: 60 * time.Second},
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

// GenerateIaC sends a natural language request to Ollama and returns
// a human-readable response and any resources to add to the canvas.
func (c *OllamaClient) GenerateIaC(ctx context.Context, message, tool string) (string, []parser.Resource, error) {
	systemPrompt := buildSystemPrompt(tool)
	prompt := fmt.Sprintf("%s\n\nUser request: %s\n\nRespond with JSON only.", systemPrompt, message)

	reqBody := ollamaRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
		Format: "json",
	}

	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("ollama unavailable: %w", err)
	}
	defer resp.Body.Close()

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", nil, err
	}

	// Parse the JSON response from the LLM
	return parseAIResponse(ollamaResp.Response)
}

func buildSystemPrompt(tool string) string {
	return fmt.Sprintf(`You are an Infrastructure as Code assistant for %s.
When the user describes infrastructure they need, respond with a JSON object containing:
{
  "message": "A brief explanation of what you created",
  "resources": [
    {
      "type": "resource_type",
      "name": "resource_name",
      "properties": {"key": "value"}
    }
  ]
}

For Terraform/OpenTofu, use AWS resource types like aws_vpc, aws_instance, aws_s3_bucket, etc.
For Ansible, use module names like apt, service, copy, template, docker_container, etc.
Only respond with valid JSON. No markdown, no code fences.`, tool)
}

func parseAIResponse(raw string) (string, []parser.Resource, error) {
	// Clean response (strip any markdown fences the LLM might add)
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
		return raw, nil, nil // Return raw text if not valid JSON
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
func PatternMatch(message, tool string) (string, []parser.Resource) {
	msg := strings.ToLower(message)

	patterns := []struct {
		keywords []string
		resource parser.Resource
		response string
	}{
		{
			keywords: []string{"vpc", "network"},
			resource: parser.Resource{Type: "aws_vpc", Name: "main_vpc", Properties: map[string]interface{}{
				"cidr_block": "10.0.0.0/16", "enable_dns_support": true, "enable_dns_hostnames": true,
			}},
			response: "Added a VPC with DNS support. You can adjust the CIDR block in properties.",
		},
		{
			keywords: []string{"subnet"},
			resource: parser.Resource{Type: "aws_subnet", Name: "main_subnet", Properties: map[string]interface{}{
				"cidr_block": "10.0.1.0/24", "availability_zone": "us-east-1a",
			}},
			response: "Subnet created in us-east-1a. Click to change the availability zone.",
		},
		{
			keywords: []string{"ec2", "instance", "server", "vm"},
			resource: parser.Resource{Type: "aws_instance", Name: "web_server", Properties: map[string]interface{}{
				"ami": "ami-0c55b159cbfafe1f0", "instance_type": "t3.micro",
			}},
			response: "EC2 instance added with t3.micro. Modify the AMI and instance type as needed.",
		},
		{
			keywords: []string{"s3", "bucket", "storage"},
			resource: parser.Resource{Type: "aws_s3_bucket", Name: "app_storage", Properties: map[string]interface{}{
				"bucket": "my-app-storage", "acl": "private",
			}},
			response: "S3 bucket created with private ACL. Remember to give it a globally unique name.",
		},
		{
			keywords: []string{"database", "rds", "db", "postgres", "mysql"},
			resource: parser.Resource{Type: "aws_rds_instance", Name: "main_db", Properties: map[string]interface{}{
				"engine": "postgres", "engine_version": "15.4", "instance_class": "db.t3.micro", "allocated_storage": 20,
			}},
			response: "PostgreSQL RDS instance added. Set to db.t3.micro with 20GB — good for development.",
		},
		{
			keywords: []string{"lambda", "function", "serverless"},
			resource: parser.Resource{Type: "aws_lambda_function", Name: "api_handler", Properties: map[string]interface{}{
				"function_name": "api_handler", "runtime": "nodejs18.x", "memory_size": 256,
			}},
			response: "Lambda function created with Node.js 18 and 256MB memory.",
		},
		{
			keywords: []string{"security group", "firewall", "sg"},
			resource: parser.Resource{Type: "aws_security_group", Name: "web_sg", Properties: map[string]interface{}{
				"name": "web_sg", "description": "Allow HTTP/HTTPS traffic",
			}},
			response: "Security group added. Configure ingress/egress rules in properties.",
		},
	}

	// Ansible-specific patterns
	if tool == "ansible" {
		patterns = []struct {
			keywords []string
			resource parser.Resource
			response string
		}{
			{
				keywords: []string{"nginx", "web server"},
				resource: parser.Resource{Type: "apt", Name: "Install nginx", Properties: map[string]interface{}{
					"name": "nginx", "state": "present", "update_cache": true,
				}},
				response: "Added nginx installation task with cache update.",
			},
			{
				keywords: []string{"docker", "container"},
				resource: parser.Resource{Type: "docker_container", Name: "Run container", Properties: map[string]interface{}{
					"name": "webapp", "image": "nginx:latest", "ports": "80:80",
				}},
				response: "Docker container task added running nginx on port 80.",
			},
			{
				keywords: []string{"user", "account"},
				resource: parser.Resource{Type: "user", Name: "Create user", Properties: map[string]interface{}{
					"name": "deploy", "shell": "/bin/bash", "groups": "sudo",
				}},
				response: "User creation task added for 'deploy' with sudo access.",
			},
			{
				keywords: []string{"copy", "file"},
				resource: parser.Resource{Type: "copy", Name: "Copy config", Properties: map[string]interface{}{
					"src": "files/app.conf", "dest": "/etc/app/app.conf", "mode": "0644",
				}},
				response: "File copy task added. Update the source and destination paths.",
			},
		}
	}

	for _, p := range patterns {
		for _, kw := range p.keywords {
			if strings.Contains(msg, kw) {
				r := p.resource
				r.ID = fmt.Sprintf("pm_%d", time.Now().UnixNano())
				return p.response, []parser.Resource{r}
			}
		}
	}

	return "I can help you add infrastructure components. Try asking for a VPC, EC2 instance, database, S3 bucket, Lambda function, or security group.", nil
}
