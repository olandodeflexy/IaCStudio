package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

// AgentToolRouter authorizes and records one fully scoped tool route without
// invoking the external tool.
type AgentToolRouter interface {
	Route(runID string, request agentrouting.Request) (agentrouting.RouteResult, error)
}

type agentToolRouteAuthorizeRequest struct {
	ConnectionID string              `json:"connection_id"`
	ServerID     string              `json:"server_id"`
	ToolName     string              `json:"tool_name"`
	Risk         mcpairlock.ToolRisk `json:"risk"`
}

func registerAgentToolRouteRoutes(
	mux *http.ServeMux,
	projectsDir string,
	store *agentruns.Store,
	router AgentToolRouter,
) {
	if router == nil {
		return
	}
	attempts := newAgentToolRouteAttemptStore(maxAgentToolRouteAttempts)

	mux.HandleFunc("POST /api/projects/{name}/agent-runs/{id}/tool-routes/authorize", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		if !requireJSONContentType(w, r) {
			return
		}
		idempotencyKey, ok := requireAgentToolRouteIdempotencyKey(w, r)
		if !ok {
			return
		}
		name := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, name) {
			return
		}
		run, ok := store.Get(r.PathValue("id"))
		if !ok || run.Project != name {
			http.Error(w, "agent run not found", http.StatusNotFound)
			return
		}
		if run.ProviderID == "" {
			http.Error(w, "agent run provider is not configured", http.StatusConflict)
			return
		}

		var req agentToolRouteAuthorizeRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeAgentToolRouteBodyError(w, err)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeAgentToolRouteBodyError(w, err)
			return
		}

		routeRequest := agentrouting.Request{
			Project:      run.Project,
			ProviderID:   run.ProviderID,
			ConnectionID: req.ConnectionID,
			ServerID:     req.ServerID,
			ToolName:     req.ToolName,
			Mode:         run.Mode,
			Risk:         req.Risk,
		}
		if err := routeRequest.Validate(); err != nil {
			http.Error(w, "invalid tool route request", http.StatusBadRequest)
			return
		}

		result, replayed, err := attempts.authorize(r.Context(), run.ID, idempotencyKey, routeRequest, func() (agentrouting.RouteResult, error) {
			return router.Route(run.ID, routeRequest)
		})
		if err != nil {
			writeAgentToolRouteError(w, err)
			return
		}
		if replayed {
			current, ok := store.Get(run.ID)
			if !ok {
				http.Error(w, "agent run not found", http.StatusNotFound)
				return
			}
			result.Run = current
			w.Header().Set("Idempotency-Replayed", "true")
		}
		setAgentRunJSONHeader(w)
		_ = json.NewEncoder(w).Encode(result)
	})
}

func writeAgentToolRouteBodyError(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid request body", http.StatusBadRequest)
}

func writeAgentToolRouteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAgentToolRouteIdempotencyConflict):
		http.Error(w, "idempotency key was already used for a different tool route", http.StatusConflict)
	case errors.Is(err, errAgentToolRouteAttemptCapacity):
		http.Error(w, "tool route authorization is temporarily unavailable", http.StatusServiceUnavailable)
	case errors.Is(err, agentrouting.ErrInvalidRequest):
		http.Error(w, "invalid tool route request", http.StatusBadRequest)
	case errors.Is(err, agentruns.ErrNotFound):
		http.Error(w, "agent run not found", http.StatusNotFound)
	case errors.Is(err, agentruns.ErrTerminated),
		errors.Is(err, agentrouting.ErrRunScopeMismatch),
		errors.Is(err, agentrouting.ErrInvalidDecision):
		http.Error(w, "agent run cannot authorize this tool route", http.StatusConflict)
	default:
		log.Printf("agent tool route authorization failed (%T)", agentToolRouteRootError(err))
		http.Error(w, "agent tool route authorization failed", http.StatusInternalServerError)
	}
}

func agentToolRouteRootError(err error) error {
	const maxUnwrapDepth = 32
	for range maxUnwrapDepth {
		unwrapped := errors.Unwrap(err)
		if unwrapped == nil {
			return err
		}
		err = unwrapped
	}
	return err
}
