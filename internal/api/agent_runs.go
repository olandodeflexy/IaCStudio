package api

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/iac-studio/iac-studio/internal/agentruns"
)

type agentRunCreateRequest struct {
	Prompt     string         `json:"prompt"`
	ProviderID string         `json:"provider_id,omitempty"`
	Mode       agentruns.Mode `json:"mode,omitempty"`
	CreatedBy  string         `json:"created_by,omitempty"`
}

func registerAgentRunRoutes(mux *http.ServeMux, projectsDir string, store *agentruns.Store) {
	mux.HandleFunc("GET /api/projects/{name}/agent-runs", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, name) {
			return
		}
		runs := []agentruns.Run{}
		for _, run := range store.List() {
			if run.Project == name {
				runs = append(runs, run)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runs": runs,
		})
	})

	mux.HandleFunc("POST /api/projects/{name}/agent-runs", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		if !requireJSONContentType(w, r) {
			return
		}
		name := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, name) {
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

	mux.HandleFunc("GET /api/projects/{name}/agent-runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, name) {
			return
		}
		run, ok := store.Get(r.PathValue("id"))
		if !ok || run.Project != name {
			http.Error(w, "agent run not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(run)
	})
}

func requireExistingAgentRunProject(w http.ResponseWriter, projectsDir, name string) bool {
	projectPath, err := safeProjectPath(projectsDir, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	info, err := os.Stat(projectPath)
	if os.IsNotExist(err) {
		http.Error(w, "project not found", http.StatusNotFound)
		return false
	}
	if err != nil {
		http.Error(w, "stat project: "+err.Error(), http.StatusInternalServerError)
		return false
	}
	if !info.IsDir() {
		http.Error(w, "project not found", http.StatusNotFound)
		return false
	}
	return true
}
