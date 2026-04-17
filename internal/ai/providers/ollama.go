package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ollamaProvider talks to a local Ollama server's /api/generate endpoint.
//
// Ollama's request format deliberately diverges from OpenAI — it takes a single
// Prompt string rather than a Messages array — so we concatenate the system
// and user prompts with a blank line between them, which matches what the
// pre-refactor bridge did.
type ollamaProvider struct {
	endpoint string
	model    string
	client   *http.Client
}

// NewOllama constructs a Provider that speaks to an Ollama instance.
func NewOllama(cfg Config) Provider {
	return &ollamaProvider{
		endpoint: strings.TrimSuffix(cfg.Endpoint, "/"),
		model:    cfg.Model,
		client:   defaultHTTPClient(cfg.Timeout),
	}
}

func (p *ollamaProvider) Kind() Kind { return KindOllama }

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Format string `json:"format,omitempty"`
}

type ollamaResponse struct {
	Response string `json:"response"`
}

func (p *ollamaProvider) Complete(ctx context.Context, req Request) (string, error) {
	body := ollamaRequest{
		Model:  p.model,
		Prompt: req.System + "\n\n" + req.User,
		Stream: false,
	}
	if req.JSONMode {
		body.Format = "json"
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/api/generate", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama unavailable: %w", err)
	}
	defer resp.Body.Close()

	var decoded ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if decoded.Response == "" {
		return "", ErrEmptyResponse
	}
	return decoded.Response, nil
}
