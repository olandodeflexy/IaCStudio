package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

type fakeAgentToolRouter struct {
	result  agentrouting.RouteResult
	err     error
	calls   int
	runID   string
	request agentrouting.Request
}

func (f *fakeAgentToolRouter) Route(runID string, request agentrouting.Request) (agentrouting.RouteResult, error) {
	f.calls++
	f.runID = runID
	f.request = request
	return f.result, f.err
}

func agentToolRouteFixture(t *testing.T, providerID string) (string, *agentruns.Store, agentruns.Run) {
	t.Helper()
	root := scaffoldAgentRunProject(t)
	store := agentruns.NewStore()
	run, err := store.Create(agentruns.CreateRequest{
		Project:    "demo",
		Prompt:     "inventory the project",
		ProviderID: providerID,
		Mode:       agentruns.ModeReadOnly,
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	return root, store, run
}

func agentToolRouteMux(root string, store *agentruns.Store, router AgentToolRouter) *http.ServeMux {
	mux := http.NewServeMux()
	registerAgentToolRouteRoutes(mux, root, store, router)
	return mux
}

func postAgentToolRoute(mux *http.ServeMux, project, runID, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+project+"/agent-runs/"+runID+"/tool-routes/authorize",
		strings.NewReader(body),
	)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAgentToolRouteUsesServerOwnedRunScope(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	wantDecision := agentrouting.Decision{
		Status:          agentrouting.DecisionAllowed,
		Reason:          agentrouting.ReasonAllowed,
		Allowed:         true,
		UntrustedOutput: true,
	}
	fake := &fakeAgentToolRouter{result: agentrouting.RouteResult{Decision: wantDecision, Run: run}}
	mux := agentToolRouteMux(root, store, fake)

	rec := postAgentToolRoute(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"risk":"read_only"
	}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("authorize status = %d, body = %s", rec.Code, rec.Body.String())
	}
	requireJSONResponse(t, rec)
	if fake.calls != 1 || fake.runID != run.ID {
		t.Fatalf("Route calls = %d, run ID = %q; want one call for %q", fake.calls, fake.runID, run.ID)
	}
	wantRequest := agentrouting.Request{
		Project:      "demo",
		ProviderID:   "codex",
		ConnectionID: "aws-prod",
		ServerID:     "aws",
		ToolName:     "list_buckets",
		Mode:         agentruns.ModeReadOnly,
		Risk:         mcpairlock.RiskReadOnly,
	}
	if fake.request != wantRequest {
		t.Fatalf("Route request = %+v, want server-owned scope %+v", fake.request, wantRequest)
	}
	var result agentrouting.RouteResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Decision != wantDecision || result.Run.ID != run.ID {
		t.Fatalf("response = %+v, want audited router result", result)
	}
}

func TestAgentToolRouteRejectsClientScopeAndCrossProjectRuns(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	fake := &fakeAgentToolRouter{}
	mux := agentToolRouteMux(root, store, fake)

	clientScope := postAgentToolRoute(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"risk":"read_only",
		"provider_id":"other-provider"
	}`, "application/json")
	if clientScope.Code != http.StatusBadRequest {
		t.Fatalf("client scope status = %d, want %d, body = %s", clientScope.Code, http.StatusBadRequest, clientScope.Body.String())
	}

	crossProject := postAgentToolRoute(mux, "other", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"risk":"read_only"
	}`, "application/json")
	if crossProject.Code != http.StatusNotFound {
		t.Fatalf("cross-project status = %d, want %d, body = %s", crossProject.Code, http.StatusNotFound, crossProject.Body.String())
	}
	if fake.calls != 0 {
		t.Fatalf("Route calls = %d, want none for rejected scope", fake.calls)
	}
}

func TestAgentToolRouteRejectsInvalidRequestsBeforeRouting(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	fake := &fakeAgentToolRouter{}
	mux := agentToolRouteMux(root, store, fake)
	validBody := `{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","risk":"read_only"}`

	tests := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
	}{
		{name: "missing content type", body: validBody, wantStatus: http.StatusUnsupportedMediaType},
		{name: "malformed JSON", body: "{not-json", contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "multiple values", body: validBody + `{}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "missing tool", body: `{"connection_id":"aws-prod","server_id":"aws","risk":"read_only"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "oversized body", body: strings.Repeat(" ", maxRequestBody+1), contentType: "application/json", wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := postAgentToolRoute(mux, "demo", run.ID, test.body, test.contentType)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.wantStatus, rec.Body.String())
			}
		})
	}
	if fake.calls != 0 {
		t.Fatalf("Route calls = %d, want none for invalid requests", fake.calls)
	}
}

func TestAgentToolRouteSanitizesRouterErrors(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	fake := &fakeAgentToolRouter{err: errors.New("token=router-secret")}
	mux := agentToolRouteMux(root, store, fake)

	rec := postAgentToolRoute(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"risk":"read_only"
	}`, "application/json")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "router-secret") {
		t.Fatalf("response leaked router error: %s", rec.Body.String())
	}
}
