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
	"encoding/json"
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
	// if it natively supports such a mode (for example, Ollama's
	// "format":"json"). Providers that don't support it ignore the hint.
	JSONMode bool
	// Cacheable asks the provider to cache the System prompt across calls
	// when it supports prompt caching. Today only Anthropic honours this
	// (via cache_control on the system block). Ignored elsewhere. When the
	// system prompt is below a provider's minimum cacheable size, the hint
	// is silently dropped by the provider.
	Cacheable bool
}

// DeltaFunc receives incremental text chunks as they arrive from the LLM.
// It MUST NOT block on the caller's UI — implementations of Provider.Stream
// call this inline during the SSE/NDJSON read loop, so slow callbacks back
// up the whole stream.
type DeltaFunc func(delta string)

// Provider is the minimum surface every LLM backend must implement.
type Provider interface {
	// Kind returns the stable identifier of this provider ("ollama" | …).
	Kind() Kind
	// Complete returns the full assistant reply for the given request.
	Complete(ctx context.Context, req Request) (string, error)
	// Stream invokes onDelta for each chunk as it arrives and returns the
	// full accumulated text on success. Cancel via ctx to stop mid-stream.
	// Providers that lack native streaming should fall back to a single
	// onDelta call with the full Complete response so callers always see at
	// least one delta before the final return.
	Stream(ctx context.Context, req Request, onDelta DeltaFunc) (string, error)
}

// ToolDefinition is what a tool advertises to the model via the provider —
// matches the shape of ai/tools.Definition. Kept in the providers package
// so we avoid an import cycle (tools depends on parser/policy/scanners;
// providers depends on neither).
//
// InputSchema is json.RawMessage rather than []byte because this struct
// gets marshalled by several providers (and surfaced in audit logs) —
// []byte would base64-encode under encoding/json, which is NOT what any
// consumer expects for a JSON Schema. RawMessage keeps the bytes raw.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolCall is the model's request to invoke a tool — the provider-neutral
// shape the caller's tool runner consumes. Args is json.RawMessage for the
// same "don't base64-encode our JSON" reason as ToolDefinition.InputSchema.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult is the caller's response to a ToolCall — becomes the next
// tool_result message the provider sends back to the model.
type ToolResult struct {
	CallID  string
	Content any  // JSON-serialisable; the provider marshals it
	IsError bool
}

// ToolRunner executes a batch of ToolCalls and returns ToolResults. The
// provider calls this between model turns. Implementations in the ai/tools
// package plug their Runner in via a small adapter.
type ToolRunner interface {
	Run(ctx context.Context, calls []ToolCall) ([]ToolResult, error)
}

// ToolLoopRequest drives Provider.RunToolLoop. System and User are the same
// as in Request; Tools is the catalogue advertised to the model each turn;
// Runner handles the calls. MaxTurns caps how many tool-use iterations
// run before the provider gives up and returns whatever text it has.
type ToolLoopRequest struct {
	System      string
	User        string
	Temperature float64
	MaxTokens   int
	Tools       []ToolDefinition
	Runner      ToolRunner
	// MaxTurns caps the number of model turns. Each tool-call round counts
	// as one turn. A model that never stops calling tools is a common
	// failure mode; this is the safety net. Zero → provider default (8).
	MaxTurns int
}

// ToolUser is the optional interface providers implement when they support
// function calling / tool use. The multi-agent orchestrator type-asserts
// for this — providers that don't implement it fall back to plain Complete
// and lose the agent feature gracefully.
type ToolUser interface {
	// RunToolLoop runs the model in a tool-use loop: call model → if the
	// response requests tools, run them via req.Runner → feed results back
	// → repeat until the model returns final text (or MaxTurns is hit).
	// The returned string is the final assistant text; transcripts are
	// optional and provider-specific.
	RunToolLoop(ctx context.Context, req ToolLoopRequest) (string, error)
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
