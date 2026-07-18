package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
)

const maxAgentToolPolicyProviderIDLength = 128

// AgentToolPolicyReader returns detached policy snapshots for exact scopes.
type AgentToolPolicyReader interface {
	Get(scope agentrouting.PolicyScope) (agentrouting.Policy, error)
}

// AgentToolPolicyStore reads and replaces exact scoped policy snapshots.
type AgentToolPolicyStore interface {
	AgentToolPolicyReader
	Save(scope agentrouting.PolicyScope, policy agentrouting.Policy) error
}

type agentToolPolicyResponse struct {
	Scope  agentrouting.PolicyScope `json:"scope"`
	Policy agentrouting.Policy      `json:"policy"`
}

func registerAgentToolPolicyRoutes(mux *http.ServeMux, projectsDir string, policies AgentToolPolicyStore) {
	if policies == nil {
		return
	}
	mux.HandleFunc("GET /api/projects/{name}/agent-routing/policies/{provider}", func(w http.ResponseWriter, r *http.Request) {
		scope, ok := agentToolPolicyScope(w, r, projectsDir)
		if !ok {
			return
		}
		policy, err := policies.Get(scope)
		if err != nil {
			writeAgentToolPolicyReadError(w, err)
			return
		}
		writeAgentToolPolicyResponse(w, scope, policy)
	})

	mux.HandleFunc("PUT /api/projects/{name}/agent-routing/policies/{provider}", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		if !requireJSONContentType(w, r) {
			return
		}
		scope, ok := agentToolPolicyScope(w, r, projectsDir)
		if !ok {
			return
		}

		var policy agentrouting.Policy
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&policy); err != nil {
			writeAgentToolPolicyBodyError(w, err)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeAgentToolPolicyBodyError(w, err)
			return
		}
		if policy.Rules == nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := policies.Save(scope, policy); err != nil {
			writeAgentToolPolicySaveError(w, err)
			return
		}
		saved, err := policies.Get(scope)
		if err != nil {
			writeAgentToolPolicyReadError(w, err)
			return
		}
		writeAgentToolPolicyResponse(w, scope, saved)
	})
}

func agentToolPolicyScope(w http.ResponseWriter, r *http.Request, projectsDir string) (agentrouting.PolicyScope, bool) {
	project := r.PathValue("name")
	if !requireExistingAgentRunProject(w, projectsDir, project) {
		return agentrouting.PolicyScope{}, false
	}
	providerID := r.PathValue("provider")
	if len(providerID) > maxAgentToolPolicyProviderIDLength || safePathSegment(providerID) != nil {
		http.Error(w, "invalid agent tool policy provider", http.StatusBadRequest)
		return agentrouting.PolicyScope{}, false
	}
	return agentrouting.PolicyScope{Project: project, ProviderID: providerID}, true
}

func writeAgentToolPolicyResponse(w http.ResponseWriter, scope agentrouting.PolicyScope, policy agentrouting.Policy) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agentToolPolicyResponse{Scope: scope, Policy: policy})
}

func writeAgentToolPolicyBodyError(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid request body", http.StatusBadRequest)
}

func writeAgentToolPolicySaveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentrouting.ErrInvalidPolicyScope),
		errors.Is(err, agentrouting.ErrInvalidRule),
		errors.Is(err, agentrouting.ErrPolicyScopeMismatch):
		http.Error(w, "invalid agent tool policy", http.StatusBadRequest)
	default:
		log.Printf("agent tool policy save failed (%T)", err)
		http.Error(w, "agent tool policy save failed", http.StatusInternalServerError)
	}
}

func writeAgentToolPolicyReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentrouting.ErrInvalidPolicyScope):
		http.Error(w, "invalid agent tool policy scope", http.StatusBadRequest)
	case errors.Is(err, agentrouting.ErrPolicyNotFound):
		http.Error(w, "agent tool policy not found", http.StatusNotFound)
	default:
		log.Printf("agent tool policy read failed (%T)", err)
		http.Error(w, "agent tool policy read failed", http.StatusInternalServerError)
	}
}
