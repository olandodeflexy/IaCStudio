package agentrouting

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

var (
	ErrToolRouteRouterRequired = errors.New("tool route router is required")
	ErrToolInvokerRequired     = errors.New("tool invoker is required")
	ErrToolExecutionContext    = errors.New("tool execution context is required")
	ErrInvalidToolExecution    = errors.New("invalid authorized tool execution")
	ErrToolInvocationFailed    = errors.New("mcp tool invocation failed")
	ErrToolApprovalKey         = errors.New("generate tool approval binding key")
)

// ToolInvokeFunc runs one transport request that the Executor has already
// authorized and recorded on an Agent Run.
type ToolInvokeFunc func(context.Context, mcpairlock.ToolCallRequest) (mcpairlock.ToolCallResult, error)

// ExecutionResult contains the recorded route and optional tool result for a
// successful Execute call. Execute returns a zero result on every error, even
// when routing was recorded before a later invocation failure.
type ExecutionResult struct {
	Route   RouteResult                `json:"route"`
	Invoked bool                       `json:"invoked"`
	Result  *mcpairlock.ToolCallResult `json:"result,omitempty"`
}

// Executor records authorization through Router before invoking one external
// MCP tool. It does not select credentials or expose an HTTP endpoint.
type Executor struct {
	router      *Router
	invoke      ToolInvokeFunc
	lookupRun   func(string) (agentruns.Run, bool)
	approvalKey []byte
}

func NewExecutor(router *Router, invoke ToolInvokeFunc) (*Executor, error) {
	if router == nil {
		return nil, ErrToolRouteRouterRequired
	}
	if invoke == nil {
		return nil, ErrToolInvokerRequired
	}
	approvalKey := make([]byte, minToolApprovalBindingKeyBytes)
	if _, err := rand.Read(approvalKey); err != nil {
		clear(approvalKey)
		return nil, fmt.Errorf("%w: %v", ErrToolApprovalKey, err)
	}
	return &Executor{
		router:      router,
		invoke:      invoke,
		lookupRun:   router.currentRun,
		approvalKey: approvalKey,
	}, nil
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
		return ExecutionResult{}, ErrToolRouteRouterRequired
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
	route, err := e.router.RouteToolCall(e.approvalKey, runID, request, arguments)
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
	if e.lookupRun == nil {
		return ExecutionResult{}, ErrInvalidToolExecution
	}
	current, ok := e.lookupRun(runID)
	if !ok {
		return ExecutionResult{}, ErrInvalidToolExecution
	}
	route.Run = current
	if err := validateExecutionRoute(route, runID, request); err != nil {
		return ExecutionResult{}, err
	}
	execution.Route = route
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
