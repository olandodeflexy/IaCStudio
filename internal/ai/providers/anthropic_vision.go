package providers

import (
	"context"
	"encoding/base64"
	"fmt"
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
		MaxTokens:   clampMaxTokens(req.MaxTokens),
		Temperature: req.Temperature,
	}
	// Shared round-trip: same headers, error handling, usage
	// bookkeeping, and content-block concatenation as Complete.
	return p.postMessagesAndExtractText(ctx, reqBody)
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
