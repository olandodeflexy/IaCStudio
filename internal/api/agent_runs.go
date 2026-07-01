package api

import (
	"encoding/json"
	"net/http"

	"github.com/iac-studio/iac-studio/internal/agentruns"
)

type agentRunCreateRequest struct {
	Prompt     string         `json:"prompt"`
	ProviderID string         `json:"provider_id,omitempty"`
	Mode       agentruns.Mode `json:"mode,omitempty"`
	CreatedBy  string         `json:"created_by,omitempty"`
}

func registerAgentRunRoutes(mux *http.ServeMux, projectsDir string, store *agentruns.Store) {
	mux.HandleFunc("GET /api/agent-runs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runs": store.List(),
		})
	})

	mux.HandleFunc("POST /api/projects/{name}/agent-runs", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		if !requireJSONContentType(w, r) {
			return
		}
		name := r.PathValue("name")
		if _, err := safeProjectPath(projectsDir, name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var req agentRunCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		run, err := store.Create(agentruns.CreateRequest{
			Project:    name,
			Prompt:     req.Prompt,
			ProviderID: req.ProviderID,
			Mode:       req.Mode,
			CreatedBy:  req.CreatedBy,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(run)
	})

	mux.HandleFunc("GET /api/agent-runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		run, ok := store.Get(r.PathValue("id"))
		if !ok {
			http.Error(w, "agent run not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(run)
	})
}
