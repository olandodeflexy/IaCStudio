package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAnthropicVision_ImageContentBlockShape — the canonical wire check:
// a single PNG attachment + text prompt produces a user message whose
// content array has the image block first (per Anthropic's diagram-to-
// structure guidance) and the text block second.
func TestAnthropicVision_ImageContentBlockShape(t *testing.T) {
	var received anthropicVisionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()

	p := &anthropicProvider{
		endpoint:   srv.URL,
		model:      "claude-opus-4-7",
		apiKey:     "test-key",
		apiVersion: DefaultAnthropicVersion,
		client:     &http.Client{},
	}

	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	out, err := p.CompleteWithImages(context.Background(), Request{
		System: "you analyse diagrams",
		User:   "describe this",
	}, []Image{{MediaType: "image/png", Data: png}})
	if err != nil {
		t.Fatalf("CompleteWithImages: %v", err)
	}
	if out != "ok" {
		t.Errorf("want 'ok', got %q", out)
	}

	if len(received.Messages) != 1 || received.Messages[0].Role != "user" {
		t.Fatalf("want single user message, got %+v", received.Messages)
	}
	blocks, ok := received.Messages[0].Content.([]any)
	if !ok {
		t.Fatalf("Content should be an array of blocks, got %T", received.Messages[0].Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks (image + text), got %d", len(blocks))
	}

	first, _ := blocks[0].(map[string]any)
	if first["type"] != "image" {
		t.Errorf("first block should be image, got %v", first["type"])
	}
	src, _ := first["source"].(map[string]any)
	if src["media_type"] != "image/png" {
		t.Errorf("wrong media_type: %v", src["media_type"])
	}
	decoded, err := base64.StdEncoding.DecodeString(src["data"].(string))
	if err != nil || string(decoded) != string(png) {
		t.Errorf("image bytes did not round-trip base64")
	}

	second, _ := blocks[1].(map[string]any)
	if second["type"] != "text" || second["text"] != "describe this" {
		t.Errorf("second block wrong: %+v", second)
	}
}

func TestAnthropicVision_EmptyImagesFallsBackToComplete(t *testing.T) {
	// When images is empty, we expect a plain Complete — the wire shape
	// has string Content on the user message, not a block array.
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()

	p := &anthropicProvider{
		endpoint:   srv.URL,
		model:      "claude-opus-4-7",
		apiKey:     "test-key",
		apiVersion: DefaultAnthropicVersion,
		client:     &http.Client{},
	}
	if _, err := p.CompleteWithImages(context.Background(), Request{User: "hi"}, nil); err != nil {
		t.Fatalf("CompleteWithImages: %v", err)
	}

	// The fallback goes through Complete → plain anthropicRequest with
	// Content as string. Verify the body doesn't carry the vision shape.
	if !strings.Contains(string(raw), `"content":"hi"`) {
		t.Errorf("expected Complete string-form body, got %s", string(raw))
	}
}

func TestAnthropicVision_DropsEmptyImages(t *testing.T) {
	var received anthropicVisionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &received)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()

	p := &anthropicProvider{
		endpoint:   srv.URL,
		model:      "m",
		apiKey:     "k",
		apiVersion: DefaultAnthropicVersion,
		client:     &http.Client{},
	}
	_, err := p.CompleteWithImages(context.Background(), Request{User: "hi"}, []Image{
		{MediaType: "", Data: []byte("x")}, // no media type
		{MediaType: "image/png", Data: nil}, // empty data
		{MediaType: "image/png", Data: []byte("real")},
	})
	if err != nil {
		t.Fatalf("CompleteWithImages: %v", err)
	}
	blocks, _ := received.Messages[0].Content.([]any)
	if len(blocks) != 2 {
		t.Errorf("want 1 image + 1 text block, got %d", len(blocks))
	}
}

func TestAnthropicVision_SatisfiesVisionUserInterface(t *testing.T) {
	// Compile-time assertion: anthropicProvider implements VisionUser.
	var _ VisionUser = &anthropicProvider{}
}
