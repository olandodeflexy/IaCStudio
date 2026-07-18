package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/agentruns"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
)

type failingAgentToolPolicyStore struct {
	err error
}

func (r failingAgentToolPolicyStore) Get(agentrouting.PolicyScope) (agentrouting.Policy, error) {
	return agentrouting.Policy{}, r.err
}

func (r failingAgentToolPolicyStore) Save(agentrouting.PolicyScope, agentrouting.Policy) error {
	return r.err
}

func agentToolPolicyMux(root string, policies AgentToolPolicyStore) *http.ServeMux {
	mux := http.NewServeMux()
	registerAgentToolPolicyRoutes(mux, root, policies)
	return mux
}

func testAgentToolPolicy() (agentrouting.PolicyScope, agentrouting.Policy) {
	scope := agentrouting.PolicyScope{Project: "demo", ProviderID: "codex"}
	return scope, agentrouting.Policy{Rules: []agentrouting.Rule{{
		Project:      scope.Project,
		ProviderID:   scope.ProviderID,
		ConnectionID: "aws-prod",
		ServerID:     "aws",
		ToolName:     "list_buckets",
		Modes:        []agentruns.Mode{agentruns.ModeReadOnly},
		Risk:         mcpairlock.RiskReadOnly,
		Effect:       agentrouting.EffectAllow,
	}}}
}

func TestAgentToolPolicyRouteReturnsExactScopedPolicy(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	scope, policy := testAgentToolPolicy()
	store := agentrouting.NewPolicyStore()
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects/demo/agent-routing/policies/codex", nil)
	rec := httptest.NewRecorder()
	agentToolPolicyMux(root, store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	requireJSONResponse(t, rec)
	var response agentToolPolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Scope != scope || len(response.Policy.Rules) != 1 || response.Policy.Rules[0].ToolName != "list_buckets" {
		t.Fatalf("response = %+v, want exact scoped policy", response)
	}
}

func TestAgentToolPolicyRouteDoesNotFallBackAcrossScopes(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	scope, policy := testAgentToolPolicy()
	store := agentrouting.NewPolicyStore()
	if err := store.Save(scope, policy); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	for _, path := range []string{
		"/api/projects/other/agent-routing/policies/codex",
		"/api/projects/demo/agent-routing/policies/claude",
	} {
		rec := httptest.NewRecorder()
		agentToolPolicyMux(root, store).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d, body = %s", path, rec.Code, http.StatusNotFound, rec.Body.String())
		}
	}
}

func TestAgentToolPolicyRouteRejectsInvalidScopeAndSanitizesErrors(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	tests := []struct {
		name   string
		path   string
		reader AgentToolPolicyStore
		status int
	}{
		{name: "missing project", path: "/api/projects/missing/agent-routing/policies/codex", reader: agentrouting.NewPolicyStore(), status: http.StatusNotFound},
		{name: "invalid provider", path: "/api/projects/demo/agent-routing/policies/bad.provider", reader: agentrouting.NewPolicyStore(), status: http.StatusBadRequest},
		{name: "long provider", path: "/api/projects/demo/agent-routing/policies/" + strings.Repeat("x", maxAgentToolPolicyProviderIDLength+1), reader: agentrouting.NewPolicyStore(), status: http.StatusBadRequest},
		{name: "reader failure", path: "/api/projects/demo/agent-routing/policies/codex", reader: failingAgentToolPolicyStore{err: errors.New("token=policy-secret")}, status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			agentToolPolicyMux(root, test.reader).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, test.path, nil))
			if rec.Code != test.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.status, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "policy-secret") {
				t.Fatalf("response leaked reader error: %s", rec.Body.String())
			}
		})
	}
}

func TestAgentToolPolicyRouteSavesExactScopedPolicy(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	scope, policy := testAgentToolPolicy()
	store, err := agentrouting.NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(): %v", err)
	}
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/projects/demo/agent-routing/policies/codex", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	agentToolPolicyMux(root, store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	requireJSONResponse(t, rec)
	var response agentToolPolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Scope != scope || len(response.Policy.Rules) != 1 || response.Policy.Rules[0].ToolName != "list_buckets" {
		t.Fatalf("response = %+v, want saved exact scoped policy", response)
	}

	restarted, err := agentrouting.NewPersistentPolicyStore(root)
	if err != nil {
		t.Fatalf("NewPersistentPolicyStore(restart): %v", err)
	}
	stored, err := restarted.Get(scope)
	if err != nil {
		t.Fatalf("Get(restarted): %v", err)
	}
	if len(stored.Rules) != 1 || stored.Rules[0].ToolName != "list_buckets" {
		t.Fatalf("restarted policy = %+v, want saved policy", stored)
	}
}

func TestAgentToolPolicyRouteRejectsInvalidWrites(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	_, policy := testAgentToolPolicy()
	crossScoped := policy
	crossScoped.Rules = append([]agentrouting.Rule(nil), policy.Rules...)
	crossScoped.Rules[0].Project = "other"
	crossScopedBody, err := json.Marshal(crossScoped)
	if err != nil {
		t.Fatalf("Marshal(crossScoped): %v", err)
	}

	tests := []struct {
		name        string
		body        string
		contentType string
		status      int
	}{
		{name: "cross-scoped rule", body: string(crossScopedBody), contentType: "application/json", status: http.StatusBadRequest},
		{name: "unknown field", body: `{"rules":[],"scope":{"project":"other"}}`, contentType: "application/json", status: http.StatusBadRequest},
		{name: "trailing value", body: `{"rules":[]} {}`, contentType: "application/json", status: http.StatusBadRequest},
		{name: "missing rules", body: `{}`, contentType: "application/json", status: http.StatusBadRequest},
		{name: "null rules", body: `{"rules":null}`, contentType: "application/json", status: http.StatusBadRequest},
		{name: "null policy", body: `null`, contentType: "application/json", status: http.StatusBadRequest},
		{name: "wrong content type", body: `{"rules":[]}`, contentType: "text/plain", status: http.StatusUnsupportedMediaType},
		{name: "oversized", body: `{"rules":[]}` + strings.Repeat(" ", maxRequestBody), contentType: "application/json", status: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := agentrouting.NewPolicyStore()
			req := httptest.NewRequest(http.MethodPut, "/api/projects/demo/agent-routing/policies/codex", strings.NewReader(test.body))
			req.Header.Set("Content-Type", test.contentType)
			rec := httptest.NewRecorder()
			agentToolPolicyMux(root, store).ServeHTTP(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.status, rec.Body.String())
			}
			if _, err := store.Get(agentrouting.PolicyScope{Project: "demo", ProviderID: "codex"}); !errors.Is(err, agentrouting.ErrPolicyNotFound) {
				t.Fatalf("Get() error = %v, want ErrPolicyNotFound", err)
			}
		})
	}
}

func TestAgentToolPolicyRouteSanitizesSaveErrors(t *testing.T) {
	root := scaffoldAgentRunProject(t)
	_, policy := testAgentToolPolicy()
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/projects/demo/agent-routing/policies/codex", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	agentToolPolicyMux(root, failingAgentToolPolicyStore{err: errors.New("token=policy-secret")}).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "policy-secret") {
		t.Fatalf("response leaked save error: %s", rec.Body.String())
	}
}

func TestAgentToolPolicyRouteSanitizesProjectFilesystemErrors(t *testing.T) {
	root := t.TempDir()
	missingTarget := filepath.Join(root, "private-policy-target")
	if err := os.Symlink(missingTarget, filepath.Join(root, "demo")); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}

	rec := httptest.NewRecorder()
	agentToolPolicyMux(root, agentrouting.NewPolicyStore()).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/projects/demo/agent-routing/policies/codex", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), root) || strings.Contains(rec.Body.String(), missingTarget) {
		t.Fatalf("response leaked filesystem path: %s", rec.Body.String())
	}
}
