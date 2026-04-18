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
	Done     bool   `json:"done"`
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return "", fmt.Errorf("ollama request failed: status %s (failed to read response body: %w)", resp.Status, readErr)
		}
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			return "", fmt.Errorf("ollama request failed: status %s", resp.Status)
		}
		return "", fmt.Errorf("ollama request failed: status %s: %s", resp.Status, msg)
	}

	var decoded ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if decoded.Response == "" {
		return "", ErrEmptyResponse
	}
	return decoded.Response, nil
}

// Stream reads Ollama's NDJSON stream (one JSON object per line, each with a
// "response" chunk and a "done" flag) and invokes onDelta for each non-empty
// chunk. The accumulated text is returned on completion.
func (p *ollamaProvider) Stream(ctx context.Context, req Request, onDelta DeltaFunc) (string, error) {
	body := ollamaRequest{
		Model:  p.model,
		Prompt: req.System + "\n\n" + req.User,
		Stream: true,
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

	scanner := bufio.NewScanner(resp.Body)
	// Ollama responses can legitimately exceed the default 64KB scan buffer
	// when a single line carries a large chunk of generated text.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var accum strings.Builder
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return accum.String(), err
		}
		var chunk ollamaResponse
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			continue // skip malformed lines rather than aborting the stream
		}
		if chunk.Response != "" {
			onDelta(chunk.Response)
			accum.WriteString(chunk.Response)
		}
		if chunk.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return accum.String(), fmt.Errorf("ollama stream: %w", err)
	}
	if accum.Len() == 0 {
		return "", ErrEmptyResponse
	}
	return accum.String(), nil
}
