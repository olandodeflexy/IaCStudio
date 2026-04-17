package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFactoryInfersKindFromCredentials covers the backwards-compatibility
// behaviour expected by existing callers: a non-empty APIKey implies
// OpenAI-compatible, an empty APIKey implies Ollama. Explicit Kind always
// wins.
func TestFactoryInfersKindFromCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want Kind
		err  bool
	}{
		{"empty key defaults to ollama", Config{Endpoint: "http://x", Model: "m"}, KindOllama, false},
		{"non-empty key infers openai", Config{Endpoint: "http://x", Model: "m", APIKey: "sk-1234"}, KindOpenAI, false},
		{"explicit ollama wins over key", Config{Kind: KindOllama, APIKey: "sk-ignored", Endpoint: "http://x", Model: "m"}, KindOllama, false},
		{"openai requires key", Config{Kind: KindOpenAI, Endpoint: "http://x", Model: "m"}, "", true},
		{"anthropic with key", Config{Kind: KindAnthropic, APIKey: "sk-ant-123", Model: "claude-opus-4-7"}, KindAnthropic, false},
		{"anthropic requires key", Config{Kind: KindAnthropic, Model: "claude-opus-4-7"}, "", true},
		{"unknown kind rejected", Config{Kind: "palm", Model: "m"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(tc.cfg)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got provider %T", p)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Kind() != tc.want {
				t.Errorf("Kind() = %q, want %q", p.Kind(), tc.want)
			}
		})
	}
}

// TestOllamaProviderComplete drives the ollama provider against a stubbed
// HTTP server so we can verify wire-format expectations (endpoint path, JSON
// format flag, system+user concatenation) without a live Ollama.
func TestOllamaProviderComplete(t *testing.T) {
	var captured ollamaRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("ollama must POST to /api/generate, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(ollamaResponse{Response: `{"ok":true}`})
	}))
	defer srv.Close()

	p := NewOllama(Config{Endpoint: srv.URL, Model: "llama3"})
	got, err := p.Complete(context.Background(), Request{
		System: "you are helpful", User: "hello", JSONMode: true,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != `{"ok":true}` {
		t.Errorf("Complete() = %q", got)
	}
	if captured.Model != "llama3" {
		t.Errorf("Model = %q, want llama3", captured.Model)
	}
	if captured.Format != "json" {
		t.Errorf("JSONMode should set Format=json, got %q", captured.Format)
	}
	if !strings.Contains(captured.Prompt, "you are helpful") || !strings.Contains(captured.Prompt, "hello") {
		t.Errorf("Prompt should concat system+user, got %q", captured.Prompt)
	}
}

// TestOllamaProviderEmptyResponse ensures an empty body surfaces as
// ErrEmptyResponse so callers can trigger their fallback path.
func TestOllamaProviderEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaResponse{Response: ""})
	}))
	defer srv.Close()

	p := NewOllama(Config{Endpoint: srv.URL, Model: "m"})
	_, err := p.Complete(context.Background(), Request{System: "s", User: "u"})
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("want ErrEmptyResponse, got %v", err)
	}
}

// TestOpenAIProviderComplete verifies messages array, Bearer auth, and
// endpoint normalisation against a stubbed OpenAI-compatible server.
func TestOpenAIProviderComplete(t *testing.T) {
	var captured openAIRequest
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("openai must POST to /chat/completions, got %s", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("bad request: %v", err)
		}
		resp := openAIResponse{}
		resp.Choices = []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{{Message: struct {
			Content string `json:"content"`
		}{Content: "ok"}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Pass the bare base URL — normaliseOpenAIEndpoint should append the
	// /chat/completions suffix.
	p := NewOpenAI(Config{Endpoint: srv.URL, Model: "gpt-4", APIKey: "sk-test"})
	got, err := p.Complete(context.Background(), Request{
		System: "sys", User: "usr", Temperature: 0.5, MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "ok" {
		t.Errorf("Complete() = %q", got)
	}
	if authHeader != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", authHeader)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || captured.Messages[0].Content != "sys" {
		t.Errorf("system message wrong: %+v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" || captured.Messages[1].Content != "usr" {
		t.Errorf("user message wrong: %+v", captured.Messages[1])
	}
	if captured.Temperature != 0.5 || captured.MaxTokens != 128 {
		t.Errorf("sampling knobs dropped: %+v", captured)
	}
}

// TestOpenAIProviderError propagates the provider's structured error body so
// users see the actual upstream message rather than a generic one.
func TestOpenAIProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(Config{Endpoint: srv.URL, Model: "m", APIKey: "sk"})
	_, err := p.Complete(context.Background(), Request{System: "s", User: "u"})
	if err == nil || !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Fatalf("want upstream error message, got %v", err)
	}
}

// TestAnthropicProviderComplete exercises the Messages API shape: top-level
// system field (not a message), messages array with just user turn, required
// max_tokens, and Anthropic's specific headers (x-api-key + anthropic-version).
func TestAnthropicProviderComplete(t *testing.T) {
	var captured anthropicRequest
	var gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			t.Errorf("anthropic must POST to /v1/messages, got %s", r.URL.Path)
		}
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("bad request: %v", err)
		}
		_, _ = w.Write([]byte(`{
			"content": [
				{"type":"text","text":"hello "},
				{"type":"text","text":"world"}
			],
			"usage": {"input_tokens": 10, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	p := NewAnthropic(Config{Endpoint: srv.URL, Model: "claude-opus-4-7", APIKey: "sk-ant-xyz"})
	got, err := p.Complete(context.Background(), Request{
		System: "you are helpful", User: "hi", MaxTokens: 256, Temperature: 0.2,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Multiple text blocks should be concatenated rather than dropped.
	if got != "hello world" {
		t.Errorf("Complete() = %q, want hello world", got)
	}
	if gotKey != "sk-ant-xyz" {
		t.Errorf("x-api-key header = %q", gotKey)
	}
	if gotVersion != DefaultAnthropicVersion {
		t.Errorf("anthropic-version header = %q, want %s", gotVersion, DefaultAnthropicVersion)
	}
	if captured.System != "you are helpful" {
		t.Errorf("System should live at top level, got %q", captured.System)
	}
	if len(captured.Messages) != 1 || captured.Messages[0].Role != "user" {
		t.Errorf("expected single user message, got %+v", captured.Messages)
	}
	if captured.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256", captured.MaxTokens)
	}
}

// TestAnthropicProviderDefaultMaxTokens guarantees we never send max_tokens=0
// (which the Messages API rejects) when the caller forgets to set it.
func TestAnthropicProviderDefaultMaxTokens(t *testing.T) {
	var captured anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	p := NewAnthropic(Config{Endpoint: srv.URL, Model: "m", APIKey: "sk"})
	_, err := p.Complete(context.Background(), Request{System: "s", User: "u"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if captured.MaxTokens == 0 {
		t.Error("MaxTokens must default to a non-zero value for the Messages API")
	}
}

// TestAnthropicProviderError surfaces the typed error message from the API.
func TestAnthropicProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad model"}}`))
	}))
	defer srv.Close()

	p := NewAnthropic(Config{Endpoint: srv.URL, Model: "m", APIKey: "sk"})
	_, err := p.Complete(context.Background(), Request{System: "s", User: "u"})
	if err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("want upstream error message, got %v", err)
	}
}

// TestOpenAIEndpointNormalisation confirms users can paste either form of URL.
func TestOpenAIEndpointNormalisation(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := normaliseOpenAIEndpoint(tc.in); got != tc.want {
				t.Errorf("normaliseOpenAIEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
