package api

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
)

type agentToolRouteAttemptOutcome struct {
	result   agentrouting.RouteResult
	replayed bool
	err      error
}

func TestAgentToolRouteAttemptStoreCoalescesConcurrentRetries(t *testing.T) {
	store := newAgentToolRouteAttemptStore(2)
	request := agentrouting.Request{
		Project:      "demo",
		ProviderID:   "codex",
		ConnectionID: "aws-prod",
		ServerID:     "aws",
		ToolName:     "list_buckets",
		Mode:         "read_only",
		Risk:         "read_only",
	}
	want := agentrouting.RouteResult{Decision: agentrouting.Decision{
		Status:          agentrouting.DecisionAllowed,
		Reason:          agentrouting.ReasonAllowed,
		Allowed:         true,
		UntrustedOutput: true,
	}}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	route := func() (agentrouting.RouteResult, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return want, nil
	}

	results := make(chan agentToolRouteAttemptOutcome, 2)
	authorize := func() {
		result, replayed, err := store.authorize(context.Background(), "run_000001", "same-attempt", request, route)
		results <- agentToolRouteAttemptOutcome{result: result, replayed: replayed, err: err}
	}
	go authorize()
	<-started
	go authorize()
	close(release)

	replayed := 0
	for range 2 {
		outcome := <-results
		if outcome.err != nil || outcome.result.Decision != want.Decision {
			t.Fatalf("authorize outcome = %+v, want recorded decision", outcome)
		}
		if outcome.replayed {
			replayed++
		}
	}
	if calls.Load() != 1 || replayed != 1 {
		t.Fatalf("route calls = %d, replayed responses = %d; want 1, 1", calls.Load(), replayed)
	}
}

func TestAgentToolRouteAttemptStoreReleasesFailedAttempts(t *testing.T) {
	store := newAgentToolRouteAttemptStore(1)
	request := agentrouting.Request{Project: "demo"}
	wantErr := errors.New("temporary route failure")

	if _, replayed, err := store.authorize(context.Background(), "run_000001", "retryable", request, func() (agentrouting.RouteResult, error) {
		return agentrouting.RouteResult{}, wantErr
	}); !errors.Is(err, wantErr) || replayed {
		t.Fatalf("first authorize error = %v, replayed = %t; want retryable failure", err, replayed)
	}

	want := agentrouting.RouteResult{Decision: agentrouting.Decision{
		Status:          agentrouting.DecisionDenied,
		Reason:          agentrouting.ReasonPolicyDenied,
		UntrustedOutput: true,
	}}
	got, replayed, err := store.authorize(context.Background(), "run_000001", "retryable", request, func() (agentrouting.RouteResult, error) {
		return want, nil
	})
	if err != nil || replayed || got.Decision != want.Decision {
		t.Fatalf("second authorize = %+v, replayed = %t, error = %v; want fresh success", got, replayed, err)
	}
}
