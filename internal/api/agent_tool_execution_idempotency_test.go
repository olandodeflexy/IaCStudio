package api

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

type agentToolExecutionAttemptOutcome struct {
	result   agentrouting.ExecutionResult
	replayed bool
	err      error
}

func TestAgentToolExecutionAttemptStoreCoalescesConcurrentRetries(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(2)
	request := testAgentToolExecutionRequest()
	arguments := testAgentToolExecutionArguments(t, `{"bucket":"reports"}`)
	want := testAgentToolExecutionResult("reports\n")
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	execute := func() (agentrouting.ExecutionResult, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return want, nil
	}

	results := make(chan agentToolExecutionAttemptOutcome, 2)
	run := func() {
		result, replayed, err := store.execute(
			context.Background(),
			"run_000001",
			"same-attempt",
			request,
			arguments,
			execute,
		)
		results <- agentToolExecutionAttemptOutcome{result: result, replayed: replayed, err: err}
	}
	go run()
	<-started
	go run()
	close(release)

	replayed := 0
	for range 2 {
		outcome := <-results
		if outcome.err != nil {
			t.Fatalf("execute error = %v", outcome.err)
		}
		if outcome.result.Result == nil || outcome.result.Result.Output != "reports" {
			t.Fatalf("execute result = %+v, want sanitized output", outcome.result)
		}
		if outcome.replayed {
			replayed++
			if outcome.result.Route.Run.ID != "" {
				t.Fatalf("replayed run = %+v, want caller-hydrated run", outcome.result.Route.Run)
			}
		}
	}
	if calls.Load() != 1 || replayed != 1 {
		t.Fatalf("execution calls = %d, replayed responses = %d; want 1, 1", calls.Load(), replayed)
	}
}

func TestAgentToolExecutionAttemptStoreRejectsChangedArguments(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(2)
	request := testAgentToolExecutionRequest()
	firstArguments := testAgentToolExecutionArguments(t, `{"bucket":"reports"}`)
	secondArguments := testAgentToolExecutionArguments(t, `{"bucket":"audit"}`)
	var calls atomic.Int32
	execute := func() (agentrouting.ExecutionResult, error) {
		calls.Add(1)
		return testAgentToolExecutionResult("ok"), nil
	}

	if _, replayed, err := store.execute(context.Background(), "run_000001", "same-key", request, firstArguments, execute); err != nil || replayed {
		t.Fatalf("first execute error = %v, replayed = %t; want fresh success", err, replayed)
	}
	if _, replayed, err := store.execute(context.Background(), "run_000001", "same-key", request, secondArguments, execute); !errors.Is(err, errAgentToolExecutionIdempotencyConflict) || replayed {
		t.Fatalf("changed arguments error = %v, replayed = %t; want idempotency conflict", err, replayed)
	}
	if calls.Load() != 1 {
		t.Fatalf("execution calls = %d, want 1", calls.Load())
	}
}

func TestAgentToolExecutionAttemptStoreRejectsChangedScope(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(2)
	request := testAgentToolExecutionRequest()
	arguments := testAgentToolExecutionArguments(t, `{}`)
	execute := func() (agentrouting.ExecutionResult, error) {
		return testAgentToolExecutionResult("ok"), nil
	}

	if _, _, err := store.execute(context.Background(), "run_000001", "same-key", request, arguments, execute); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	request.ConnectionID = "aws-stage"
	if _, replayed, err := store.execute(context.Background(), "run_000001", "same-key", request, arguments, execute); !errors.Is(err, errAgentToolExecutionIdempotencyConflict) || replayed {
		t.Fatalf("changed scope error = %v, replayed = %t; want idempotency conflict", err, replayed)
	}
}

func TestAgentToolExecutionAttemptStoreReleasesFailedAttempts(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(1)
	request := testAgentToolExecutionRequest()
	arguments := testAgentToolExecutionArguments(t, `{}`)
	wantErr := errors.New("temporary execution failure")

	failed, replayed, err := store.execute(context.Background(), "run_000001", "retryable", request, arguments, func() (agentrouting.ExecutionResult, error) {
		return testAgentToolExecutionResult("partial"), wantErr
	})
	if !errors.Is(err, wantErr) ||
		replayed ||
		failed.Invoked ||
		failed.Result != nil ||
		failed.Route.Decision != (agentrouting.Decision{}) ||
		failed.Route.Run.ID != "" {
		t.Fatalf("first execute = %+v, error = %v, replayed = %t; want zero retryable failure", failed, err, replayed)
	}

	want := testAgentToolExecutionResult("ok")
	got, replayed, err := store.execute(context.Background(), "run_000001", "retryable", request, arguments, func() (agentrouting.ExecutionResult, error) {
		return want, nil
	})
	if err != nil || replayed || got.Result == nil || got.Result.Output != "ok" {
		t.Fatalf("second execute = %+v, replayed = %t, error = %v; want fresh success", got, replayed, err)
	}
}

func TestAgentToolExecutionAttemptStoreReleasesPanickedAttempts(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(1)
	request := testAgentToolExecutionRequest()
	arguments := testAgentToolExecutionArguments(t, `{}`)
	started := make(chan struct{})
	release := make(chan struct{})
	recovered := make(chan any, 1)

	go func() {
		defer func() {
			recovered <- recover()
		}()
		_, _, _ = store.execute(context.Background(), "run_000001", "panicked", request, arguments, func() (agentrouting.ExecutionResult, error) {
			close(started)
			<-release
			panic("callback failure")
		})
	}()
	<-started

	key := agentToolExecutionAttemptKey{runID: "run_000001", key: "panicked"}
	store.mu.Lock()
	attempt := store.entries[key]
	store.mu.Unlock()
	if attempt == nil {
		t.Fatal("in-flight attempt was not registered")
	}

	close(release)
	if got := <-recovered; got != "callback failure" {
		t.Fatalf("recovered panic = %v, want original value", got)
	}
	select {
	case <-attempt.done:
		if !errors.Is(attempt.err, errAgentToolExecutionCallbackPanicked) {
			t.Fatalf("waiter error = %v, want stable panic error", attempt.err)
		}
	default:
		t.Fatal("panicked attempt did not release waiters")
	}

	store.mu.Lock()
	_, retained := store.entries[key]
	store.mu.Unlock()
	if retained {
		t.Fatal("panicked attempt remained in replay store")
	}

	got, replayed, err := store.execute(context.Background(), "run_000001", "panicked", request, arguments, func() (agentrouting.ExecutionResult, error) {
		return testAgentToolExecutionResult("retried"), nil
	})
	if err != nil || replayed || got.Result == nil || got.Result.Output != "retried" {
		t.Fatalf("retry result = %+v, replayed = %t, error = %v; want fresh valid execution", got, replayed, err)
	}
}

func TestAgentToolExecutionAttemptStoreRetriesWriteAfterApproval(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(1)
	request := testAgentToolExecutionRequest()
	request.Risk = mcpairlock.RiskCloudMutation
	request.Mode = "approved_execute"
	arguments := testAgentToolExecutionArguments(t, `{}`)
	var calls atomic.Int32

	approvalRequired := agentrouting.ExecutionResult{
		Route: agentrouting.RouteResult{
			Decision: agentrouting.Decision{
				Status:           agentrouting.DecisionApprovalRequired,
				Reason:           agentrouting.ReasonApprovalRequired,
				ApprovalRequired: true,
				UntrustedOutput:  true,
			},
		},
	}
	first, replayed, err := store.execute(context.Background(), "run_000001", "write", request, arguments, func() (agentrouting.ExecutionResult, error) {
		calls.Add(1)
		return approvalRequired, nil
	})
	if err != nil || replayed || first.Route.Decision.Status != agentrouting.DecisionApprovalRequired || first.Invoked || first.Result != nil {
		t.Fatalf("first write = %+v, replayed = %t, error = %v; want fresh approval requirement", first, replayed, err)
	}

	approved := testAgentToolExecutionResult("applied")
	second, replayed, err := store.execute(context.Background(), "run_000001", "write", request, arguments, func() (agentrouting.ExecutionResult, error) {
		calls.Add(1)
		return approved, nil
	})
	if err != nil || replayed || second.Result == nil || second.Result.Output != "applied" {
		t.Fatalf("approved write = %+v, replayed = %t, error = %v; want fresh execution", second, replayed, err)
	}

	third, replayed, err := store.execute(context.Background(), "run_000001", "write", request, arguments, func() (agentrouting.ExecutionResult, error) {
		calls.Add(1)
		return testAgentToolExecutionResult("duplicate"), nil
	})
	if err != nil || !replayed || third.Result == nil || third.Result.Output != "applied" {
		t.Fatalf("write replay = %+v, replayed = %t, error = %v; want cached approved result", third, replayed, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("execution calls = %d, want approval check plus one write", calls.Load())
	}
}

func TestAgentToolExecutionAttemptStoreRejectsInvalidIdempotencyKeys(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(1)
	request := testAgentToolExecutionRequest()
	arguments := testAgentToolExecutionArguments(t, `{}`)
	invalidKeys := []string{
		"",
		" retry",
		strings.Repeat("a", maxAgentToolRouteIdempotencyKeyLength+1),
		"r\u00e9try",
	}

	for _, key := range invalidKeys {
		t.Run(key, func(t *testing.T) {
			var called atomic.Bool
			_, replayed, err := store.execute(context.Background(), "run_000001", key, request, arguments, func() (agentrouting.ExecutionResult, error) {
				called.Store(true)
				return agentrouting.ExecutionResult{}, nil
			})
			if !errors.Is(err, errAgentToolExecutionInvalidIdempotencyKey) || replayed {
				t.Fatalf("invalid key error = %v, replayed = %t; want validation failure", err, replayed)
			}
			if called.Load() {
				t.Fatal("invalid idempotency key reached execution callback")
			}
		})
	}
}

func TestAgentToolExecutionAttemptStoreRejectsCanceledContext(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(1)
	request := testAgentToolExecutionRequest()
	arguments := testAgentToolExecutionArguments(t, `{}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called atomic.Bool

	_, replayed, err := store.execute(ctx, "run_000001", "canceled", request, arguments, func() (agentrouting.ExecutionResult, error) {
		called.Store(true)
		return agentrouting.ExecutionResult{}, nil
	})
	if !errors.Is(err, context.Canceled) || replayed {
		t.Fatalf("canceled execution error = %v, replayed = %t; want context cancellation", err, replayed)
	}
	if called.Load() {
		t.Fatal("canceled execution callback was called")
	}
}

func TestAgentToolExecutionAttemptStoreReleasesInvalidResults(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(1)
	request := testAgentToolExecutionRequest()
	arguments := testAgentToolExecutionArguments(t, `{}`)

	if result, replayed, err := store.execute(context.Background(), "run_000001", "invalid", request, arguments, func() (agentrouting.ExecutionResult, error) {
		return agentrouting.ExecutionResult{
			Route: agentrouting.RouteResult{
				Decision: agentrouting.Decision{
					Status:          agentrouting.DecisionAllowed,
					Reason:          agentrouting.ReasonAllowed,
					Allowed:         true,
					UntrustedOutput: true,
				},
			},
		}, nil
	}); !errors.Is(err, agentrouting.ErrInvalidToolExecution) ||
		replayed ||
		result.Invoked ||
		result.Result != nil ||
		result.Route.Decision != (agentrouting.Decision{}) ||
		result.Route.Run.ID != "" {
		t.Fatalf("invalid result = %+v, replayed = %t, error = %v; want zero invalid execution", result, replayed, err)
	}

	got, replayed, err := store.execute(context.Background(), "run_000001", "invalid", request, arguments, func() (agentrouting.ExecutionResult, error) {
		return testAgentToolExecutionResult("retried"), nil
	})
	if err != nil || replayed || got.Result == nil || got.Result.Output != "retried" {
		t.Fatalf("retry result = %+v, replayed = %t, error = %v; want fresh valid execution", got, replayed, err)
	}
}

func TestAgentToolExecutionAttemptStoreCapsReplayEntries(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(maxAgentToolExecutionReplayEntries + 1)
	if store.maxEntries != maxAgentToolExecutionReplayEntries {
		t.Fatalf("max entries = %d, want hard cap %d", store.maxEntries, maxAgentToolExecutionReplayEntries)
	}
}

func TestAgentToolExecutionAttemptStoreReplaysIndependentResult(t *testing.T) {
	store := newAgentToolExecutionAttemptStore(1)
	request := testAgentToolExecutionRequest()
	arguments := testAgentToolExecutionArguments(t, `{}`)
	execute := func() (agentrouting.ExecutionResult, error) {
		return testAgentToolExecutionResult("original"), nil
	}

	if _, _, err := store.execute(context.Background(), "run_000001", "replay", request, arguments, execute); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	first, replayed, err := store.execute(context.Background(), "run_000001", "replay", request, arguments, execute)
	if err != nil || !replayed || first.Result == nil {
		t.Fatalf("first replay = %+v, replayed = %t, error = %v", first, replayed, err)
	}
	first.Result.Output = "mutated"

	second, replayed, err := store.execute(context.Background(), "run_000001", "replay", request, arguments, execute)
	if err != nil || !replayed || second.Result == nil || second.Result.Output != "original" {
		t.Fatalf("second replay = %+v, replayed = %t, error = %v; want independent result", second, replayed, err)
	}
}

func testAgentToolExecutionRequest() agentrouting.Request {
	return agentrouting.Request{
		Project:      "demo",
		ProviderID:   "codex",
		ConnectionID: "aws-prod",
		ServerID:     "aws",
		ToolName:     "list_buckets",
		Mode:         "read_only",
		Risk:         mcpairlock.RiskReadOnly,
	}
}

func testAgentToolExecutionArguments(t *testing.T, raw string) mcpairlock.ToolCallArguments {
	t.Helper()
	arguments, err := mcpairlock.ParseToolCallArguments([]byte(raw))
	if err != nil {
		t.Fatalf("parse tool arguments: %v", err)
	}
	return arguments
}

func testAgentToolExecutionResult(output string) agentrouting.ExecutionResult {
	result := mcpairlock.NewToolCallResult([]byte(output), false)
	return agentrouting.ExecutionResult{
		Route: agentrouting.RouteResult{
			Decision: agentrouting.Decision{
				Status:          agentrouting.DecisionAllowed,
				Reason:          agentrouting.ReasonAllowed,
				Allowed:         true,
				UntrustedOutput: true,
			},
		},
		Invoked: true,
		Result:  &result,
	}
}
