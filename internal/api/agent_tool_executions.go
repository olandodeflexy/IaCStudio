package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

// AgentToolExecutor is the guarded execution boundary exposed to the API.
// Implementations remain responsible for authorization, audit recording, and
// output validation before returning a result.
type AgentToolExecutor interface {
	Execute(
		context.Context,
		string,
		agentrouting.Request,
		mcpairlock.ToolCallArguments,
	) (agentrouting.ExecutionResult, error)
}

type agentToolExecutionRequest struct {
	ConnectionID string                       `json:"connection_id"`
	ServerID     string                       `json:"server_id"`
	ToolName     string                       `json:"tool_name"`
	Arguments    mcpairlock.ToolCallArguments `json:"arguments"`
}

func registerAgentToolExecutionRoutes(
	mux *http.ServeMux,
	projectsDir string,
	store *agentruns.Store,
	executor AgentToolExecutor,
	evaluator agentrouting.ToolEvaluator,
) {
	if missingAgentToolExecutionDependency(executor) || missingAgentToolExecutionDependency(evaluator) {
		return
	}
	attempts := newAgentToolExecutionAttemptStore(maxAgentToolExecutionReplayEntries)

	mux.HandleFunc("POST /api/projects/{name}/agent-runs/{id}/tool-routes/execute", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		if !requireJSONContentType(w, r) {
			return
		}
		idempotencyKey, ok := requireAgentToolRouteIdempotencyKey(w, r)
		if !ok {
			return
		}
		run, routeRequest, arguments, ok := readAgentToolExecutionRequest(w, r, projectsDir, store, evaluator)
		if !ok {
			return
		}

		result, replayed, err := attempts.execute(
			r.Context(),
			run.ID,
			idempotencyKey,
			routeRequest,
			arguments,
			func() (agentrouting.ExecutionResult, error) {
				if err := requireActiveAgentToolExecutionRun(store, run.ID); err != nil {
					return agentrouting.ExecutionResult{}, err
				}
				return executor.Execute(r.Context(), run.ID, routeRequest, arguments)
			},
		)
		if err != nil {
			writeAgentToolExecutionError(w, err)
			return
		}
		if replayed {
			current, ok := store.Get(run.ID)
			if !ok {
				http.Error(w, "agent run not found", http.StatusNotFound)
				return
			}
			result.Route.Run = current
			w.Header().Set("Idempotency-Replayed", "true")
		}
		setAgentRunJSONHeader(w)
		_ = json.NewEncoder(w).Encode(result)
	})
}

func missingAgentToolExecutionDependency(dependency any) bool {
	if dependency == nil {
		return true
	}
	value := reflect.ValueOf(dependency)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func readAgentToolExecutionRequest(
	w http.ResponseWriter,
	r *http.Request,
	projectsDir string,
	store *agentruns.Store,
	evaluator agentrouting.ToolEvaluator,
) (agentruns.Run, agentrouting.Request, mcpairlock.ToolCallArguments, bool) {
	name := r.PathValue("name")
	if !requireExistingAgentRunProject(w, projectsDir, name) {
		return agentruns.Run{}, agentrouting.Request{}, mcpairlock.ToolCallArguments{}, false
	}
	run, ok := store.Get(r.PathValue("id"))
	if !ok || run.Project != name {
		http.Error(w, "agent run not found", http.StatusNotFound)
		return agentruns.Run{}, agentrouting.Request{}, mcpairlock.ToolCallArguments{}, false
	}
	if run.ProviderID == "" {
		http.Error(w, "agent run provider is not configured", http.StatusConflict)
		return agentruns.Run{}, agentrouting.Request{}, mcpairlock.ToolCallArguments{}, false
	}
	var req agentToolExecutionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeAgentToolRouteBodyError(w, err)
		return agentruns.Run{}, agentrouting.Request{}, mcpairlock.ToolCallArguments{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAgentToolRouteBodyError(w, err)
		return agentruns.Run{}, agentrouting.Request{}, mcpairlock.ToolCallArguments{}, false
	}

	// Risk is server-owned; unknown is used only for structural validation
	// before Airlock resolves the discovered tool's current classification.
	request := agentrouting.Request{
		Project:      run.Project,
		ProviderID:   run.ProviderID,
		ConnectionID: req.ConnectionID,
		ServerID:     req.ServerID,
		ToolName:     req.ToolName,
		Mode:         run.Mode,
		Risk:         mcpairlock.RiskUnknown,
	}
	if err := request.Validate(); err != nil {
		http.Error(w, "invalid tool execution request", http.StatusBadRequest)
		return agentruns.Run{}, agentrouting.Request{}, mcpairlock.ToolCallArguments{}, false
	}
	if _, err := req.Arguments.MarshalJSON(); err != nil {
		http.Error(w, "invalid tool execution arguments", http.StatusBadRequest)
		return agentruns.Run{}, agentrouting.Request{}, mcpairlock.ToolCallArguments{}, false
	}
	risk, err := resolveAgentToolExecutionRisk(evaluator, request)
	if err != nil {
		writeAgentToolExecutionError(w, err)
		return agentruns.Run{}, agentrouting.Request{}, mcpairlock.ToolCallArguments{}, false
	}
	request.Risk = risk
	return run, request, req.Arguments, true
}

func resolveAgentToolExecutionRisk(
	evaluator agentrouting.ToolEvaluator,
	request agentrouting.Request,
) (mcpairlock.ToolRisk, error) {
	entry, err := evaluator.EvaluateTool(request.ServerID, request.Project, request.ToolName)
	if err != nil {
		return "", fmt.Errorf("evaluate MCP tool risk: %w", err)
	}
	if entry.ServerID != request.ServerID ||
		entry.Name != request.ToolName ||
		entry.Risk != entry.Decision.Risk {
		return "", agentrouting.ErrInvalidToolExecution
	}
	request.Risk = entry.Risk
	if err := request.Validate(); err != nil {
		return "", agentrouting.ErrInvalidToolExecution
	}
	return entry.Risk, nil
}

func agentToolExecutionRunIsActive(run agentruns.Run) bool {
	if run.Canceled || (run.Status != agentruns.StatusQueued && run.Status != agentruns.StatusRunning) {
		return false
	}
	for _, approval := range run.Approvals {
		if approval.Status == agentruns.ApprovalPending {
			return false
		}
	}
	return true
}

func requireActiveAgentToolExecutionRun(store *agentruns.Store, runID string) error {
	run, ok := store.Get(runID)
	if !ok {
		return agentruns.ErrNotFound
	}
	if !agentToolExecutionRunIsActive(run) {
		return agentrouting.ErrInvalidToolExecution
	}
	return nil
}

func writeAgentToolExecutionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAgentToolExecutionIdempotencyConflict):
		http.Error(w, "idempotency key was already used for a different tool execution", http.StatusConflict)
	case errors.Is(err, errAgentToolExecutionInvalidIdempotencyKey),
		errors.Is(err, agentrouting.ErrInvalidRequest),
		errors.Is(err, mcpairlock.ErrUnknownServer),
		errors.Is(err, mcpairlock.ErrInvalidToolCallArguments),
		errors.Is(err, mcpairlock.ErrInvalidToolCallRequest):
		http.Error(w, "invalid tool execution request", http.StatusBadRequest)
	case errors.Is(err, errAgentToolExecutionAttemptCapacity):
		http.Error(w, "tool execution is temporarily unavailable", http.StatusServiceUnavailable)
	case errors.Is(err, agentruns.ErrNotFound):
		http.Error(w, "agent run not found", http.StatusNotFound)
	case errors.Is(err, agentruns.ErrTerminated),
		errors.Is(err, agentrouting.ErrRunScopeMismatch),
		errors.Is(err, agentrouting.ErrInvalidDecision),
		errors.Is(err, agentrouting.ErrInvalidToolExecution):
		http.Error(w, "agent run cannot execute this tool route", http.StatusConflict)
	case errors.Is(err, agentrouting.ErrToolInvocationFailed):
		http.Error(w, "MCP tool invocation failed", http.StatusBadGateway)
	case errors.Is(err, context.Canceled):
		http.Error(w, "tool execution request canceled", http.StatusRequestTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "tool execution request timed out", http.StatusGatewayTimeout)
	default:
		log.Printf("agent tool execution failed (%T)", agentToolRouteRootError(err))
		http.Error(w, "agent tool execution failed", http.StatusInternalServerError)
	}
}
