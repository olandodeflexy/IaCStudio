// Package providers is the transport layer for language-model calls.
//
// Each supported backend (Ollama, OpenAI-compatible, Anthropic, …) implements
// the Provider interface so the rest of the AI package can stay unaware of
// wire formats, auth schemes, or per-provider quirks.
//
// The bridge.go caller builds a Request (a system prompt, a user prompt, and
// a few shared knobs) and hands it to whichever Provider the user configured.
// Adding a new backend is a matter of dropping a file in this package that
// satisfies the interface and teaching the factory how to build it.
package providers

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Kind is the stable identifier for a provider type, as seen by the user in
// settings and serialized in ProviderConfig.
type Kind string

const (
	KindOllama    Kind = "ollama"
	KindOpenAI    Kind = "openai"
	KindAnthropic Kind = "anthropic"
)

// Config carries everything needed to construct a Provider. Fields that
// don't apply to a given Kind are ignored.
type Config struct {
	Kind     Kind
	Endpoint string
	Model    string
	APIKey   string
	// Timeout caps the end-to-end LLM round-trip. Defaults to 5 minutes to
	// accommodate large local models on slow machines.
	Timeout time.Duration
}

// Request is the provider-agnostic shape handed to Provider.Complete. Fields
// map to roughly the same idea in every backend — system prompt, user prompt,
// sampling knobs — even when each wire format represents them differently.
type Request struct {
	System      string
	User        string
	Temperature float64 // 0.0-1.0; providers clamp as needed
	MaxTokens   int     // upper bound on response length; 0 = provider default
	// JSONMode hints that the provider should constrain the response to JSON
	// if it natively supports such a mode (Ollama's "format":"json", OpenAI's
	// response_format, etc.). Providers that don't support it ignore the hint.
	JSONMode bool
}

// Provider is the minimum surface every LLM backend must implement.
// Streaming will be added as a second method in a follow-up commit once the
// refactor lands; the single-shot Complete signature is enough to keep
// feature parity with the original bridge for now.
type Provider interface {
	// Kind returns the stable identifier of this provider ("ollama" | …).
	Kind() Kind
	// Complete returns the full assistant reply for the given request.
	Complete(ctx context.Context, req Request) (string, error)
}

// ErrEmptyResponse is returned when a provider round-trips successfully but
// the body contains no usable content — separated out so callers can fall
// back to deterministic heuristics (pattern matching) instead of surfacing a
// generic error.
var ErrEmptyResponse = errors.New("empty response from provider")

// defaultHTTPClient constructs the shared HTTP client used by provider
// implementations. Timeout defaults to 5 minutes to mirror the pre-refactor
// OllamaClient behaviour.
func defaultHTTPClient(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	return &http.Client{Timeout: timeout}
}
