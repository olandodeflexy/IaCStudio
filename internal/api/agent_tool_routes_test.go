package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

type fakeAgentToolRouter struct {
	result         agentrouting.RouteResult
	err            error
	route          func(string, agentrouting.Request) (agentrouting.RouteResult, error)
	calls          int
	runID          string
	request        agentrouting.Request
	previewResult  agentrouting.Decision
	previewErr     error
	previewCalls   int
	previewRequest agentrouting.Request
}

type agentToolRouteTestRootError struct{}

func (e *agentToolRouteTestRootError) Error() string {
	return "root"
}

func (f *fakeAgentToolRouter) Route(runID string, request agentrouting.Request) (agentrouting.RouteResult, error) {
	f.calls++
	f.runID = runID
	f.request = request
	if f.route != nil {
		return f.route(runID, request)
	}
	return f.result, f.err
}

func (f *fakeAgentToolRouter) Preview(request agentrouting.Request) (agentrouting.Decision, error) {
	f.previewCalls++
	f.previewRequest = request
	return f.previewResult, f.previewErr
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
	return postAgentToolRouteWithKey(mux, project, runID, body, contentType, "route-attempt-1")
}

func postAgentToolRouteWithKey(mux *http.ServeMux, project, runID, body, contentType, idempotencyKey string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+project+"/agent-runs/"+runID+"/tool-routes/authorize",
		strings.NewReader(body),
	)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if idempotencyKey != "" {
		req.Header.Set(agentToolRouteIdempotencyHeader, idempotencyKey)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func postAgentToolRoutePreview(mux *http.ServeMux, project, runID, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+project+"/agent-runs/"+runID+"/tool-routes/preview",
		strings.NewReader(body),
	)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAgentToolRoutePreviewUsesServerOwnedScopeWithoutMutation(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	before, ok := store.Get(run.ID)
	if !ok {
		t.Fatalf("Get(%q) returned no run", run.ID)
	}
	wantDecision := agentrouting.Decision{
		Status:          agentrouting.DecisionAllowed,
		Reason:          agentrouting.ReasonAllowed,
		Allowed:         true,
		UntrustedOutput: true,
	}
	fake := &fakeAgentToolRouter{previewResult: wantDecision}
	mux := agentToolRouteMux(root, store, fake)

	rec := postAgentToolRoutePreview(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"risk":"read_only"
	}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", rec.Code, rec.Body.String())
	}
	requireJSONResponse(t, rec)
	wantRequest := agentrouting.Request{
		Project:      "demo",
		ProviderID:   "codex",
		ConnectionID: "aws-prod",
		ServerID:     "aws",
		ToolName:     "list_buckets",
		Mode:         agentruns.ModeReadOnly,
		Risk:         mcpairlock.RiskReadOnly,
	}
	if fake.previewCalls != 1 || fake.previewRequest != wantRequest || fake.calls != 0 {
		t.Fatalf("Preview calls = %d, request = %+v, Route calls = %d; want one scoped preview only", fake.previewCalls, fake.previewRequest, fake.calls)
	}
	var response struct {
		Decision agentrouting.Decision `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Decision != wantDecision {
		t.Fatalf("preview decision = %+v, want %+v", response.Decision, wantDecision)
	}
	after, ok := store.Get(run.ID)
	if !ok || !reflect.DeepEqual(after, before) {
		t.Fatalf("run mutated by preview: before=%+v after=%+v", before, after)
	}
}

func TestAgentToolRoutePreviewRejectsClientScopeAndTerminalRuns(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	fake := &fakeAgentToolRouter{}
	mux := agentToolRouteMux(root, store, fake)

	clientScope := postAgentToolRoutePreview(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"risk":"read_only",
		"mode":"approved_execute"
	}`, "application/json")
	if clientScope.Code != http.StatusBadRequest {
		t.Fatalf("client scope status = %d, want %d, body = %s", clientScope.Code, http.StatusBadRequest, clientScope.Body.String())
	}
	if _, err := store.SetStatus(run.ID, agentruns.StatusCompleted); err != nil {
		t.Fatalf("SetStatus(completed): %v", err)
	}
	terminal := postAgentToolRoutePreview(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"risk":"read_only"
	}`, "application/json")
	if terminal.Code != http.StatusConflict {
		t.Fatalf("terminal status = %d, want %d, body = %s", terminal.Code, http.StatusConflict, terminal.Body.String())
	}
	if fake.previewCalls != 0 {
		t.Fatalf("Preview calls = %d, want none for rejected requests", fake.previewCalls)
	}
}

func TestAgentToolRoutePreviewSanitizesServiceFailures(t *testing.T) {
	tests := []struct {
		name string
		fake *fakeAgentToolRouter
	}{
		{name: "service error", fake: &fakeAgentToolRouter{previewErr: errors.New("token=preview-secret")}},
		{name: "invalid decision", fake: &fakeAgentToolRouter{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, store, run := agentToolRouteFixture(t, "codex")
			mux := agentToolRouteMux(root, store, test.fake)
			rec := postAgentToolRoutePreview(mux, "demo", run.ID, `{
				"connection_id":"aws-prod",
				"server_id":"aws",
				"tool_name":"list_buckets",
				"risk":"read_only"
			}`, "application/json")
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "preview-secret") {
				t.Fatalf("response leaked preview error: %s", rec.Body.String())
			}
		})
	}
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

func TestAgentToolRouteRequiresIdempotencyKey(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	fake := &fakeAgentToolRouter{}
	mux := agentToolRouteMux(root, store, fake)
	body := `{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","risk":"read_only"}`

	tests := []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "padded", key: " padded "},
		{name: "too long", key: strings.Repeat("x", maxAgentToolRouteIdempotencyKeyLength+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := postAgentToolRouteWithKey(mux, "demo", run.ID, body, "application/json", test.key)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
	if fake.calls != 0 {
		t.Fatalf("Route calls = %d, want none without a valid idempotency key", fake.calls)
	}
}

func TestAgentToolRouteReplaysRecordedDecisionWithoutDuplicateMutation(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	decision := agentrouting.Decision{
		Status:           agentrouting.DecisionApprovalRequired,
		Reason:           agentrouting.ReasonApprovalRequired,
		Allowed:          true,
		ApprovalRequired: true,
		UntrustedOutput:  true,
	}
	fake := &fakeAgentToolRouter{}
	fake.route = func(_ string, _ agentrouting.Request) (agentrouting.RouteResult, error) {
		updated, err := store.AddApproval(run.ID, agentruns.ApprovalGate{
			Kind:    agentruns.ApprovalMCPNetwork,
			Summary: "authorize list_buckets",
		})
		return agentrouting.RouteResult{Decision: decision, Run: updated}, err
	}
	mux := agentToolRouteMux(root, store, fake)
	body := `{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","risk":"read_only"}`

	first := postAgentToolRouteWithKey(mux, "demo", run.ID, body, "application/json", "same-attempt")
	second := postAgentToolRouteWithKey(mux, "demo", run.ID, body, "application/json", "same-attempt")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d; want two successful responses", first.Code, second.Code)
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay header = %q, want true", second.Header().Get("Idempotency-Replayed"))
	}
	current, ok := store.Get(run.ID)
	if !ok || fake.calls != 1 || len(current.Approvals) != 1 {
		t.Fatalf("Route calls = %d, approvals = %d; want one recorded attempt", fake.calls, len(current.Approvals))
	}

	conflictBody := `{"connection_id":"aws-prod","server_id":"aws","tool_name":"get_bucket","risk":"read_only"}`
	conflict := postAgentToolRouteWithKey(mux, "demo", run.ID, conflictBody, "application/json", "same-attempt")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want %d, body = %s", conflict.Code, http.StatusConflict, conflict.Body.String())
	}
	if fake.calls != 1 {
		t.Fatalf("Route calls = %d, want no call after conflicting key reuse", fake.calls)
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

func TestAgentToolRouteRootErrorReturnsDeepestWrappedType(t *testing.T) {
	funcError := &agentToolRouteTestRootError{}
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", funcError))

	if got := agentToolRouteRootError(wrapped); got != funcError {
		t.Fatalf("root error = %T, want %T", got, funcError)
	}
}
