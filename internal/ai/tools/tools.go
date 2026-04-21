// Package tools is the agent's function-calling surface.
//
// A Tool is a typed Go function the LLM can invoke by name — list_resources,
// run_plan, run_policy, write_hcl, and so on. The provider decides when to
// call which tool; this package owns the registry, the JSON schema the
// provider sees, and the loop that marshals arguments in, runs the Go
// handler, and marshals results back.
//
// The design is intentionally provider-agnostic: the Anthropic tool-use
// wire format is what drove the shape, but a future OpenAI function-
// calling adapter plugs into the same Registry + Runner without touching
// any tool implementation.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Definition is how a tool advertises itself to the LLM. Schema is a
// JSON Schema object describing the tool's input arguments — providers
// forward it verbatim to the model.
type Definition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"input_schema"`
}

// Handler is the Go-side implementation that runs when the model calls a
// tool. args is the raw JSON the model emitted; the handler is responsible
// for decoding it into whatever typed shape it expects.
//
// Return value semantics:
//   - result: any JSON-serialisable value. It becomes the tool_result the
//     model sees on its next turn. Use a struct or map[string]any.
//   - error: surfaced to the model as a tool_result with "error" flag set
//     so the model can decide whether to retry or give up. Fatal errors
//     that should abort the whole loop are signalled via a sentinel
//     ErrAbort rather than returning a generic error — see Runner.
type Handler func(ctx context.Context, args json.RawMessage) (result any, err error)

// Tool pairs one Definition with its Handler. Authors don't construct Tool
// directly; use New.
type Tool struct {
	Def     Definition
	Handler Handler
}

// New builds a Tool. schema must be a valid JSON Schema object (raw bytes);
// keeping it as json.RawMessage rather than a struct spares us from
// reinventing JSON Schema in Go just to serialise it back out.
func New(name, description string, schema string, handler Handler) Tool {
	return Tool{
		Def: Definition{
			Name:        name,
			Description: description,
			Schema:      json.RawMessage(schema),
		},
		Handler: handler,
	}
}

// Registry holds the tools available to the agent. It's a concrete type
// rather than an interface because we need reflective access to the whole
// set (Definitions() for the model, Lookup() for the runner).
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. A later registration with the same name replaces
// the earlier one — useful for tests that swap a real handler for a stub.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Def.Name] = t
}

// Lookup returns the named tool. The second return is false when the model
// hallucinates a tool name; the Runner surfaces that as a tool_result with
// "unknown tool" so the model can correct itself instead of looping forever.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns every registered tool's metadata, sorted by name for
// deterministic output. Providers call this to build the tools array sent
// with each model turn.
func (r *Registry) Definitions() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]Definition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Def)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// Names returns every registered tool name, sorted — handy for logs and
// routing decisions that don't need the full schema.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ToolCall is the model's request to invoke a tool. ID is the model's own
// correlation token (different providers use different shapes — Anthropic's
// "tool_use" has an id field, OpenAI's has tool_call_id — we normalise to
// ID here). Args is the raw JSON the model emitted.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult is what the runner produces for each ToolCall. It's the value
// the provider feeds back to the model on the next turn. Error captures
// handler failures so the model can see them and decide how to react.
type ToolResult struct {
	CallID  string
	Content any    // handler's return value (or an error-shaped object)
	IsError bool
}

// ErrAbort is the sentinel handlers return to tell the Runner to stop the
// whole loop rather than feed the error back to the model. Use this when
// continuing would be unsafe (e.g. the project directory disappeared under
// us, the user revoked permission).
var ErrAbort = errors.New("tool loop aborted")

// Runner executes a batch of ToolCalls against the Registry. It's the
// generic piece of the agent loop — provider-specific code only decides
// which ToolCalls to pass in and what to do with the ToolResults.
type Runner struct {
	Registry *Registry
	// MaxIterations is a safety cap on how many tool-call rounds the agent
	// can run before we declare it stuck. Providers enforce this by
	// counting their own tool-use turns; the Runner enforces it per Run()
	// call so a broken registry entry can't spin forever.
	MaxIterations int
}

// Run executes every call in sequence (models typically emit one per turn,
// but Anthropic can batch), returning the resulting ToolResults in the same
// order. An ErrAbort from any handler stops immediately and is returned
// alongside whatever results accumulated. If MaxIterations is non-zero and
// the batch exceeds it, the surplus calls are silently dropped — providers
// should never send a batch larger than the declared limit.
func (r *Runner) Run(ctx context.Context, calls []ToolCall) ([]ToolResult, error) {
	if r.MaxIterations > 0 && len(calls) > r.MaxIterations {
		calls = calls[:r.MaxIterations]
	}
	out := make([]ToolResult, 0, len(calls))
	for _, call := range calls {
		result := ToolResult{CallID: call.ID}
		tool, ok := r.Registry.Lookup(call.Name)
		if !ok {
			// Hallucinated name — hand back an error result so the model
			// can correct itself. This is NOT fatal to the loop.
			result.Content = map[string]string{
				"error": fmt.Sprintf("unknown tool: %s", call.Name),
			}
			result.IsError = true
			out = append(out, result)
			continue
		}
		value, err := tool.Handler(ctx, call.Args)
		if err != nil {
			if errors.Is(err, ErrAbort) {
				return out, ErrAbort
			}
			// Any other error becomes a visible tool_result so the model
			// can see what went wrong and potentially retry with different
			// arguments.
			result.Content = map[string]string{"error": err.Error()}
			result.IsError = true
		} else {
			result.Content = value
		}
		out = append(out, result)
	}
	return out, nil
}
