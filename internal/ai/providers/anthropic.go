package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// anthropicProvider talks to Anthropic's Messages API.
// https://docs.anthropic.com/en/api/messages
//
// Compared to the OpenAI shape:
//   - the system prompt is a top-level "system" field, not a message with role;
//   - the anthropic-version header is required;
//   - response content is an array of blocks (we only use the first text block today);
//   - max_tokens is required, not optional.
//
// Prompt caching (cache_control on the system prompt) arrives in a later
// commit — this one is just feature-parity with the other providers.
type anthropicProvider struct {
	endpoint   string
	model      string
	apiKey     string
	apiVersion string
	client     *http.Client
}

// DefaultAnthropicEndpoint is the public Messages API. Users can point at a
// proxy by supplying a custom Endpoint in Config.
const DefaultAnthropicEndpoint = "https://api.anthropic.com/v1/messages"

// DefaultAnthropicVersion pins the API schema version so an upstream default
// change can't silently alter request/response shapes under our feet.
const DefaultAnthropicVersion = "2023-06-01"

// NewAnthropic constructs a Claude-backed Provider. APIKey is required;
// Endpoint defaults to the public Messages API when blank.
func NewAnthropic(cfg Config) Provider {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultAnthropicEndpoint
	}
	// Accept both the base URL and the full /v1/messages URL, same convenience
	// we offer for OpenAI-compatible endpoints.
	endpoint = strings.TrimSuffix(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/messages") {
		endpoint += "/v1/messages"
	}
	return &anthropicProvider{
		endpoint:   endpoint,
		model:      cfg.Model,
		apiKey:     cfg.APIKey,
		apiVersion: DefaultAnthropicVersion,
		client:     defaultHTTPClient(cfg.Timeout),
	}
}

func (p *anthropicProvider) Kind() Kind { return KindAnthropic }

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	// Usage fields are consumed by prompt-caching telemetry in a later commit;
	// we decode them now so the wire shape stays stable.
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *anthropicProvider) Complete(ctx context.Context, req Request) (string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		// The Messages API requires max_tokens; pick a sensible ceiling that
		// matches the other providers' default.
		maxTokens = 4096
	}
	body := anthropicRequest{
		Model:  p.model,
		System: req.System,
		Messages: []anthropicMessage{
			{Role: "user", Content: req.User},
		},
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
	}
	raw, _ := json.Marshal(body)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", p.apiVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic API unavailable: %w", err)
	}
	defer resp.Body.Close()

	var decoded anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("anthropic API error: %s", decoded.Error.Message)
	}
	if len(decoded.Content) == 0 {
		return "", ErrEmptyResponse
	}
	// The Messages API returns an array of content blocks. Concatenate all
	// "text" blocks so we don't silently drop content when the model chooses
	// to split its response.
	var out strings.Builder
	for _, block := range decoded.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	if out.Len() == 0 {
		return "", ErrEmptyResponse
	}
	return out.String(), nil
}
