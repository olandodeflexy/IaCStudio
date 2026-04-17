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
)

// openAIProvider talks to any OpenAI-compatible /chat/completions endpoint —
// OpenAI itself, Groq, Together, Azure OpenAI, and anything else that honours
// the same schema. Auth is Bearer {apiKey}; the endpoint is allowed to include
// or omit the /chat/completions suffix.
type openAIProvider struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
}

// NewOpenAI constructs a Provider speaking the OpenAI /chat/completions wire
// format. The APIKey is required.
func NewOpenAI(cfg Config) Provider {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}

	return &openAIProvider{
		endpoint: normaliseOpenAIEndpoint(endpoint),
		model:    cfg.Model,
		apiKey:   cfg.APIKey,
		client:   defaultHTTPClient(cfg.Timeout),
	}
}

func (p *openAIProvider) Kind() Kind { return KindOpenAI }

// normaliseOpenAIEndpoint accepts either the bare base URL ("https://api.openai.com/v1")
// or the full chat-completions URL, and returns the full completions URL. This
// preserves the historical convenience of letting users paste either.
func normaliseOpenAIEndpoint(ep string) string {
	ep = strings.TrimSuffix(ep, "/")
	if !strings.HasSuffix(ep, "/chat/completions") {
		ep += "/chat/completions"
	}
	return ep
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *openAIProvider) Complete(ctx context.Context, req Request) (string, error) {
	body := openAIRequest{
		Model: p.model,
		Messages: []openAIMessage{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai-compatible API unavailable: %w", err)
	}
	defer resp.Body.Close()

	var decoded openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("API error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return "", ErrEmptyResponse
	}
	return decoded.Choices[0].Message.Content, nil
}

// openAIStreamChunk is the shape of each SSE event body from /chat/completions
// with stream=true. Choices[0].Delta.Content carries the incremental text.
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// Stream sends stream=true to /chat/completions and parses the SSE response.
// Each non-data line is skipped; "data: [DONE]" terminates the stream; every
// other data line is a JSON chunk whose delta.content is emitted to onDelta.
func (p *openAIProvider) Stream(ctx context.Context, req Request, onDelta DeltaFunc) (string, error) {
	body := openAIRequest{
		Model: p.model,
		Messages: []openAIMessage{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai-compatible API unavailable: %w", err)
	}
	defer resp.Body.Close()

	var accum strings.Builder
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
		if payload == "[DONE]" {
			break
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta != "" {
			onDelta(delta)
			accum.WriteString(delta)
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return accum.String(), fmt.Errorf("openai stream: %w", err)
	}
	if accum.Len() == 0 {
		return "", ErrEmptyResponse
	}
	return accum.String(), nil
}
