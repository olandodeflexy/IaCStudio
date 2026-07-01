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
	requireJSONResponse(t, rec)
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if _, ok := raw["prompt"]; ok {
		t.Fatal("create response leaked raw prompt field")
	}
	if _, ok := raw["created_by"]; ok {
		t.Fatal("create response accepted client-supplied created_by field")
	}
	var created agentruns.Run
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created run: %v", err)
	}
	if created.ID == "" || created.Project != "demo" || created.Mode != agentruns.ModeReadOnly || created.Status != agentruns.StatusQueued {
		t.Fatalf("unexpected created run: %+v", created)
	}
	if created.CreatedBy != "" {
		t.Fatalf("created_by = %q, want empty until identity is server-derived", created.CreatedBy)
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
	requireJSONResponse(t, getRec)
	var fetched agentruns.Run
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode fetched run: %v", err)
	}
	if fetched.ID != created.ID || fetched.PromptHash != created.PromptHash {
		t.Fatalf("fetched run mismatch: got %+v want %+v", fetched, created)
	}

	if _, err := store.AddLog(created.ID, agentruns.LogInfo, "started with token=log-secret"); err != nil {
		t.Fatalf("add log: %v", err)
	}
	if _, err := store.AddPatch(created.ID, agentruns.ProposedPatch{
		Path:    "main.tf",
		Summary: "add resource",
		Diff:    "+ token=diff-secret",
	}); err != nil {
		t.Fatalf("add patch: %v", err)
	}
	if _, err := store.AddApproval(created.ID, agentruns.ApprovalGate{
		Kind:    agentruns.ApprovalCommand,
		Summary: "run command with token=approval-secret",
	}); err != nil {
		t.Fatalf("add approval: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects/demo/agent-runs", nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	requireJSONResponse(t, listRec)
	var listed struct {
		Runs []map[string]json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listed runs: %v", err)
	}
	if len(listed.Runs) != 1 {
		t.Fatalf("listed runs = %+v, want created run %s", listed.Runs, created.ID)
	}
	summary := listed.Runs[0]
	if got := rawString(t, summary, "id"); got != created.ID {
		t.Fatalf("listed run id = %q, want %q", got, created.ID)
	}
	for _, field := range []string{"logs", "patches", "approvals", "created_by"} {
		if _, ok := summary[field]; ok {
			t.Fatalf("list summary leaked field %q: %s", field, listRec.Body.String())
		}
	}
	if got := rawInt(t, summary, "log_count"); got != 1 {
		t.Fatalf("log_count = %d, want 1", got)
	}
	if got := rawInt(t, summary, "patch_count"); got != 1 {
		t.Fatalf("patch_count = %d, want 1", got)
	}
	if got := rawInt(t, summary, "approval_count"); got != 1 {
		t.Fatalf("approval_count = %d, want 1", got)
	}
	if got := rawInt(t, summary, "pending_approval_count"); got != 1 {
		t.Fatalf("pending_approval_count = %d, want 1", got)
	}
	for _, secret := range []string{"log-secret", "diff-secret", "approval-secret"} {
		if strings.Contains(listRec.Body.String(), secret) {
			t.Fatalf("list response leaked heavy-field secret %q: %s", secret, listRec.Body.String())
		}
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
		Runs []agentruns.RunSummary `json:"runs"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode listed runs: %v", err)
	}
	if len(listed.Runs) != 0 {
		t.Fatalf("cross-project list leaked runs: %+v", listed.Runs)
	}
}

func TestAgentRunRoutesCancelRun(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	store := agentruns.NewStore()
	router := agentRunMux(root, store)

	run, err := store.Create(agentruns.CreateRequest{
		Project: "demo",
		Prompt:  "review this project",
		Mode:    agentruns.ModeProposeOnly,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/agent-runs/"+run.ID+"/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	requireJSONResponse(t, rec)
	var canceled agentruns.Run
	if err := json.Unmarshal(rec.Body.Bytes(), &canceled); err != nil {
		t.Fatalf("decode canceled run: %v", err)
	}
	if canceled.ID != run.ID || canceled.Project != "demo" || canceled.Status != agentruns.StatusCanceled || !canceled.Canceled || canceled.CompletedAt == nil {
		t.Fatalf("unexpected canceled run: %+v", canceled)
	}

	got, ok := store.Get(run.ID)
	if !ok {
		t.Fatal("expected canceled run to remain in store")
	}
	if got.Status != agentruns.StatusCanceled || !got.Canceled {
		t.Fatalf("store did not persist canceled state: %+v", got)
	}
}

func TestAgentRunRoutesDoNotCancelRunsAcrossProjects(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	store := agentruns.NewStore()
	router := agentRunMux(root, store)

	run, err := store.Create(agentruns.CreateRequest{
		Project: "demo",
		Prompt:  "review this project",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/other/agent-runs/"+run.ID+"/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project cancel status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got, ok := store.Get(run.ID)
	if !ok {
		t.Fatal("expected original run to remain in store")
	}
	if got.Status != agentruns.StatusQueued || got.Canceled {
		t.Fatalf("cross-project cancel mutated run: %+v", got)
	}
}

func TestAgentRunRoutesReturnConflictWhenCancelingTerminalRun(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	store := agentruns.NewStore()
	router := agentRunMux(root, store)

	run, err := store.Create(agentruns.CreateRequest{
		Project: "demo",
		Prompt:  "review this project",
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := store.SetStatus(run.ID, agentruns.StatusCompleted); err != nil {
		t.Fatalf("complete run: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/agent-runs/"+run.ID+"/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("terminal cancel status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	got, ok := store.Get(run.ID)
	if !ok {
		t.Fatal("expected completed run to remain in store")
	}
	if got.Status != agentruns.StatusCompleted || got.Canceled {
		t.Fatalf("terminal cancel mutated run: %+v", got)
	}
}

func rawString(t *testing.T, raw map[string]json.RawMessage, field string) string {
	t.Helper()
	value, ok := raw[field]
	if !ok {
		t.Fatalf("missing field %q in %#v", field, raw)
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatalf("decode field %q as string: %v", field, err)
	}
	return decoded
}

func rawInt(t *testing.T, raw map[string]json.RawMessage, field string) int {
	t.Helper()
	value, ok := raw[field]
	if !ok {
		t.Fatalf("missing field %q in %#v", field, raw)
	}
	var decoded int
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatalf("decode field %q as int: %v", field, err)
	}
	return decoded
}

func requireJSONResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}
