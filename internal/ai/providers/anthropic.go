package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// anthropicProvider talks to Anthropic's Messages API.
// https://docs.anthropic.com/en/api/messages
//
// Compared to the OpenAI shape:
//   - the system prompt is a top-level "system" field, not a message with role;
//   - the anthropic-version header is required;
//   - response content is an array of blocks (we only use the first text block today);
//   - max_tokens is required, not optional;
//   - prompt caching is supported via cache_control on the top-level system field.
type anthropicProvider struct {
	endpoint   string
	model      string
	apiKey     string
	apiVersion string
	client     *http.Client

	// lastUsage carries the usage block from the most recent response.
	// Exported via LastUsage() so tests and the /api/ai/settings endpoint
	// can read cache-hit ratios without re-plumbing telemetry through the
	// Provider interface (which stays tiny on purpose).
	mu        sync.Mutex
	lastUsage AnthropicUsage
}

// AnthropicUsage is a public snapshot of the decoded usage fields from the
// last Anthropic response. Exposed for observability — callers treat the
// whole struct as read-only.
type AnthropicUsage struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// LastUsage returns the usage snapshot from the most recent round-trip. Safe
// to call concurrently with other provider methods.
func (p *anthropicProvider) LastUsage() AnthropicUsage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastUsage
}

func (p *anthropicProvider) recordUsage(u struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastUsage = AnthropicUsage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
	}
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

// anthropicCacheControl marks a content block as cacheable. Anthropic currently
// exposes one type ("ephemeral"), which caches for about 5 minutes.
type anthropicCacheControl struct {
	Type string `json:"type"`
}

// anthropicSystemBlock is one element of the system-as-array form. When
// present, CacheControl tells the API to put a cache breakpoint at the end of
// this block so subsequent calls with the same prefix hit the cache.
type anthropicSystemBlock struct {
	Type         string                 `json:"type"` // always "text" today
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicRequest.System accepts either a plain string or an array of
// content blocks. We send the array form when caching is requested (so we can
// attach cache_control) and the string form otherwise — both are valid per
// Anthropic's docs; the simpler shape keeps non-cached requests compact and
// easy to read in logs.
type anthropicRequest struct {
	Model       string             `json:"model"`
	System      any                `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

// buildSystemField returns the appropriate JSON shape for the System field
// depending on whether the caller opted into caching.
func buildSystemField(text string, cacheable bool) any {
	if text == "" {
		return nil
	}
	if !cacheable {
		return text
	}
	return []anthropicSystemBlock{{
		Type:         "text",
		Text:         text,
		CacheControl: &anthropicCacheControl{Type: "ephemeral"},
	}}
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
		System: buildSystemField(req.System, req.Cacheable),
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
	p.recordUsage(decoded.Usage)
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

// anthropicStreamEvent is the shape of the event JSON carried by each SSE
// "data:" line from the Messages API. We care about content_block_delta for
// incremental text and message_delta / message_stop for stream end. Event
// types we don't recognise are ignored so new fields don't break parsing.
//
// Usage fields live on message_start (initial input_tokens + cache_*) and on
// message_delta (output_tokens running total). We merge both so LastUsage
// after a stream is directly comparable to the non-streaming path.
type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	Message struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
		} `json:"usage"`
	} `json:"message"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Stream consumes Anthropic's Messages SSE stream. Each token arrives as a
// content_block_delta event whose delta.text carries the increment.
func (p *anthropicProvider) Stream(ctx context.Context, req Request, onDelta DeltaFunc) (string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	body := anthropicRequest{
		Model:  p.model,
		System: buildSystemField(req.System, req.Cacheable),
		Messages: []anthropicMessage{
			{Role: "user", Content: req.User},
		},
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", p.apiVersion)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic API unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if len(body) > 0 && json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			return "", fmt.Errorf("anthropic API error (%d): %s", resp.StatusCode, apiErr.Error.Message)
		}
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return "", fmt.Errorf("anthropic API error (%d): %s", resp.StatusCode, msg)
		}
		return "", fmt.Errorf("anthropic API error (%d): %s", resp.StatusCode, resp.Status)
	}

	var accum strings.Builder
	// Accumulate usage across the stream: message_start carries input_tokens
	// and the cache counts; message_delta carries the running output_tokens.
	streamUsage := AnthropicUsage{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return accum.String(), err
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "" {
			continue
		}
		var ev anthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev.Type == "message_start" {
			streamUsage.InputTokens = ev.Message.Usage.InputTokens
			streamUsage.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
			streamUsage.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
			streamUsage.OutputTokens = ev.Message.Usage.OutputTokens
		}
		if ev.Type == "message_delta" && ev.Usage.OutputTokens > 0 {
			streamUsage.OutputTokens = ev.Usage.OutputTokens
		}
		if ev.Error != nil {
			return accum.String(), fmt.Errorf("anthropic API error: %s", ev.Error.Message)
		}
		if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
			onDelta(ev.Delta.Text)
			accum.WriteString(ev.Delta.Text)
		}
		if ev.Type == "message_stop" {
			break
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return accum.String(), fmt.Errorf("anthropic stream: %w", err)
	}
	// Persist the merged usage snapshot so LastUsage() works identically
	// after a streaming call and a blocking Complete call.
	p.mu.Lock()
	p.lastUsage = streamUsage
	p.mu.Unlock()
	if accum.Len() == 0 {
		return "", ErrEmptyResponse
	}
	return accum.String(), nil
}
