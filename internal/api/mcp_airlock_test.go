package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
