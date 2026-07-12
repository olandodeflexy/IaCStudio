package agentrouting

import (
	"errors"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

func routerFixture(t *testing.T, policy Policy, request Request, airlock mcpairlock.ToolDecision) (*Router, *fakeToolEvaluator, *agentruns.Store, agentruns.Run) {
	t.Helper()
	evaluator := &fakeToolEvaluator{entry: evaluationEntry(request, airlock)}
	authorizer, err := NewAuthorizer(policy, evaluator)
	if err != nil {
		t.Fatalf("NewAuthorizer(): %v", err)
	}
	recorder, store, run := recorderFixture(t, request)
	router, err := NewRouter(authorizer, recorder)
	if err != nil {
		t.Fatalf("NewRouter(): %v", err)
	}
	return router, evaluator, store, run
}

func TestRouterAuthorizesAndRecordsOutcomes(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(*Policy, *mcpairlock.ToolDecision)
		wantDecision  DecisionStatus
		wantRun       agentruns.Status
		wantAirlock   int
		wantApprovals int
	}{
		{
			name:         "allowed",
			wantDecision: DecisionAllowed,
			wantRun:      agentruns.StatusQueued,
			wantAirlock:  1,
		},
		{
			name: "approval required",
			mutate: func(_ *Policy, airlock *mcpairlock.ToolDecision) {
				airlock.Status = "approval_required"
				airlock.Allowed = false
				airlock.ApprovalRequired = true
			},
			wantDecision:  DecisionApprovalRequired,
			wantRun:       agentruns.StatusWaitingApproval,
			wantAirlock:   1,
			wantApprovals: 1,
		},
		{
			name: "policy denied",
			mutate: func(policy *Policy, _ *mcpairlock.ToolDecision) {
				policy.Rules[0].Effect = EffectDeny
			},
			wantDecision: DecisionDenied,
			wantRun:      agentruns.StatusFailed,
			wantAirlock:  0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, airlock := readOnlyEvaluation()
			if test.mutate != nil {
				test.mutate(&policy, &airlock)
			}
			router, evaluator, _, run := routerFixture(t, policy, request, airlock)

			result, err := router.Route(run.ID, request)
			if err != nil {
				t.Fatalf("Route(): %v", err)
			}
			if result.Decision.Status != test.wantDecision || result.Run.Status != test.wantRun {
				t.Fatalf("Route() = %+v, want %q decision and %q run", result, test.wantDecision, test.wantRun)
			}
			if evaluator.calls != test.wantAirlock || len(result.Run.Approvals) != test.wantApprovals {
				t.Fatalf("Route() Airlock calls = %d, approvals = %d; want %d, %d", evaluator.calls, len(result.Run.Approvals), test.wantAirlock, test.wantApprovals)
			}
		})
	}
}

func TestRouterDoesNotExposeUnrecordedDecision(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, evaluator, store, run := routerFixture(t, policy, request, airlock)
	if _, err := store.Fail(run.ID, "already terminal"); err != nil {
		t.Fatalf("Fail(): %v", err)
	}

	result, err := router.Route(run.ID, request)
	if !errors.Is(err, agentruns.ErrTerminated) {
		t.Fatalf("Route() error = %v, want ErrTerminated", err)
	}
	if result.Decision != (Decision{}) || result.Run.ID != "" {
		t.Fatalf("Route() result = %+v, want zero result after recorder failure", result)
	}
	if evaluator.calls != 1 {
		t.Fatalf("EvaluateTool calls = %d, want one authorization attempt", evaluator.calls)
	}
	terminal, ok := store.Get(run.ID)
	if !ok || terminal.Status != agentruns.StatusFailed || len(terminal.Logs) != 1 || len(terminal.Approvals) != 0 {
		t.Fatalf("terminal run mutated after Route(): %+v", terminal)
	}
}

func TestRouterRejectsMissingDependencies(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	evaluator := &fakeToolEvaluator{entry: evaluationEntry(request, airlock)}
	authorizer, err := NewAuthorizer(policy, evaluator)
	if err != nil {
		t.Fatalf("NewAuthorizer(): %v", err)
	}
	recorder, _, _ := recorderFixture(t, request)

	if _, err := NewRouter(nil, recorder); !errors.Is(err, ErrAuthorizerRequired) {
		t.Fatalf("NewRouter(nil authorizer) error = %v, want ErrAuthorizerRequired", err)
	}
	if _, err := NewRouter(authorizer, nil); !errors.Is(err, ErrRunRecorderRequired) {
		t.Fatalf("NewRouter(nil recorder) error = %v, want ErrRunRecorderRequired", err)
	}
	var nilRouter *Router
	if _, err := nilRouter.Route("run_000001", request); !errors.Is(err, ErrAuthorizerRequired) {
		t.Fatalf("nil Route() error = %v, want ErrAuthorizerRequired", err)
	}
}
