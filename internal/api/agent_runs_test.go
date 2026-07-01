package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentruns"
)

func agentRunMux(projectsDir string, store *agentruns.Store) *http.ServeMux {
	mux := http.NewServeMux()
	registerAgentRunRoutes(mux, projectsDir, store)
	return mux
}

func scaffoldAgentRunProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"demo", "other"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir project %s: %v", name, err)
		}
	}
	return root
}

func TestAgentRunRoutesCreateListAndGetSanitizedRun(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	store := agentruns.NewStore(agentruns.WithPromptHashKey([]byte("stable-test-key")))
	router := agentRunMux(root, store)

	body := `{
		"prompt": "create a VPC with token=super-secret",
		"provider_id": "codex token=provider-secret",
		"created_by": "alice password=hunter2"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/agent-runs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if _, ok := raw["prompt"]; ok {
		t.Fatal("create response leaked raw prompt field")
	}
	var created agentruns.Run
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created run: %v", err)
	}
	if created.ID == "" || created.Project != "demo" || created.Mode != agentruns.ModeReadOnly || created.Status != agentruns.StatusQueued {
		t.Fatalf("unexpected created run: %+v", created)
	}
	if created.PromptHash == "" {
		t.Fatal("prompt hash is empty")
	}
	serialized := rec.Body.String()
	for _, secret := range []string{"super-secret", "provider-secret", "hunter2"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("response leaked secret %q: %s", secret, serialized)
		}
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/demo/agent-runs/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
	var fetched agentruns.Run
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode fetched run: %v", err)
	}
	if fetched.ID != created.ID || fetched.PromptHash != created.PromptHash {
		t.Fatalf("fetched run mismatch: got %+v want %+v", fetched, created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects/demo/agent-runs", nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Runs []agentruns.Run `json:"runs"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listed runs: %v", err)
	}
	if len(listed.Runs) != 1 || listed.Runs[0].ID != created.ID {
		t.Fatalf("listed runs = %+v, want created run %s", listed.Runs, created.ID)
	}
}

func TestAgentRunRoutesRejectBadRequests(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	router := agentRunMux(root, agentruns.NewStore())

	cases := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{"missing content type", "/api/projects/demo/agent-runs", `{"prompt":"x"}`, http.StatusUnsupportedMediaType},
		{"malformed JSON", "/api/projects/demo/agent-runs", "{not-json", http.StatusBadRequest},
		{"missing prompt", "/api/projects/demo/agent-runs", `{}`, http.StatusBadRequest},
		{"invalid mode", "/api/projects/demo/agent-runs", `{"prompt":"x","mode":"write_everything"}`, http.StatusBadRequest},
		{"project traversal", "/api/projects/%2e%2e%2fetc/agent-runs", `{"prompt":"x"}`, http.StatusBadRequest},
		{"missing project", "/api/projects/missing/agent-runs", `{"prompt":"x"}`, http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			if tc.name != "missing content type" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

func TestAgentRunRoutesReturn404ForMissingRun(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	router := agentRunMux(root, agentruns.NewStore())

	req := httptest.NewRequest(http.MethodGet, "/api/projects/demo/agent-runs/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestAgentRunRoutesDoNotExposeRunsAcrossProjects(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	store := agentruns.NewStore()
	router := agentRunMux(root, store)

	run, err := store.Create(agentruns.CreateRequest{
		Project: "demo",
		Prompt:  "create a VPC",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/other/agent-runs/"+run.ID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("cross-project get status = %d, want %d, body = %s", getRec.Code, http.StatusNotFound, getRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects/other/agent-runs", nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("cross-project list status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Runs []agentruns.Run `json:"runs"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listed runs: %v", err)
	}
	if len(listed.Runs) != 0 {
		t.Fatalf("cross-project list leaked runs: %+v", listed.Runs)
	}
}
