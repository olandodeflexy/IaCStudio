package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/iac-studio/iac-studio/internal/agentruns"
)

type agentRunCreateRequest struct {
	Prompt     string         `json:"prompt"`
	ProviderID string         `json:"provider_id,omitempty"`
	Mode       agentruns.Mode `json:"mode,omitempty"`
}

type agentRunApprovalDecisionRequest struct {
	Decision agentruns.ApprovalStatus `json:"decision"`
}

func registerAgentRunRoutes(mux *http.ServeMux, projectsDir string, store *agentruns.Store) {
	mux.HandleFunc("GET /api/projects/{name}/agent-runs", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, name) {
			return
		}
		setAgentRunJSONHeader(w)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runs": store.ListProjectSummaries(name),
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
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		setAgentRunJSONHeader(w)
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
		setAgentRunJSONHeader(w)
		_ = json.NewEncoder(w).Encode(run)
	})

	mux.HandleFunc("POST /api/projects/{name}/agent-runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, name) {
			return
		}
		id := r.PathValue("id")
		run, ok := store.Get(id)
		if !ok || run.Project != name {
			http.Error(w, "agent run not found", http.StatusNotFound)
			return
		}
		run, err := store.Cancel(id)
		if err != nil {
			if errors.Is(err, agentruns.ErrTerminated) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			if errors.Is(err, agentruns.ErrNotFound) {
				http.Error(w, "agent run not found", http.StatusNotFound)
				return
			}
			http.Error(w, "cancel agent run: "+err.Error(), http.StatusInternalServerError)
			return
		}
		setAgentRunJSONHeader(w)
		_ = json.NewEncoder(w).Encode(run)
	})

	mux.HandleFunc("POST /api/projects/{name}/agent-runs/{id}/approvals/{approval_id}/decision", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		if !requireJSONContentType(w, r) {
			return
		}
		name := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, name) {
			return
		}
		id := r.PathValue("id")
		run, ok := store.Get(id)
		if !ok || run.Project != name {
			http.Error(w, "agent run not found", http.StatusNotFound)
			return
		}

		var req agentRunApprovalDecisionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytes *http.MaxBytesError
			if errors.As(err, &maxBytes) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Decision == "" {
			http.Error(w, "approval decision is required", http.StatusBadRequest)
			return
		}
		if req.Decision != agentruns.ApprovalApproved && req.Decision != agentruns.ApprovalRejected {
			http.Error(w, "invalid approval decision: "+string(req.Decision), http.StatusBadRequest)
			return
		}

		run, err := store.DecideApproval(id, r.PathValue("approval_id"), req.Decision, "")
		if err != nil {
			if errors.Is(err, agentruns.ErrNotFound) {
				http.Error(w, "agent run not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, agentruns.ErrApprovalNotFound) {
				http.Error(w, "approval gate not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, agentruns.ErrTerminated) || errors.Is(err, agentruns.ErrApprovalDecided) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, "decide approval: "+err.Error(), http.StatusInternalServerError)
			return
		}
		setAgentRunJSONHeader(w)
		_ = json.NewEncoder(w).Encode(run)
	})
}

func setAgentRunJSONHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
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
