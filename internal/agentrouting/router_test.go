package agentrouting

import (
	"errors"
	"reflect"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

func TestRouterPreviewsWithoutRecording(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, evaluator, store, run := routerFixture(t, policy, request, airlock)
	before, ok := store.Get(run.ID)
	if !ok {
		t.Fatalf("Get(%q) returned no run", run.ID)
	}

	decision, err := router.Preview(request)
	if err != nil {
		t.Fatalf("Preview(): %v", err)
	}
	if decision.Status != DecisionAllowed || !decision.Allowed {
		t.Fatalf("Preview() = %+v, want allowed decision", decision)
	}
	after, ok := store.Get(run.ID)
	if !ok || !reflect.DeepEqual(after, before) {
		t.Fatalf("run mutated by Preview(): before=%+v after=%+v", before, after)
	}
	if evaluator.calls != 1 {
		t.Fatalf("EvaluateTool calls = %d, want one read-only evaluation", evaluator.calls)
	}
}

func TestRouterPreviewRejectsInvalidRequestBeforeAuthorization(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, evaluator, store, run := routerFixture(t, policy, request, airlock)
	before, ok := store.Get(run.ID)
	if !ok {
		t.Fatalf("Get(%q) returned no run", run.ID)
	}
	request.ToolName = ""

	decision, err := router.Preview(request)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Preview() error = %v, want ErrInvalidRequest", err)
	}
	if decision != (Decision{}) {
		t.Fatalf("Preview() decision = %+v, want zero decision", decision)
	}
	after, ok := store.Get(run.ID)
	if !ok || !reflect.DeepEqual(after, before) {
		t.Fatalf("run mutated by invalid preview: before=%+v after=%+v", before, after)
	}
	if evaluator.calls != 0 {
		t.Fatalf("EvaluateTool calls = %d, want none for an invalid request", evaluator.calls)
	}
}

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

func TestRouterRecordsAllowedExactToolCall(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, evaluator, _, run := routerFixture(t, policy, request, airlock)
	arguments := mustToolArguments(t, `{"region":"us-east-1"}`)

	result, err := router.RouteToolCall(testToolApprovalKey, run.ID, request, arguments)
	if err != nil {
		t.Fatalf("RouteToolCall(): %v", err)
	}
	if result.Decision.Status != DecisionAllowed || result.Run.Status != agentruns.StatusQueued || len(result.Run.Approvals) != 0 {
		t.Fatalf("RouteToolCall() = %+v, want allowed queued run without approvals", result)
	}
	if evaluator.calls != 1 {
		t.Fatalf("EvaluateTool calls = %d, want one", evaluator.calls)
	}
}

func TestRouterRecordsExactToolCallApproval(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	policy.Rules[0].ApprovalRequired = true
	router, evaluator, store, run := routerFixture(t, policy, request, airlock)
	arguments := mustToolArguments(t, `{"region":"us-east-1","refresh":false}`)

	result, err := router.RouteToolCall(testToolApprovalKey, run.ID, request, arguments)
	if err != nil {
		t.Fatalf("RouteToolCall(): %v", err)
	}
	if result.Decision.Status != DecisionApprovalRequired || result.Run.Status != agentruns.StatusWaitingApproval {
		t.Fatalf("RouteToolCall() = %+v, want approval-required waiting run", result)
	}
	if len(result.Run.Approvals) != 1 {
		t.Fatalf("RouteToolCall() approvals = %d, want one", len(result.Run.Approvals))
	}
	binding := ToolApprovalBinding(result.Run.Approvals[0].OperationBinding.Value())
	if !binding.Matches(testToolApprovalKey, run.ID, request, arguments) {
		t.Fatal("recorded gate is not bound to the exact tool call")
	}
	stored, ok := store.Get(run.ID)
	if !ok || len(stored.Approvals) != 1 || stored.Approvals[0].OperationBinding.Value() != string(binding) {
		t.Fatalf("stored run did not retain exact operation binding: %+v", stored)
	}
	if evaluator.calls != 1 {
		t.Fatalf("EvaluateTool calls = %d, want one", evaluator.calls)
	}
}

func TestRouterRejectsInvalidBoundToolCallBeforeAuthorization(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	validArguments := mustToolArguments(t, `{}`)
	tests := []struct {
		name      string
		key       []byte
		arguments mcpairlock.ToolCallArguments
		mutate    func(*Request)
		mutateID  func(string) string
		want      error
	}{
		{name: "short key", key: []byte("short"), arguments: validArguments, want: ErrInvalidToolApprovalBinding},
		{name: "invalid arguments", key: testToolApprovalKey, want: ErrInvalidToolApprovalBinding},
		{name: "invalid route", key: testToolApprovalKey, arguments: validArguments, mutate: func(request *Request) {
			request.ToolName = ""
		}, want: ErrInvalidRequest},
		{name: "empty run id", key: testToolApprovalKey, arguments: validArguments, mutateID: func(string) string {
			return ""
		}, want: ErrRunIDRequired},
		{name: "padded run id", key: testToolApprovalKey, arguments: validArguments, mutateID: func(runID string) string {
			return " " + runID
		}, want: ErrInvalidRunID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, evaluator, store, run := routerFixture(t, policy, request, airlock)
			before, ok := store.Get(run.ID)
			if !ok {
				t.Fatalf("Get(%q) returned no run", run.ID)
			}
			candidate := request
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			candidateRunID := run.ID
			if test.mutateID != nil {
				candidateRunID = test.mutateID(run.ID)
			}

			result, err := router.RouteToolCall(test.key, candidateRunID, candidate, test.arguments)
			if !errors.Is(err, test.want) {
				t.Fatalf("RouteToolCall() error = %v, want %v", err, test.want)
			}
			if result.Decision != (Decision{}) || result.Run.ID != "" {
				t.Fatalf("RouteToolCall() = %+v, want zero result", result)
			}
			after, ok := store.Get(run.ID)
			if !ok || !reflect.DeepEqual(after, before) {
				t.Fatalf("run mutated after invalid tool call: before=%+v after=%+v", before, after)
			}
			if evaluator.calls != 0 {
				t.Fatalf("EvaluateTool calls = %d, want none", evaluator.calls)
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

func TestRouterBlocksAllowedOnWaitingApprovalRun(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, _, store, run := routerFixture(t, policy, request, airlock)

	// Pre-transition the run to StatusWaitingApproval by adding an approval gate directly.
	if _, err := store.AddApproval(run.ID, agentruns.ApprovalGate{
		Kind:    agentruns.ApprovalMCPNetwork,
		Summary: "pending approval",
	}); err != nil {
		t.Fatalf("AddApproval(): %v", err)
	}

	// The airlock would allow this call, but the recorder must block it.
	result, err := router.Route(run.ID, request)
	if !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("Route() error = %v, want ErrInvalidDecision", err)
	}
	if result.Decision != (Decision{}) || result.Run.ID != "" {
		t.Fatalf("Route() result = %+v, want zero result after recorder rejection", result)
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
	if _, err := nilRouter.Preview(request); !errors.Is(err, ErrAuthorizerRequired) {
		t.Fatalf("nil Preview() error = %v, want ErrAuthorizerRequired", err)
	}
	if _, err := nilRouter.Route("run_000001", request); !errors.Is(err, ErrAuthorizerRequired) {
		t.Fatalf("nil Route() error = %v, want ErrAuthorizerRequired", err)
	}
}
