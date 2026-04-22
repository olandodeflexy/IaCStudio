package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CompleteWithImages is the VisionUser implementation for Anthropic.
// Each Image becomes a content block inside the user message, ahead of
// the text prompt. An empty images slice falls through to Complete so
// callers don't have to branch.
//
// Anthropic encodes image bytes as base64 in a content block of type
// "image". We do the encoding here (not at the caller) so multipart
// uploads can stream raw bytes straight through. Media types supported
// by the API: image/png, image/jpeg, image/webp, image/gif.
func (p *anthropicProvider) CompleteWithImages(ctx context.Context, req Request, images []Image) (string, error) {
	if len(images) == 0 {
		return p.Complete(ctx, req)
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	// Build a content-block array: images first (the model attends more
	// consistently when attachments precede the text), then the user
	// prompt text. Anthropic's docs explicitly recommend this ordering
	// for diagram-to-structure tasks.
	content := make([]any, 0, len(images)+1)
	for _, img := range images {
		if img.MediaType == "" || len(img.Data) == 0 {
			continue
		}
		content = append(content, anthropicImageBlock{
			Type: "image",
			Source: anthropicImageSource{
				Type:      "base64",
				MediaType: img.MediaType,
				Data:      base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	if strings.TrimSpace(req.User) != "" {
		content = append(content, anthropicTextBlock{Type: "text", Text: req.User})
	}
	if len(content) == 0 {
		return "", fmt.Errorf("anthropic vision: no usable image or text in request")
	}

	reqBody := anthropicVisionRequest{
		Model:       p.model,
		System:      buildSystemField(req.System, req.Cacheable),
		Messages:    []anthropicVisionMessage{{Role: "user", Content: content}},
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
	}
	raw, _ := json.Marshal(reqBody)

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
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read anthropic response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var apiErr anthropicResponse
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error != nil && apiErr.Error.Message != "" {
			return "", fmt.Errorf("anthropic API error (status %d): %s", resp.StatusCode, apiErr.Error.Message)
		}
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return "", fmt.Errorf("anthropic API error (status %d): %s", resp.StatusCode, msg)
		}
		return "", fmt.Errorf("anthropic API error (status %d)", resp.StatusCode)
	}

	var decoded anthropicResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("anthropic API error: %s", decoded.Error.Message)
	}
	p.recordUsage(decoded.Usage)
	if len(decoded.Content) == 0 {
		return "", ErrEmptyResponse
	}
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

// ─── Vision wire structs ─────────────────────────────────────────

// anthropicVisionMessage differs from anthropicMessage only in Content:
// the vision path needs a content-block array (image + text), the
// existing Complete path keeps the simpler string shape to minimise
// diff from the established wire format.
type anthropicVisionMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicVisionRequest struct {
	Model       string                   `json:"model"`
	System      any                      `json:"system,omitempty"`
	Messages    []anthropicVisionMessage `json:"messages"`
	MaxTokens   int                      `json:"max_tokens"`
	Temperature float64                  `json:"temperature,omitempty"`
}

type anthropicImageBlock struct {
	Type   string               `json:"type"` // always "image"
	Source anthropicImageSource `json:"source"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`       // always "base64" today
	MediaType string `json:"media_type"` // "image/png" | "image/jpeg" | "image/webp" | "image/gif"
	Data      string `json:"data"`       // base64-encoded image bytes
}

type anthropicTextBlock struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}
