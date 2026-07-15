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

type failingAgentToolPolicyReader struct {
	err error
}

func (r failingAgentToolPolicyReader) Get(agentrouting.PolicyScope) (agentrouting.Policy, error) {
	return agentrouting.Policy{}, r.err
}

func agentToolPolicyMux(root string, policies AgentToolPolicyReader) *http.ServeMux {
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
		reader AgentToolPolicyReader
		status int
	}{
		{name: "missing project", path: "/api/projects/missing/agent-routing/policies/codex", reader: agentrouting.NewPolicyStore(), status: http.StatusNotFound},
		{name: "invalid provider", path: "/api/projects/demo/agent-routing/policies/bad.provider", reader: agentrouting.NewPolicyStore(), status: http.StatusBadRequest},
		{name: "long provider", path: "/api/projects/demo/agent-routing/policies/" + strings.Repeat("x", maxAgentToolPolicyProviderIDLength+1), reader: agentrouting.NewPolicyStore(), status: http.StatusBadRequest},
		{name: "reader failure", path: "/api/projects/demo/agent-routing/policies/codex", reader: failingAgentToolPolicyReader{err: errors.New("token=policy-secret")}, status: http.StatusInternalServerError},
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
