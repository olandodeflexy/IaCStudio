package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeRunner implements ToolRunner by mapping tool name → response. Used to
// script the model's tool calls without pulling in the full ai/tools package.
type fakeRunner struct {
	responses map[string]any
	calls     []ToolCall
}

func (r *fakeRunner) Run(_ context.Context, calls []ToolCall) ([]ToolResult, error) {
	r.calls = append(r.calls, calls...)
	out := make([]ToolResult, 0, len(calls))
	for _, c := range calls {
		if resp, ok := r.responses[c.Name]; ok {
			out = append(out, ToolResult{CallID: c.ID, Content: resp})
			continue
		}
		out = append(out, ToolResult{CallID: c.ID, Content: map[string]string{"error": "no stub"}, IsError: true})
	}
	return out, nil
}

// TestAnthropicRunToolLoopTwoTurns scripts a canonical pattern: the model
// calls one tool, we feed the result back, the model returns final text.
// Verifies the loop stops on end_turn, preserves message history, and
// returns the concatenated final text.
func TestAnthropicRunToolLoopTwoTurns(t *testing.T) {
	var turn int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&turn, 1)
		// Decode the request so we can assert history is being carried
		// across turns correctly.
		body, _ := io.ReadAll(r.Body)
		var in anthropicToolLoopRequest
		_ = json.Unmarshal(body, &in)

		if n == 1 {
			// Sanity: first turn carries just the user message.
			if len(in.Messages) != 1 || in.Messages[0].Role != "user" {
				t.Errorf("first turn should carry only the user message, got %+v", in.Messages)
			}
			_, _ = w.Write([]byte(`{
                "content": [
                    {"type":"text","text":"Let me check the resources."},
                    {"type":"tool_use","id":"call-1","name":"list_resources","input":{}}
                ],
                "stop_reason":"tool_use"
            }`))
			return
		}
		// Second turn: history must now be [user, assistant (with tool_use),
		// user (with tool_result)].
		if len(in.Messages) != 3 {
			t.Errorf("second turn should have 3 messages, got %d: %+v", len(in.Messages), in.Messages)
		}
		// The tool_result must reference the model's original call-1 id.
		last := in.Messages[len(in.Messages)-1]
		if last.Role != "user" || len(last.Content) != 1 || last.Content[0].ToolUseID != "call-1" {
			t.Errorf("tool_result message wrong: %+v", last)
		}
		_, _ = w.Write([]byte(`{
            "content": [{"type":"text","text":"Found 2 resources. All looks fine."}],
            "stop_reason":"end_turn"
        }`))
	}))
	defer srv.Close()

	p := NewAnthropic(Config{Endpoint: srv.URL, Model: "claude-opus-4-7", APIKey: "sk-test"})
	tu, ok := p.(ToolUser)
	if !ok {
		t.Fatalf("anthropic provider must implement ToolUser")
	}

	runner := &fakeRunner{responses: map[string]any{
		"list_resources": map[string]any{"count": 2},
	}}
	out, err := tu.RunToolLoop(context.Background(), ToolLoopRequest{
		System: "you are an agent",
		User:   "do the thing",
		Tools: []ToolDefinition{
			{Name: "list_resources", Description: "list", InputSchema: []byte(`{"type":"object"}`)},
		},
		Runner:   runner,
		MaxTurns: 4,
	})
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if !strings.Contains(out, "All looks fine") {
		t.Errorf("final text wrong: %q", out)
	}
	if len(runner.calls) != 1 || runner.calls[0].Name != "list_resources" {
		t.Errorf("runner should have been invoked once with list_resources, got %+v", runner.calls)
	}
	if atomic.LoadInt32(&turn) != 2 {
		t.Errorf("expected exactly 2 HTTP turns, got %d", turn)
	}
}

// TestAnthropicRunToolLoopMaxTurns — a model that keeps asking for tools
// without ever returning end_turn must be cut off. The returned text is
// whatever was in the final assistant message, plus a clear error.
func TestAnthropicRunToolLoopMaxTurns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
            "content":[
                {"type":"text","text":"still working..."},
                {"type":"tool_use","id":"c","name":"list_resources","input":{}}
            ],
            "stop_reason":"tool_use"
        }`))
	}))
	defer srv.Close()

	p := NewAnthropic(Config{Endpoint: srv.URL, Model: "m", APIKey: "sk"}).(ToolUser)
	runner := &fakeRunner{responses: map[string]any{"list_resources": map[string]int{"count": 0}}}
	_, err := p.RunToolLoop(context.Background(), ToolLoopRequest{
		System: "s", User: "u",
		Tools:    []ToolDefinition{{Name: "list_resources", InputSchema: []byte(`{"type":"object"}`)}},
		Runner:   runner,
		MaxTurns: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "max turns") {
		t.Errorf("should hit max turns limit, got: %v", err)
	}
	// Runner should have been called MaxTurns times (one per round).
	if len(runner.calls) != 3 {
		t.Errorf("runner should be called MaxTurns=3 times, got %d", len(runner.calls))
	}
}

// TestAnthropicRunToolLoopRunnerAbort — when a handler returns ErrAbort
// via the runner's error return, the loop stops immediately and surfaces
// the error.
func TestAnthropicRunToolLoopRunnerAbort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
            "content":[{"type":"tool_use","id":"c","name":"t","input":{}}],
            "stop_reason":"tool_use"
        }`))
	}))
	defer srv.Close()

	p := NewAnthropic(Config{Endpoint: srv.URL, Model: "m", APIKey: "sk"}).(ToolUser)

	abortErr := errors.New("user revoked permission")
	runner := &abortingRunner{err: abortErr}
	_, err := p.RunToolLoop(context.Background(), ToolLoopRequest{
		System: "s", User: "u",
		Tools:  []ToolDefinition{{Name: "t", InputSchema: []byte(`{}`)}},
		Runner: runner,
	})
	if !errors.Is(err, abortErr) {
		t.Errorf("expected runner abort to propagate, got %v", err)
	}
}

type abortingRunner struct{ err error }

func (r *abortingRunner) Run(_ context.Context, _ []ToolCall) ([]ToolResult, error) {
	return nil, r.err
}

// TestAnthropicRunToolLoopHTTPError — a non-2xx response with a usable
// error body must surface the message verbatim.
func TestAnthropicRunToolLoopHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = fmt.Fprint(w, `{"error":{"message":"rate limit exceeded"}}`)
	}))
	defer srv.Close()

	p := NewAnthropic(Config{Endpoint: srv.URL, Model: "m", APIKey: "sk"}).(ToolUser)
	_, err := p.RunToolLoop(context.Background(), ToolLoopRequest{
		System: "s", User: "u", Runner: &fakeRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("API error should surface, got %v", err)
	}
}
