package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestRegistryRoundTrip — Register + Lookup + Definitions should round-trip
// cleanly, and Definitions must be sorted so the model sees a stable
// schema feed across server restarts.
func TestRegistryRoundTrip(t *testing.T) {
	reg := NewRegistry()
	reg.Register(New("zebra", "last alphabetically", `{"type":"object"}`,
		func(ctx context.Context, args json.RawMessage) (any, error) { return nil, nil }))
	reg.Register(New("alpha", "first alphabetically", `{"type":"object"}`,
		func(ctx context.Context, args json.RawMessage) (any, error) { return nil, nil }))

	if _, ok := reg.Lookup("alpha"); !ok {
		t.Error("Lookup should find registered tool")
	}
	if _, ok := reg.Lookup("ghost"); ok {
		t.Error("Lookup should return false for unknown tool")
	}

	defs := reg.Definitions()
	if len(defs) != 2 {
		t.Fatalf("want 2 definitions, got %d", len(defs))
	}
	if defs[0].Name != "alpha" || defs[1].Name != "zebra" {
		t.Errorf("definitions must be sorted by name: %+v", defs)
	}

	names := reg.Names()
	if names[0] != "alpha" || names[1] != "zebra" {
		t.Errorf("names must be sorted: %+v", names)
	}
}

// TestRegistryReplaceOnDuplicateRegister — test fixtures commonly swap the
// real handler for a stub; a second Register with the same name must win.
func TestRegistryReplaceOnDuplicateRegister(t *testing.T) {
	reg := NewRegistry()
	called := ""
	reg.Register(New("echo", "first", `{}`,
		func(ctx context.Context, args json.RawMessage) (any, error) { called = "first"; return nil, nil }))
	reg.Register(New("echo", "second", `{}`,
		func(ctx context.Context, args json.RawMessage) (any, error) { called = "second"; return nil, nil }))

	got, _ := reg.Lookup("echo")
	_, _ = got.Handler(context.Background(), nil)
	if called != "second" {
		t.Errorf("last Register should win, called = %q", called)
	}
	if got.Def.Description != "second" {
		t.Errorf("last definition should win, desc = %q", got.Def.Description)
	}
}

// TestRunnerHappyPath — a batch of calls dispatches to the right handlers
// and returns results in the same order, with CallIDs preserved.
func TestRunnerHappyPath(t *testing.T) {
	reg := NewRegistry()
	reg.Register(New("double", "doubles n", `{"type":"object"}`,
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var in struct{ N int }
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, err
			}
			return map[string]int{"result": in.N * 2}, nil
		}))

	runner := &Runner{Registry: reg, MaxIterations: 10}
	calls := []ToolCall{
		{ID: "call-1", Name: "double", Args: json.RawMessage(`{"n":3}`)},
		{ID: "call-2", Name: "double", Args: json.RawMessage(`{"n":5}`)},
	}
	results, err := runner.Run(context.Background(), calls)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].CallID != "call-1" || results[1].CallID != "call-2" {
		t.Errorf("CallIDs out of order: %+v", results)
	}
	if results[0].IsError || results[1].IsError {
		t.Errorf("happy path results should not be flagged IsError: %+v", results)
	}
}

// TestRunnerUnknownToolIsNonFatal — a hallucinated tool name must NOT
// abort the loop. The model needs a visible "unknown tool" result so it
// can course-correct.
func TestRunnerUnknownToolIsNonFatal(t *testing.T) {
	runner := &Runner{Registry: NewRegistry(), MaxIterations: 10}
	results, err := runner.Run(context.Background(), []ToolCall{
		{ID: "x", Name: "nope", Args: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("unknown tool must not abort: %v", err)
	}
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("unknown tool must yield one IsError result: %+v", results)
	}
}

// TestRunnerHandlerErrorSurfacesToModel — generic errors from the handler
// become tool_result errors the model sees, not abort signals. Only
// ErrAbort terminates the whole loop.
func TestRunnerHandlerErrorSurfacesToModel(t *testing.T) {
	reg := NewRegistry()
	reg.Register(New("boom", "always errors", `{}`,
		func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, errors.New("transient failure")
		}))
	runner := &Runner{Registry: reg, MaxIterations: 10}
	results, err := runner.Run(context.Background(), []ToolCall{{ID: "c", Name: "boom"}})
	if err != nil {
		t.Fatalf("generic handler error must not abort: %v", err)
	}
	if !results[0].IsError {
		t.Error("handler error should set IsError on the result")
	}
}

// TestRunnerErrAbortTerminatesLoop — ErrAbort stops the run immediately,
// returning any accumulated results plus the sentinel error.
func TestRunnerErrAbortTerminatesLoop(t *testing.T) {
	reg := NewRegistry()
	reg.Register(New("ok", "always succeeds", `{}`,
		func(ctx context.Context, args json.RawMessage) (any, error) { return "done", nil }))
	reg.Register(New("stop", "aborts", `{}`,
		func(ctx context.Context, args json.RawMessage) (any, error) { return nil, ErrAbort }))

	runner := &Runner{Registry: reg, MaxIterations: 10}
	results, err := runner.Run(context.Background(), []ToolCall{
		{ID: "1", Name: "ok"},
		{ID: "2", Name: "stop"},
		{ID: "3", Name: "ok"}, // should NOT run
	})
	if err != ErrAbort {
		t.Fatalf("want ErrAbort, got %v", err)
	}
	if len(results) != 1 {
		t.Errorf("only the pre-abort result should be collected, got %+v", results)
	}
}
