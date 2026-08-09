package api

import (
	"bytes"
	"context"
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

type fakeAgentToolExecutor struct {
	result    agentrouting.ExecutionResult
	err       error
	calls     int
	runID     string
	request   agentrouting.Request
	arguments mcpairlock.ToolCallArguments
}

type fakeAgentToolEvaluator struct {
	entry    mcpairlock.ToolInventoryEntry
	err      error
	calls    int
	serverID string
	project  string
	toolName string
}

func (f *fakeAgentToolEvaluator) EvaluateTool(serverID, project, toolName string) (mcpairlock.ToolInventoryEntry, error) {
	f.calls++
	f.serverID = serverID
	f.project = project
	f.toolName = toolName
	return f.entry, f.err
}

func (f *fakeAgentToolExecutor) Execute(
	_ context.Context,
	runID string,
	request agentrouting.Request,
	arguments mcpairlock.ToolCallArguments,
) (agentrouting.ExecutionResult, error) {
	f.calls++
	f.runID = runID
	f.request = request
	f.arguments = arguments
	return f.result, f.err
}

func agentToolExecutionMux(root string, store *agentruns.Store, executor AgentToolExecutor) *http.ServeMux {
	return agentToolExecutionMuxWithEvaluator(root, store, executor, readOnlyAgentToolEvaluator())
}

func agentToolExecutionMuxWithEvaluator(
	root string,
	store *agentruns.Store,
	executor AgentToolExecutor,
	evaluator agentrouting.ToolEvaluator,
) *http.ServeMux {
	mux := http.NewServeMux()
	registerAgentToolExecutionRoutes(mux, root, store, executor, evaluator)
	return mux
}

func readOnlyAgentToolEvaluator() *fakeAgentToolEvaluator {
	return &fakeAgentToolEvaluator{entry: mcpairlock.ToolInventoryEntry{
		ServerID: "aws",
		Name:     "list_buckets",
		Risk:     mcpairlock.RiskReadOnly,
		Decision: mcpairlock.ToolDecision{Risk: mcpairlock.RiskReadOnly},
	}}
}

func postAgentToolExecution(
	mux *http.ServeMux,
	project string,
	runID string,
	body string,
	contentType string,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+project+"/agent-runs/"+runID+"/tool-routes/execute",
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

func TestAgentToolExecutionUsesServerResolvedReadOnlyRisk(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	wantResult := mcpairlock.NewToolCallResult([]byte("reports\n"), false)
	wantDecision := agentrouting.Decision{
		Status:          agentrouting.DecisionAllowed,
		Reason:          agentrouting.ReasonAllowed,
		Allowed:         true,
		UntrustedOutput: true,
	}
	fake := &fakeAgentToolExecutor{result: agentrouting.ExecutionResult{
		Route:   agentrouting.RouteResult{Decision: wantDecision, Run: run},
		Invoked: true,
		Result:  &wantResult,
	}}
	evaluator := readOnlyAgentToolEvaluator()
	mux := agentToolExecutionMuxWithEvaluator(root, store, fake, evaluator)

	rec := postAgentToolExecution(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"arguments":{"bucket":"reports","limit":10}
	}`, "application/json", "execution-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
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
	if fake.calls != 1 || fake.runID != run.ID || fake.request != wantRequest {
		t.Fatalf("Execute calls = %d, run = %q, request = %+v; want one server-scoped call", fake.calls, fake.runID, fake.request)
	}
	if evaluator.calls != 1 || evaluator.serverID != "aws" || evaluator.project != "demo" || evaluator.toolName != "list_buckets" {
		t.Fatalf("EvaluateTool calls = %d, scope = %q/%q/%q; want exact server-owned lookup", evaluator.calls, evaluator.serverID, evaluator.project, evaluator.toolName)
	}
	if !bytes.Equal(fake.arguments.Bytes(), []byte(`{"bucket":"reports","limit":10}`)) {
		t.Fatalf("arguments = %s, want normalized object", fake.arguments.Bytes())
	}

	var response agentrouting.ExecutionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Invoked || response.Result == nil || *response.Result != wantResult {
		t.Fatalf("response = %+v, want validated tool result", response)
	}
	if response.Route.Decision != wantDecision || response.Route.Run.ID != run.ID {
		t.Fatalf("route = %+v, want audited execution route", response.Route)
	}
}

func TestAgentToolExecutionUsesServerResolvedMutationRisk(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	store := agentruns.NewStore()
	run, err := store.Create(agentruns.CreateRequest{
		Project:    "demo",
		Prompt:     "create a reports bucket",
		ProviderID: "codex",
		Mode:       agentruns.ModeApprovedExecute,
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	evaluator := &fakeAgentToolEvaluator{entry: mcpairlock.ToolInventoryEntry{
		ServerID: "aws",
		Name:     "create_bucket",
		Risk:     mcpairlock.RiskCloudMutation,
		Decision: mcpairlock.ToolDecision{Risk: mcpairlock.RiskCloudMutation},
	}}
	fake := &fakeAgentToolExecutor{result: agentrouting.ExecutionResult{
		Route: agentrouting.RouteResult{
			Decision: agentrouting.Decision{
				Status:           agentrouting.DecisionApprovalRequired,
				Reason:           agentrouting.ReasonApprovalRequired,
				ApprovalRequired: true,
				UntrustedOutput:  true,
			},
			Run: run,
		},
	}}
	mux := agentToolExecutionMuxWithEvaluator(root, store, fake, evaluator)

	rec := postAgentToolExecution(
		mux,
		"demo",
		run.ID,
		`{"connection_id":"aws-prod","server_id":"aws","tool_name":"create_bucket","arguments":{"bucket":"reports"}}`,
		"application/json",
		"mutation-execution",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if fake.calls != 1 || fake.request.Risk != mcpairlock.RiskCloudMutation || fake.request.Mode != agentruns.ModeApprovedExecute {
		t.Fatalf("Execute calls = %d, request = %+v; want server-resolved guarded mutation", fake.calls, fake.request)
	}
}

func TestResolveAgentToolExecutionRiskFailsClosed(t *testing.T) {
	request := agentrouting.Request{
		Project:      "demo",
		ProviderID:   "codex",
		ConnectionID: "aws-prod",
		ServerID:     "aws",
		ToolName:     "list_buckets",
		Mode:         agentruns.ModeReadOnly,
		Risk:         mcpairlock.RiskUnknown,
	}
	evaluationError := errors.New("inventory unavailable")
	tests := []struct {
		name      string
		evaluator *fakeAgentToolEvaluator
		wantErr   error
	}{
		{name: "evaluation error", evaluator: &fakeAgentToolEvaluator{err: evaluationError}, wantErr: evaluationError},
		{name: "server mismatch", evaluator: &fakeAgentToolEvaluator{entry: mcpairlock.ToolInventoryEntry{ServerID: "terraform", Name: "list_buckets", Risk: mcpairlock.RiskReadOnly, Decision: mcpairlock.ToolDecision{Risk: mcpairlock.RiskReadOnly}}}, wantErr: agentrouting.ErrInvalidToolExecution},
		{name: "tool mismatch", evaluator: &fakeAgentToolEvaluator{entry: mcpairlock.ToolInventoryEntry{ServerID: "aws", Name: "create_bucket", Risk: mcpairlock.RiskReadOnly, Decision: mcpairlock.ToolDecision{Risk: mcpairlock.RiskReadOnly}}}, wantErr: agentrouting.ErrInvalidToolExecution},
		{name: "risk mismatch", evaluator: &fakeAgentToolEvaluator{entry: mcpairlock.ToolInventoryEntry{ServerID: "aws", Name: "list_buckets", Risk: mcpairlock.RiskReadOnly, Decision: mcpairlock.ToolDecision{Risk: mcpairlock.RiskCloudMutation}}}, wantErr: agentrouting.ErrInvalidToolExecution},
		{name: "invalid risk", evaluator: &fakeAgentToolEvaluator{entry: mcpairlock.ToolInventoryEntry{ServerID: "aws", Name: "list_buckets", Risk: "invalid", Decision: mcpairlock.ToolDecision{Risk: "invalid"}}}, wantErr: agentrouting.ErrInvalidToolExecution},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolveAgentToolExecutionRisk(test.evaluator, request); !errors.Is(err, test.wantErr) {
				t.Fatalf("resolve error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestRouterOptionsMountAgentToolExecutionRoute(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	result := mcpairlock.NewToolCallResult([]byte("reports\n"), false)
	fake := &fakeAgentToolExecutor{result: agentrouting.ExecutionResult{
		Route: agentrouting.RouteResult{
			Decision: agentrouting.Decision{
				Status:          agentrouting.DecisionAllowed,
				Reason:          agentrouting.ReasonAllowed,
				Allowed:         true,
				UntrustedOutput: true,
			},
			Run: run,
		},
		Invoked: true,
		Result:  &result,
	}}
	router := NewRouterWithOptions(nil, nil, nil, nil, root, RouterOptions{
		AgentRuns:          store,
		AgentToolExecutor:  fake,
		AgentToolEvaluator: readOnlyAgentToolEvaluator(),
	})

	rec := postAgentToolExecution(
		router,
		"demo",
		run.ID,
		`{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","arguments":{}}`,
		"application/json",
		"router-option-execution",
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if fake.calls != 1 {
		t.Fatalf("Execute calls = %d, want one", fake.calls)
	}
	if fake.request.Risk != mcpairlock.RiskReadOnly {
		t.Fatalf("risk = %v, want RiskReadOnly; RouterOptions path must not weaken the read-only security boundary", fake.request.Risk)
	}
}

func TestRouterOptionsOmitsExecutionRouteWithoutEvaluator(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	router := NewRouterWithOptions(nil, nil, nil, nil, root, RouterOptions{
		AgentRuns:         store,
		AgentToolExecutor: &fakeAgentToolExecutor{},
	})

	rec := postAgentToolExecution(
		router,
		"demo",
		run.ID,
		`{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","arguments":{}}`,
		"application/json",
		"absent-evaluator",
	)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (route must be absent when no evaluator is configured)", rec.Code, http.StatusNotFound)
	}
}

func TestRouterOptionsOmitsExecutionRouteWithoutExecutor(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	router := NewRouterWithOptions(nil, nil, nil, nil, root, RouterOptions{
		AgentRuns: store,
		// AgentToolExecutor intentionally absent
	})

	rec := postAgentToolExecution(
		router,
		"demo",
		run.ID,
		`{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","arguments":{}}`,
		"application/json",
		"absent-executor",
	)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (route must be absent when no executor is configured)", rec.Code, http.StatusNotFound)
	}
}

func TestAgentToolExecutionRejectsClientScopeAndInactiveRuns(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	fake := &fakeAgentToolExecutor{}
	mux := agentToolExecutionMux(root, store, fake)

	clientScope := postAgentToolExecution(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"arguments":{},
		"risk":"cloud_mutation"
	}`, "application/json", "client-scope")
	if clientScope.Code != http.StatusBadRequest {
		t.Fatalf("client scope status = %d, want %d, body = %s", clientScope.Code, http.StatusBadRequest, clientScope.Body.String())
	}

	if _, err := store.AddApproval(run.ID, agentruns.ApprovalGate{
		Kind:    agentruns.ApprovalMCPNetwork,
		Summary: "approve inventory",
	}); err != nil {
		t.Fatalf("AddApproval(): %v", err)
	}
	inactive := postAgentToolExecution(mux, "demo", run.ID, `{
		"connection_id":"aws-prod",
		"server_id":"aws",
		"tool_name":"list_buckets",
		"arguments":{}
	}`, "application/json", "inactive")
	if inactive.Code != http.StatusConflict {
		t.Fatalf("inactive status = %d, want %d, body = %s", inactive.Code, http.StatusConflict, inactive.Body.String())
	}
	if fake.calls != 0 {
		t.Fatalf("Execute calls = %d, want none for rejected requests", fake.calls)
	}
}

func TestAgentToolExecutionRejectsInvalidRequests(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	fake := &fakeAgentToolExecutor{}
	mux := agentToolExecutionMux(root, store, fake)
	validBody := `{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","arguments":{}}`

	tests := []struct {
		name        string
		body        string
		contentType string
		key         string
		wantStatus  int
	}{
		{name: "missing content type", body: validBody, key: "attempt", wantStatus: http.StatusUnsupportedMediaType},
		{name: "missing idempotency key", body: validBody, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "missing arguments", body: `{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets"}`, contentType: "application/json", key: "attempt", wantStatus: http.StatusBadRequest},
		{name: "scalar arguments", body: `{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","arguments":true}`, contentType: "application/json", key: "attempt", wantStatus: http.StatusBadRequest},
		{name: "multiple values", body: validBody + `{}`, contentType: "application/json", key: "attempt", wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := postAgentToolExecution(mux, "demo", run.ID, test.body, test.contentType, test.key)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.wantStatus, rec.Body.String())
			}
		})
	}
	if fake.calls != 0 {
		t.Fatalf("Execute calls = %d, want none for invalid requests", fake.calls)
	}
}

func TestAgentToolExecutionReplaysWithoutDuplicateInvocation(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	result := mcpairlock.NewToolCallResult([]byte("reports"), false)
	fake := &fakeAgentToolExecutor{result: agentrouting.ExecutionResult{
		Route: agentrouting.RouteResult{
			Decision: agentrouting.Decision{
				Status:          agentrouting.DecisionAllowed,
				Reason:          agentrouting.ReasonAllowed,
				Allowed:         true,
				UntrustedOutput: true,
			},
			Run: run,
		},
		Invoked: true,
		Result:  &result,
	}}
	mux := agentToolExecutionMux(root, store, fake)
	body := `{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","arguments":{"bucket":"reports"}}`

	first := postAgentToolExecution(mux, "demo", run.ID, body, "application/json", "same-execution")
	second := postAgentToolExecution(mux, "demo", run.ID, body, "application/json", "same-execution")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d; want two successful responses", first.Code, second.Code)
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay header = %q, want true", second.Header().Get("Idempotency-Replayed"))
	}
	if fake.calls != 1 {
		t.Fatalf("Execute calls = %d, want one", fake.calls)
	}
	if _, err := store.SetStatus(run.ID, agentruns.StatusCompleted); err != nil {
		t.Fatalf("SetStatus(completed): %v", err)
	}
	terminalReplay := postAgentToolExecution(mux, "demo", run.ID, body, "application/json", "same-execution")
	if terminalReplay.Code != http.StatusOK || terminalReplay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("terminal replay status = %d, replay header = %q, body = %s", terminalReplay.Code, terminalReplay.Header().Get("Idempotency-Replayed"), terminalReplay.Body.String())
	}
	var replayed agentrouting.ExecutionResult
	if err := json.Unmarshal(terminalReplay.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode terminal replay: %v", err)
	}
	if replayed.Route.Run.Status != agentruns.StatusCompleted || fake.calls != 1 {
		t.Fatalf("replayed run status = %q, Execute calls = %d; want current terminal state and one call", replayed.Route.Run.Status, fake.calls)
	}

	conflictBody := `{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","arguments":{"bucket":"audit"}}`
	conflict := postAgentToolExecution(mux, "demo", run.ID, conflictBody, "application/json", "same-execution")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want %d, body = %s", conflict.Code, http.StatusConflict, conflict.Body.String())
	}
	if fake.calls != 1 {
		t.Fatalf("Execute calls = %d, want none after conflicting key reuse", fake.calls)
	}
}

func TestAgentToolExecutionSanitizesExecutorErrors(t *testing.T) {
	root, store, run := agentToolRouteFixture(t, "codex")
	fake := &fakeAgentToolExecutor{err: errors.New("token=executor-secret")}
	mux := agentToolExecutionMux(root, store, fake)

	rec := postAgentToolExecution(
		mux,
		"demo",
		run.ID,
		`{"connection_id":"aws-prod","server_id":"aws","tool_name":"list_buckets","arguments":{}}`,
		"application/json",
		"failing-execution",
	)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "executor-secret") {
		t.Fatalf("response leaked executor error: %s", rec.Body.String())
	}
}

func TestAgentToolExecutionDistinguishesCancellationAndTimeout(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "request canceled",
			err:        context.Canceled,
			wantStatus: http.StatusRequestTimeout,
			wantBody:   "tool execution request canceled\n",
		},
		{
			name:       "deadline exceeded",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusGatewayTimeout,
			wantBody:   "tool execution request timed out\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeAgentToolExecutionError(rec, test.err)

			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, test.wantStatus)
			}
			if rec.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), test.wantBody)
			}
		})
	}
}

func TestAgentToolExecutionRunRecheckReturnsNotFoundAfterEviction(t *testing.T) {
	store := agentruns.NewStore(agentruns.WithMaxRuns(1))
	first, err := store.Create(agentruns.CreateRequest{
		Project:    "demo",
		Prompt:     "inventory the project",
		ProviderID: "codex",
		Mode:       agentruns.ModeReadOnly,
	})
	if err != nil {
		t.Fatalf("Create(first): %v", err)
	}
	if _, err := store.Create(agentruns.CreateRequest{
		Project:    "demo",
		Prompt:     "inventory another project",
		ProviderID: "codex",
		Mode:       agentruns.ModeReadOnly,
	}); err != nil {
		t.Fatalf("Create(second): %v", err)
	}

	err = requireActiveAgentToolExecutionRun(store, first.ID)
	if !errors.Is(err, agentruns.ErrNotFound) {
		t.Fatalf("run recheck error = %v, want %v", err, agentruns.ErrNotFound)
	}
	rec := httptest.NewRecorder()
	writeAgentToolExecutionError(rec, err)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
