package api

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/iac-studio/iac-studio/internal/agentruns"
)

type agentRunCreateRequest struct {
	Prompt     string         `json:"prompt"`
	ProviderID string         `json:"provider_id,omitempty"`
	Mode       agentruns.Mode `json:"mode,omitempty"`
}

type agentRunSummary struct {
	ID                   string           `json:"id"`
	Project              string           `json:"project"`
	ProviderID           string           `json:"provider_id,omitempty"`
	Mode                 agentruns.Mode   `json:"mode"`
	Status               agentruns.Status `json:"status"`
	PromptPreview        string           `json:"prompt_preview"`
	PromptHash           string           `json:"prompt_hash"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
	StartedAt            *time.Time       `json:"started_at,omitempty"`
	CompletedAt          *time.Time       `json:"completed_at,omitempty"`
	Canceled             bool             `json:"canceled"`
	Error                string           `json:"error,omitempty"`
	LogCount             int              `json:"log_count"`
	PatchCount           int              `json:"patch_count"`
	ApprovalCount        int              `json:"approval_count"`
	PendingApprovalCount int              `json:"pending_approval_count"`
}

func registerAgentRunRoutes(mux *http.ServeMux, projectsDir string, store *agentruns.Store) {
	mux.HandleFunc("GET /api/projects/{name}/agent-runs", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !requireExistingAgentRunProject(w, projectsDir, name) {
			return
		}
		runs := []agentRunSummary{}
		for _, run := range store.List() {
			if run.Project == name {
				runs = append(runs, summarizeAgentRun(run))
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

func summarizeAgentRun(run agentruns.Run) agentRunSummary {
	summary := agentRunSummary{
		ID:            run.ID,
		Project:       run.Project,
		ProviderID:    run.ProviderID,
		Mode:          run.Mode,
		Status:        run.Status,
		PromptPreview: run.PromptPreview,
		PromptHash:    run.PromptHash,
		CreatedAt:     run.CreatedAt,
		UpdatedAt:     run.UpdatedAt,
		StartedAt:     run.StartedAt,
		CompletedAt:   run.CompletedAt,
		Canceled:      run.Canceled,
		Error:         run.Error,
		LogCount:      len(run.Logs),
		PatchCount:    len(run.Patches),
		ApprovalCount: len(run.Approvals),
	}
	for _, approval := range run.Approvals {
		if approval.Status == agentruns.ApprovalPending {
			summary.PendingApprovalCount++
		}
	}
	return summary
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
