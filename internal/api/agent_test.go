package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/ai"
	iacregistry "github.com/iac-studio/iac-studio/internal/registry"
)

// agentMux wires just the agent routes — keeps endpoint tests hermetic
// without spinning up the full NewRouter stack.
func agentMux(projectsDir string, client *ai.Client, reg *iacregistry.Client) *http.ServeMux {
	mux := http.NewServeMux()
	registerAgentRoutes(mux, projectsDir, client, reg)
	return mux
}

// scaffoldAgentProject — one project dir with one main.tf, enough for the
// path-safety + tool-registry wiring to exercise.
func scaffoldAgentProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(proj, "main.tf"),
		[]byte(`resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`), 0o644)
	return root
}

// TestAgentRunBadRequestsReject400 — missing prompt, bad JSON, and
// traversal attempts must all surface as 400 before any provider call.
func TestAgentRunBadRequestsReject400(t *testing.T) {
	root := scaffoldAgentProject(t)
	client := ai.NewClient("http://127.0.0.1:1", "ignored") // Ollama default; no tool use
	srv := httptest.NewServer(agentMux(root, client, iacregistry.New(iacregistry.Config{})))
	defer srv.Close()

	cases := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{"malformed JSON", "/api/projects/demo/ai/agent", "{not json", 400},
		{"missing prompt", "/api/projects/demo/ai/agent", `{}`, 400},
		{"path traversal", "/api/projects/%2e%2e%2fetc/ai/agent", `{"prompt":"x"}`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+tc.path, "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.status)
			}
		})
	}
}

// TestAgentRunNonToolUseProviderReturns400 — default Ollama provider
// doesn't implement ToolUser. The agent endpoint must return 400 with a
// clear "switch to Anthropic" message rather than 500.
func TestAgentRunNonToolUseProviderReturns400(t *testing.T) {
	root := scaffoldAgentProject(t)
	client := ai.NewClient("http://127.0.0.1:1", "ignored") // Ollama
	srv := httptest.NewServer(agentMux(root, client, iacregistry.New(iacregistry.Config{})))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"prompt": "add a vpc"})
	resp, err := http.Post(srv.URL+"/api/projects/demo/ai/agent", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-tool-use provider should 400, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "tool use") {
		t.Errorf("error body should explain tool-use requirement: %q", string(raw))
	}
}

// TestAgentAuditRecordsFailedRuns — even when the agent run fails (non-
// tool-use provider, here), the audit log should still carry an entry so
// operators can see the attempt.
func TestAgentAuditRecordsFailedRuns(t *testing.T) {
	root := scaffoldAgentProject(t)
	client := ai.NewClient("http://127.0.0.1:1", "ignored")
	srv := httptest.NewServer(agentMux(root, client, iacregistry.New(iacregistry.Config{})))
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"prompt": "whatever"})
	_, _ = http.Post(srv.URL+"/api/projects/demo/ai/agent", "application/json", bytes.NewReader(body))

	resp, err := http.Get(srv.URL + "/api/ai/agent/audit")
	if err != nil {
		t.Fatalf("audit GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var audit struct {
		Entries []auditEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&audit); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(audit.Entries) != 1 {
		t.Fatalf("want 1 audit entry, got %d", len(audit.Entries))
	}
	got := audit.Entries[0]
	if got.Project != "demo" || got.Prompt != "whatever" {
		t.Errorf("audit entry wrong: %+v", got)
	}
	if got.Err == "" {
		t.Error("failed run must carry an Err field")
	}
	if got.Specialist != "" {
		t.Errorf("failed run must NOT carry a specialist (never reached the loop): %q", got.Specialist)
	}
}

// TestIsClientErrorMatchesProviderMessages — the 400-vs-502 heuristic
// needs to catch the exact error strings agent.Run returns.
func TestIsClientErrorMatchesProviderMessages(t *testing.T) {
	cases := map[string]bool{
		"provider \"ollama\" does not support tool use — switch to Anthropic": true,
		"agent: no provider configured":  true,
		"agent: no tool registry configured": true,
		"anthropic tool-loop (status 502)":   false,
		"decode anthropic response: EOF":     false,
	}
	for msg, want := range cases {
		got := isClientError(stringErr(msg))
		if got != want {
			t.Errorf("isClientError(%q) = %v, want %v", msg, got, want)
		}
	}
}

type stringErr string

func (s stringErr) Error() string { return string(s) }
