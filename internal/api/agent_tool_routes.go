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

// AgentToolRouter previews or records one fully scoped tool route without
// invoking the external tool.
type AgentToolRouter interface {
	Preview(request agentrouting.Request) (agentrouting.Decision, error)
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
		run, routeRequest, ok := readAgentToolRouteRequest(w, r, projectsDir, store)
		if !ok {
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

	mux.HandleFunc("POST /api/projects/{name}/agent-runs/{id}/tool-routes/preview", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		if !requireJSONContentType(w, r) {
			return
		}
		run, routeRequest, ok := readAgentToolRouteRequest(w, r, projectsDir, store)
		if !ok {
			return
		}
		if agentToolRouteRunIsTerminal(run.Status) {
			http.Error(w, "agent run cannot preview this tool route", http.StatusConflict)
			return
		}

		decision, err := router.Preview(routeRequest)
		if err == nil {
			err = decision.Validate()
		}
		if err != nil {
			writeAgentToolRoutePreviewError(w, err)
			return
		}
		setAgentRunJSONHeader(w)
		_ = json.NewEncoder(w).Encode(map[string]agentrouting.Decision{"decision": decision})
	})
}

func readAgentToolRouteRequest(
	w http.ResponseWriter,
	r *http.Request,
	projectsDir string,
	store *agentruns.Store,
) (agentruns.Run, agentrouting.Request, bool) {
	name := r.PathValue("name")
	if !requireExistingAgentRunProject(w, projectsDir, name) {
		return agentruns.Run{}, agentrouting.Request{}, false
	}
	run, ok := store.Get(r.PathValue("id"))
	if !ok || run.Project != name {
		http.Error(w, "agent run not found", http.StatusNotFound)
		return agentruns.Run{}, agentrouting.Request{}, false
	}
	if run.ProviderID == "" {
		http.Error(w, "agent run provider is not configured", http.StatusConflict)
		return agentruns.Run{}, agentrouting.Request{}, false
	}

	var req agentToolRouteAuthorizeRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeAgentToolRouteBodyError(w, err)
		return agentruns.Run{}, agentrouting.Request{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAgentToolRouteBodyError(w, err)
		return agentruns.Run{}, agentrouting.Request{}, false
	}

	request := agentrouting.Request{
		Project:      run.Project,
		ProviderID:   run.ProviderID,
		ConnectionID: req.ConnectionID,
		ServerID:     req.ServerID,
		ToolName:     req.ToolName,
		Mode:         run.Mode,
		Risk:         req.Risk,
	}
	if err := request.Validate(); err != nil {
		http.Error(w, "invalid tool route request", http.StatusBadRequest)
		return agentruns.Run{}, agentrouting.Request{}, false
	}
	return run, request, true
}

func agentToolRouteRunIsTerminal(status agentruns.Status) bool {
	switch status {
	case agentruns.StatusCompleted, agentruns.StatusFailed, agentruns.StatusCanceled:
		return true
	default:
		return false
	}
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

func writeAgentToolRoutePreviewError(w http.ResponseWriter, err error) {
	if errors.Is(err, agentrouting.ErrInvalidRequest) {
		http.Error(w, "invalid tool route request", http.StatusBadRequest)
		return
	}
	log.Printf("agent tool route preview failed (%T)", agentToolRouteRootError(err))
	http.Error(w, "agent tool route preview failed", http.StatusInternalServerError)
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
