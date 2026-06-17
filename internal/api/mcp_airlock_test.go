package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/mcpairlock"
	"github.com/iac-studio/iac-studio/internal/runner"
	"github.com/iac-studio/iac-studio/internal/watcher"
)

func TestMCPAirlockRoutesExposeTrustedReadOnlyServers(t *testing.T) {
	srv := httptest.NewServer(fullRouterForTest(t, t.TempDir()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/mcp-airlock/servers")
	if err != nil {
		t.Fatalf("GET servers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var statuses []struct {
		Server struct {
			ID              string `json:"id"`
			Trusted         bool   `json:"trusted"`
			ReadOnlyDefault bool   `json:"read_only_default"`
			CredentialMode  string `json:"credential_mode"`
		} `json:"server"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		t.Fatalf("decode servers: %v", err)
	}
	if len(statuses) < 2 {
		t.Fatalf("expected built-in Airlock servers, got %+v", statuses)
	}
	for _, status := range statuses {
		if !status.Server.Trusted || !status.Server.ReadOnlyDefault || status.Server.CredentialMode != "none" {
			t.Fatalf("server is not safe by default: %+v", status.Server)
		}
	}
}

func TestMCPAirlockHealthUnknownServerFailsClosed(t *testing.T) {
	srv := httptest.NewServer(fullRouterForTest(t, t.TempDir()))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/mcp-airlock/servers/unknown/health", "application/json", nil)
	if err != nil {
		t.Fatalf("POST health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMCPAirlockStartStopRoutesUseLifecycle(t *testing.T) {
	root := t.TempDir()
	handle := newAPIFakeProcess()
	manager := mcpairlock.NewManager(root,
		mcpairlock.WithDefinitions([]mcpairlock.ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         apiTestExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		mcpairlock.WithLauncher(func(context.Context, mcpairlock.ServerDefinition, time.Duration) (mcpairlock.ProcessHandle, error) {
			return handle, nil
		}),
	)
	srv := httptest.NewServer(fullRouterForTestWithAirlock(t, root, manager))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/mcp-airlock/servers/terraform/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST start: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var started struct {
		Running bool   `json:"running"`
		State   string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if !started.Running || started.State != "running" {
		t.Fatalf("expected running response, got %+v", started)
	}

	resp, err = http.Post(srv.URL+"/api/mcp-airlock/servers/terraform/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST stop: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var stopped struct {
		Running bool   `json:"running"`
		State   string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stopped); err != nil {
		t.Fatalf("decode stop: %v", err)
	}
	if stopped.Running || stopped.State != "stopped" || !handle.stopped {
		t.Fatalf("expected stopped response, got response=%+v stopped=%v", stopped, handle.stopped)
	}
}

func TestMCPAirlockToolRoutesDiscoverAndEvaluateFirewall(t *testing.T) {
	root := t.TempDir()
	manager := mcpairlock.NewManager(root,
		mcpairlock.WithDefinitions([]mcpairlock.ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         apiTestExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
		mcpairlock.WithToolDiscoverer(func(context.Context, mcpairlock.ServerDefinition, time.Duration) mcpairlock.DiscoveryProbeResult {
			return mcpairlock.DiscoveryProbeResult{Tools: []mcpairlock.DiscoveredTool{
				{
					Name:        "list_modules",
					Description: "List Terraform registry modules.",
					InputSchema: map[string]any{"type": "object"},
					Annotations: map[string]any{"readOnlyHint": true},
				},
				{
					Name:        "apply_workspace",
					Description: "Apply a Terraform workspace.",
					InputSchema: map[string]any{"type": "object"},
				},
			}}
		}),
	)
	srv := httptest.NewServer(fullRouterForTestWithAirlock(t, root, manager))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/mcp-airlock/servers/terraform/tools/discover", "application/json", nil)
	if err != nil {
		t.Fatalf("POST discover: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var inventory struct {
		Tools []struct {
			Name     string `json:"name"`
			Risk     string `json:"risk"`
			Decision struct {
				Status  string `json:"status"`
				Allowed bool   `json:"allowed"`
			} `json:"decision"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&inventory); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	if len(inventory.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %+v", inventory)
	}

	resp, err = http.Post(
		srv.URL+"/api/mcp-airlock/servers/terraform/tools/evaluate",
		"application/json",
		strings.NewReader(`{"tool_name":"apply_workspace"}`),
	)
	if err != nil {
		t.Fatalf("POST evaluate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var evaluated struct {
		Risk     string `json:"risk"`
		Decision struct {
			Status string `json:"status"`
		} `json:"decision"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&evaluated); err != nil {
		t.Fatalf("decode evaluation: %v", err)
	}
	if evaluated.Risk != "cloud_mutation" || evaluated.Decision.Status != "blocked" {
		t.Fatalf("expected blocked cloud mutation, got %+v", evaluated)
	}

	resp, err = http.Post(
		srv.URL+"/api/mcp-airlock/servers/terraform/tools/allowlist",
		"application/json",
		strings.NewReader(`{"tool_name":"apply_workspace","project":"demo","allowed":true}`),
	)
	if err != nil {
		t.Fatalf("POST allowlist: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var allowed struct {
		Decision struct {
			Status           string `json:"status"`
			Allowlisted      bool   `json:"allowlisted"`
			ApprovalRequired bool   `json:"approval_required"`
		} `json:"decision"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&allowed); err != nil {
		t.Fatalf("decode allowlist response: %v", err)
	}
	if allowed.Decision.Status != "approval_required" || !allowed.Decision.Allowlisted || !allowed.Decision.ApprovalRequired {
		t.Fatalf("expected allowlisted mutation to require approval, got %+v", allowed)
	}
}

func TestMCPAirlockToolRoutesReturnArrayJSONForEmptyInventory(t *testing.T) {
	root := t.TempDir()
	manager := mcpairlock.NewManager(root,
		mcpairlock.WithDefinitions([]mcpairlock.ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         apiTestExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
	)
	srv := httptest.NewServer(fullRouterForTestWithAirlock(t, root, manager))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/mcp-airlock/servers/terraform/tools")
	if err != nil {
		t.Fatalf("GET inventory: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload struct {
		Tools  []any `json:"tools"`
		Checks []any `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	if payload.Tools == nil {
		t.Fatal("expected tools to decode from [] rather than null")
	}
	if payload.Checks == nil {
		t.Fatal("expected checks to decode from [] rather than null")
	}
}

func TestMCPAirlockToolRoutesSanitizeInternalErrors(t *testing.T) {
	root := t.TempDir()
	manager := mcpairlock.NewManager(root,
		mcpairlock.WithDefinitions([]mcpairlock.ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         apiTestExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
	)
	if err := os.MkdirAll(filepath.Join(root, ".iac-studio"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".iac-studio", "mcp-airlock-tools.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := httptest.NewServer(fullRouterForTestWithAirlock(t, root, manager))
	defer srv.Close()

	resp, err := http.Post(
		srv.URL+"/api/mcp-airlock/servers/terraform/tools/evaluate",
		"application/json",
		strings.NewReader(`{"tool_name":"list_modules"}`),
	)
	if err != nil {
		t.Fatalf("POST evaluate: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("ReadAll evaluate body: %v", readErr)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	if got := string(body); !strings.Contains(got, "mcp airlock tool evaluation failed") || strings.Contains(got, ".iac-studio") {
		t.Fatalf("expected sanitized evaluate error, got %q", got)
	}

	resp, err = http.Post(
		srv.URL+"/api/mcp-airlock/servers/terraform/tools/allowlist",
		"application/json",
		strings.NewReader(`{"tool_name":"list_modules","allowed":true}`),
	)
	if err != nil {
		t.Fatalf("POST allowlist: %v", err)
	}
	body, readErr = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("ReadAll allowlist body: %v", readErr)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	if got := string(body); !strings.Contains(got, "mcp airlock allowlist update failed") || strings.Contains(got, ".iac-studio") {
		t.Fatalf("expected sanitized allowlist error, got %q", got)
	}
}

func TestMCPAirlockToolRoutesValidateToolName(t *testing.T) {
	root := t.TempDir()
	manager := mcpairlock.NewManager(root,
		mcpairlock.WithDefinitions([]mcpairlock.ServerDefinition{{
			ID:              "terraform",
			Name:            "Terraform",
			Command:         apiTestExecutable(t),
			Transport:       "stdio",
			Trusted:         true,
			ReadOnlyDefault: true,
			CredentialMode:  "none",
		}}),
	)
	srv := httptest.NewServer(fullRouterForTestWithAirlock(t, root, manager))
	defer srv.Close()

	for _, path := range []string{
		"/api/mcp-airlock/servers/terraform/tools/evaluate",
		"/api/mcp-airlock/servers/terraform/tools/allowlist",
	} {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(`{"tool_name":"   ","allowed":true}`))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatalf("ReadAll %s body: %v", path, readErr)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d", path, resp.StatusCode)
		}
		if got := string(body); !strings.Contains(got, "tool_name is required") {
			t.Fatalf("expected validation error for %s, got %q", path, got)
		}
	}
}

func fullRouterForTestWithAirlock(t *testing.T, projectsDir string, airlock *mcpairlock.Manager) *http.ServeMux {
	t.Helper()
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Close)
	fw := watcher.New(hub)
	t.Cleanup(fw.Close)
	return NewRouterWithOptions(
		hub,
		fw,
		ai.NewClient("http://127.0.0.1:1", "ignored"),
		runner.NewSafeRunner(runner.DefaultSafetyConfig()),
		projectsDir,
		RouterOptions{MCPAirlock: airlock},
	)
}

func apiTestExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

type apiFakeProcess struct {
	done    chan error
	stopped bool
}

func newAPIFakeProcess() *apiFakeProcess {
	return &apiFakeProcess{done: make(chan error, 1)}
}

func (p *apiFakeProcess) Done() <-chan error {
	return p.done
}

func (p *apiFakeProcess) Stop(context.Context) error {
	p.stopped = true
	select {
	case p.done <- nil:
	default:
	}
	return nil
}
