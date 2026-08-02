package agentrouting

import (
	"context"
	"errors"
	"fmt"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

var (
	ErrToolRouteExecutorRequired = errors.New("tool route executor is required")
	ErrToolInvokerRequired       = errors.New("tool invoker is required")
	ErrToolExecutionContext      = errors.New("tool execution context is required")
	ErrInvalidToolExecution      = errors.New("invalid authorized tool execution")
	ErrToolInvocationFailed      = errors.New("MCP tool invocation failed")
)

// ToolInvokeFunc runs one transport request that the Executor has already
// authorized and recorded on an Agent Run.
type ToolInvokeFunc func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error)

// ExecutionResult includes a route decision for every recorded authorization
// outcome. Result is populated only when the external tool was invoked.
type ExecutionResult struct {
	Route   RouteResult                `json:"route"`
	Invoked bool                       `json:"invoked"`
	Result  *mcpairlock.ToolCallResult `json:"result,omitempty"`
}

// Executor records authorization through Router before invoking one external
// MCP tool. It does not select credentials or expose an HTTP endpoint.
type Executor struct {
	router *Router
	invoke ToolInvokeFunc
}

func NewExecutor(router *Router, invoke ToolInvokeFunc) (*Executor, error) {
	if router == nil {
		return nil, ErrToolRouteExecutorRequired
	}
	if invoke == nil {
		return nil, ErrToolInvokerRequired
	}
	return &Executor{router: router, invoke: invoke}, nil
}

// Execute validates and binds the transport request, records the scoped route
// decision, and invokes exactly once only when that decision is allowed.
func (e *Executor) Execute(
	ctx context.Context,
	runID string,
	request Request,
	arguments mcpairlock.ToolCallArguments,
) (ExecutionResult, error) {
	if e == nil || e.router == nil {
		return ExecutionResult{}, ErrToolRouteExecutorRequired
	}
	if e.invoke == nil {
		return ExecutionResult{}, ErrToolInvokerRequired
	}
	if ctx == nil {
		return ExecutionResult{}, ErrToolExecutionContext
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}
	if err := request.Validate(); err != nil {
		return ExecutionResult{}, err
	}

	toolRequest, err := mcpairlock.NewToolCallRequest(request.ServerID, request.ToolName, arguments)
	if err != nil {
		return ExecutionResult{}, err
	}
	route, err := e.router.Route(runID, request)
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("record tool execution route: %w", err)
	}
	if err := validateExecutionRoute(route, runID, request); err != nil {
		return ExecutionResult{}, err
	}

	execution := ExecutionResult{Route: route}
	if route.Decision.Status != DecisionAllowed {
		return execution, nil
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}

	result, err := e.invoke(ctx, toolRequest)
	if contextErr := ctx.Err(); contextErr != nil {
		return ExecutionResult{}, contextErr
	}
	if err != nil {
		return ExecutionResult{}, ErrToolInvocationFailed
	}
	if err := result.Validate(); err != nil {
		return ExecutionResult{}, ErrToolInvocationFailed
	}
	execution.Invoked = true
	execution.Result = &result
	return execution, nil
}

func validateExecutionRoute(route RouteResult, runID string, request Request) error {
	if err := route.Decision.Validate(); err != nil {
		return ErrInvalidToolExecution
	}
	if route.Run.ID != runID ||
		route.Run.Project != request.Project ||
		route.Run.ProviderID != request.ProviderID ||
		route.Run.Mode != request.Mode {
		return ErrInvalidToolExecution
	}
	if route.Decision.Status != DecisionAllowed {
		return nil
	}
	if route.Run.Canceled || (route.Run.Status != agentruns.StatusQueued && route.Run.Status != agentruns.StatusRunning) {
		return ErrInvalidToolExecution
	}
	for _, approval := range route.Run.Approvals {
		if approval.Status == agentruns.ApprovalPending {
			return ErrInvalidToolExecution
		}
	}
	return nil
}
