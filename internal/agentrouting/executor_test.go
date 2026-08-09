package agentrouting

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

func TestExecutorRecordsAllowedRouteBeforeInvocation(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, _, store, run := routerFixture(t, policy, request, airlock)
	arguments := executionArguments(t)
	invocations := 0
	executor, err := NewExecutor(router, func(_ context.Context, call mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
		invocations++
		if call.ServerID != request.ServerID || call.ToolName != request.ToolName || string(call.Arguments.Bytes()) != string(arguments.Bytes()) {
			t.Fatalf("tool request = %+v, want route-bound request", call)
		}
		current, ok := store.Get(run.ID)
		if !ok || len(current.Logs) != 1 || current.Logs[0].Level != agentruns.LogAudit || current.Logs[0].Message != routeAuditMessage("Allowed", request) {
			t.Fatalf("route was not audited before invocation: %+v", current)
		}
		return mcpairlock.NewToolCallResult([]byte("inventory ready"), false), nil
	})
	if err != nil {
		t.Fatalf("NewExecutor(): %v", err)
	}

	result, err := executor.Execute(context.Background(), run.ID, request, arguments)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if invocations != 1 || !result.Invoked || result.Result == nil || result.Result.Output != "inventory ready" {
		t.Fatalf("Execute() = %+v, invocations = %d", result, invocations)
	}
}

func TestExecutorDoesNotInvokeNonAllowedRoutes(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*Policy, *mcpairlock.ToolDecision)
		wantStatus DecisionStatus
	}{
		{
			name: "denied",
			mutate: func(policy *Policy, _ *mcpairlock.ToolDecision) {
				policy.Rules[0].Effect = EffectDeny
			},
			wantStatus: DecisionDenied,
		},
		{
			name: "approval required",
			mutate: func(_ *Policy, airlock *mcpairlock.ToolDecision) {
				airlock.Status = "approval_required"
				airlock.Allowed = false
				airlock.ApprovalRequired = true
			},
			wantStatus: DecisionApprovalRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, airlock := readOnlyEvaluation()
			test.mutate(&policy, &airlock)
			router, _, _, run := routerFixture(t, policy, request, airlock)
			invocations := 0
			executor, err := NewExecutor(router, func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
				invocations++
				return mcpairlock.NewToolCallResult(nil, false), nil
			})
			if err != nil {
				t.Fatalf("NewExecutor(): %v", err)
			}

			result, err := executor.Execute(context.Background(), run.ID, request, executionArguments(t))
			if err != nil {
				t.Fatalf("Execute(): %v", err)
			}
			if invocations != 0 || result.Invoked || result.Result != nil || result.Route.Decision.Status != test.wantStatus {
				t.Fatalf("Execute() = %+v, invocations = %d", result, invocations)
			}
		})
	}
}

func TestExecutorBindsApprovalToExactToolCall(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	policy.Rules[0].ApprovalRequired = true
	router, _, store, run := routerFixture(t, policy, request, airlock)
	arguments := executionArguments(t)
	invocations := 0
	executor, err := NewExecutor(router, func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
		invocations++
		return mcpairlock.NewToolCallResult(nil, false), nil
	})
	if err != nil {
		t.Fatalf("NewExecutor(): %v", err)
	}

	result, err := executor.Execute(context.Background(), run.ID, request, arguments)
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if invocations != 0 || result.Invoked || result.Route.Decision.Status != DecisionApprovalRequired {
		t.Fatalf("Execute() = %+v, invocations = %d; want a non-invoking approval route", result, invocations)
	}
	if len(result.Route.Run.Approvals) != 1 {
		t.Fatalf("Execute() approvals = %d, want one", len(result.Route.Run.Approvals))
	}
	binding := ToolApprovalBinding(result.Route.Run.Approvals[0].OperationBinding.Value())
	if !binding.Matches(executor.approvalKey, run.ID, request, arguments) {
		t.Fatal("approval gate is not bound to the exact executed tool call")
	}
	if _, err := store.DecideApproval(run.ID, result.Route.Run.Approvals[0].ID, agentruns.ApprovalApproved, "operator"); err != nil {
		t.Fatalf("DecideApproval(): %v", err)
	}

	retry, err := executor.Execute(context.Background(), run.ID, request, arguments)
	if err != nil {
		t.Fatalf("Execute(retry): %v", err)
	}
	if invocations != 1 || !retry.Invoked || retry.Result == nil || retry.Route.Decision.Status != DecisionAllowed {
		t.Fatalf("Execute(retry) = %+v, invocations = %d; want approved exact-call execution", retry, invocations)
	}

	replay, err := executor.Execute(context.Background(), run.ID, request, arguments)
	if err != nil {
		t.Fatalf("Execute(replay): %v", err)
	}
	if invocations != 1 || replay.Invoked || replay.Route.Decision.Status != DecisionApprovalRequired {
		t.Fatalf("Execute(replay) = %+v, invocations = %d; want a fresh approval gate", replay, invocations)
	}
	if len(replay.Route.Run.Approvals) != 2 {
		t.Fatalf("Execute(replay) approvals = %d, want two", len(replay.Route.Run.Approvals))
	}
}

func TestExecutorDoesNotReuseApprovedBindingForDifferentArguments(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	policy.Rules[0].ApprovalRequired = true
	router, _, store, run := routerFixture(t, policy, request, airlock)
	firstArguments := executionArguments(t)
	secondArguments := mustExecutionArguments(t, `{"region":"us-west-2"}`)
	invocations := 0
	executor, err := NewExecutor(router, func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
		invocations++
		return mcpairlock.NewToolCallResult(nil, false), nil
	})
	if err != nil {
		t.Fatalf("NewExecutor(): %v", err)
	}

	first, err := executor.Execute(context.Background(), run.ID, request, firstArguments)
	if err != nil {
		t.Fatalf("Execute(first): %v", err)
	}
	if len(first.Route.Run.Approvals) != 1 {
		t.Fatalf("Execute(first) approvals = %d, want one", len(first.Route.Run.Approvals))
	}
	if _, err := store.DecideApproval(run.ID, first.Route.Run.Approvals[0].ID, agentruns.ApprovalApproved, "operator"); err != nil {
		t.Fatalf("DecideApproval(): %v", err)
	}

	second, err := executor.Execute(context.Background(), run.ID, request, secondArguments)
	if err != nil {
		t.Fatalf("Execute(second): %v", err)
	}
	if invocations != 0 || second.Invoked || second.Route.Decision.Status != DecisionApprovalRequired {
		t.Fatalf("Execute(second) = %+v, invocations = %d; want a new approval gate", second, invocations)
	}
	if len(second.Route.Run.Approvals) != 2 {
		t.Fatalf("Execute(second) approvals = %d, want two", len(second.Route.Run.Approvals))
	}
	binding := ToolApprovalBinding(second.Route.Run.Approvals[1].OperationBinding.Value())
	if !binding.Matches(executor.approvalKey, run.ID, request, secondArguments) {
		t.Fatal("new approval gate is not bound to the retried tool call arguments")
	}
}

func TestExecutorRechecksRunImmediatelyBeforeInvocation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *agentruns.Store, string)
	}{
		{
			name: "canceled",
			mutate: func(t *testing.T, store *agentruns.Store, runID string) {
				t.Helper()
				if _, err := store.Cancel(runID); err != nil {
					t.Fatalf("Cancel(): %v", err)
				}
			},
		},
		{
			name: "pending approval",
			mutate: func(t *testing.T, store *agentruns.Store, runID string) {
				t.Helper()
				if _, err := store.AddApproval(runID, agentruns.ApprovalGate{
					Kind:    agentruns.ApprovalMCPNetwork,
					Summary: "authorize invocation",
				}); err != nil {
					t.Fatalf("AddApproval(): %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, airlock := readOnlyEvaluation()
			router, _, store, run := routerFixture(t, policy, request, airlock)
			invocations := 0
			executor, err := NewExecutor(router, func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
				invocations++
				return mcpairlock.NewToolCallResult(nil, false), nil
			})
			if err != nil {
				t.Fatalf("NewExecutor(): %v", err)
			}
			lookups := 0
			executor.lookupRun = func(runID string) (agentruns.Run, bool) {
				lookups++
				test.mutate(t, store, runID)
				return store.Get(runID)
			}

			result, err := executor.Execute(context.Background(), run.ID, request, executionArguments(t))
			if !errors.Is(err, ErrInvalidToolExecution) {
				t.Fatalf("Execute() error = %v, want ErrInvalidToolExecution", err)
			}
			if !reflect.DeepEqual(result, ExecutionResult{}) || invocations != 0 || lookups != 1 {
				t.Fatalf("Execute() = %+v, invocations = %d, lookups = %d; want fail closed", result, invocations, lookups)
			}
		})
	}
}

func TestExecutorRejectsInvalidInputBeforeRouting(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Request, *mcpairlock.ToolCallArguments)
		wantError error
	}{
		{
			name: "invalid route",
			mutate: func(request *Request, _ *mcpairlock.ToolCallArguments) {
				request.ConnectionID = ""
			},
			wantError: ErrInvalidRequest,
		},
		{
			name: "invalid arguments",
			mutate: func(_ *Request, arguments *mcpairlock.ToolCallArguments) {
				*arguments = mcpairlock.ToolCallArguments{}
			},
			wantError: mcpairlock.ErrInvalidToolCallRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, airlock := readOnlyEvaluation()
			router, evaluator, store, run := routerFixture(t, policy, request, airlock)
			arguments := executionArguments(t)
			test.mutate(&request, &arguments)
			invocations := 0
			executor, err := NewExecutor(router, func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
				invocations++
				return mcpairlock.NewToolCallResult(nil, false), nil
			})
			if err != nil {
				t.Fatalf("NewExecutor(): %v", err)
			}

			result, err := executor.Execute(context.Background(), run.ID, request, arguments)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Execute() error = %v, want %v", err, test.wantError)
			}
			current, ok := store.Get(run.ID)
			if !reflect.DeepEqual(result, ExecutionResult{}) || invocations != 0 || evaluator.calls != 0 || !ok || len(current.Logs) != 0 || len(current.Approvals) != 0 {
				t.Fatalf("invalid input had side effects: result=%+v invocations=%d evaluator=%d run=%+v", result, invocations, evaluator.calls, current)
			}
		})
	}
}

func TestExecutorFailsClosedWhenRouteCannotBeRecorded(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, _, store, run := routerFixture(t, policy, request, airlock)
	if _, err := store.Fail(run.ID, "already terminal"); err != nil {
		t.Fatalf("Fail(): %v", err)
	}
	invocations := 0
	executor, err := NewExecutor(router, func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
		invocations++
		return mcpairlock.NewToolCallResult(nil, false), nil
	})
	if err != nil {
		t.Fatalf("NewExecutor(): %v", err)
	}

	result, err := executor.Execute(context.Background(), run.ID, request, executionArguments(t))
	if !errors.Is(err, agentruns.ErrTerminated) {
		t.Fatalf("Execute() error = %v, want ErrTerminated", err)
	}
	if !reflect.DeepEqual(result, ExecutionResult{}) || invocations != 0 {
		t.Fatalf("Execute() = %+v, invocations = %d; want fail closed", result, invocations)
	}
}

func TestExecutorSanitizesInvocationFailures(t *testing.T) {
	tests := []struct {
		name   string
		invoke ToolInvokeFunc
	}{
		{
			name: "transport error",
			invoke: func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
				return mcpairlock.ToolCallResult{}, errors.New("token=executor-secret")
			},
		},
		{
			name: "invalid result",
			invoke: func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
				return mcpairlock.ToolCallResult{Output: "token=executor-secret"}, nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, request, airlock := readOnlyEvaluation()
			router, _, _, run := routerFixture(t, policy, request, airlock)
			executor, err := NewExecutor(router, test.invoke)
			if err != nil {
				t.Fatalf("NewExecutor(): %v", err)
			}

			result, err := executor.Execute(context.Background(), run.ID, request, executionArguments(t))
			if err == nil || !errors.Is(err, ErrToolInvocationFailed) || strings.Contains(err.Error(), "executor-secret") {
				t.Fatalf("Execute() error = %v, want sanitized invocation failure", err)
			}
			if !reflect.DeepEqual(result, ExecutionResult{}) {
				t.Fatalf("Execute() = %+v, want zero result", result)
			}
		})
	}
}

func TestExecutorDiscardsResultWhenContextIsCanceledDuringInvocation(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, _, _, run := routerFixture(t, policy, request, airlock)
	ctx, cancel := context.WithCancel(context.Background())
	executor, err := NewExecutor(router, func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
		cancel()
		return mcpairlock.NewToolCallResult([]byte("stale result"), false), nil
	})
	if err != nil {
		t.Fatalf("NewExecutor(): %v", err)
	}

	result, err := executor.Execute(ctx, run.ID, request, executionArguments(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(result, ExecutionResult{}) {
		t.Fatalf("Execute() = %+v, want zero result", result)
	}
}

func TestExecutorRejectsMissingDependenciesAndContext(t *testing.T) {
	policy, request, airlock := readOnlyEvaluation()
	router, _, _, run := routerFixture(t, policy, request, airlock)
	invoke := func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error) {
		return mcpairlock.NewToolCallResult(nil, false), nil
	}

	if _, err := NewExecutor(nil, invoke); !errors.Is(err, ErrToolRouteRouterRequired) {
		t.Fatalf("NewExecutor(nil router) error = %v", err)
	}
	if _, err := NewExecutor(router, nil); !errors.Is(err, ErrToolInvokerRequired) {
		t.Fatalf("NewExecutor(nil invoker) error = %v", err)
	}
	executor, err := NewExecutor(router, invoke)
	if err != nil {
		t.Fatalf("NewExecutor(): %v", err)
	}
	if _, err := executor.Execute(nilContext(), run.ID, request, executionArguments(t)); !errors.Is(err, ErrToolExecutionContext) {
		t.Fatalf("Execute(nil context) error = %v", err)
	}
	var nilExecutor *Executor
	if _, err := nilExecutor.Execute(context.Background(), run.ID, request, executionArguments(t)); !errors.Is(err, ErrToolRouteRouterRequired) {
		t.Fatalf("nil Execute() error = %v", err)
	}
}

func executionArguments(t *testing.T) mcpairlock.ToolCallArguments {
	return mustExecutionArguments(t, `{"region":"us-east-1"}`)
}

func mustExecutionArguments(t *testing.T, raw string) mcpairlock.ToolCallArguments {
	t.Helper()
	arguments, err := mcpairlock.ParseToolCallArguments([]byte(raw))
	if err != nil {
		t.Fatalf("ParseToolCallArguments(): %v", err)
	}
	return arguments
}

// nilContext returns a nil context.Context to exercise nil-context rejection
// guards. Defined as a function so SA1012 (do not pass a nil Context) does not
// flag the call site, which only matches literal nil arguments.
func nilContext() context.Context { return nil }
