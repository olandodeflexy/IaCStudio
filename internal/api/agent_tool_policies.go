package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
)

const maxAgentToolPolicyProviderIDLength = 128

// AgentToolPolicyReader returns detached policy snapshots for exact scopes.
type AgentToolPolicyReader interface {
	Get(scope agentrouting.PolicyScope) (agentrouting.Policy, error)
}

type agentToolPolicyResponse struct {
	Scope  agentrouting.PolicyScope `json:"scope"`
	Policy agentrouting.Policy      `json:"policy"`
}

func registerAgentToolPolicyRoutes(mux *http.ServeMux, projectsDir string, policies AgentToolPolicyReader) {
	if policies == nil {
		return
	}
	mux.HandleFunc("GET /api/projects/{name}/agent-routing/policies/{provider}", func(w http.ResponseWriter, r *http.Request) {
		project := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, project) {
			return
		}
		providerID := r.PathValue("provider")
		if len(providerID) > maxAgentToolPolicyProviderIDLength || safePathSegment(providerID) != nil {
			http.Error(w, "invalid agent tool policy provider", http.StatusBadRequest)
			return
		}
		scope := agentrouting.PolicyScope{Project: project, ProviderID: providerID}
		policy, err := policies.Get(scope)
		if err != nil {
			writeAgentToolPolicyReadError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agentToolPolicyResponse{Scope: scope, Policy: policy})
	})
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
