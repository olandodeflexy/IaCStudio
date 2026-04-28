package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/runner"
	"github.com/iac-studio/iac-studio/internal/watcher"
)

func fullRouterForTest(t *testing.T, projectsDir string) *http.ServeMux {
	t.Helper()
	hub := NewHub()
	go hub.Run()
	fw := watcher.New(hub)
	t.Cleanup(fw.Close)
	return NewRouter(
		hub,
		fw,
		ai.NewClient("http://127.0.0.1:1", "ignored"),
		runner.NewSafeRunner(runner.DefaultSafetyConfig()),
		projectsDir,
	)
}

func TestStateRoutesRejectTraversalProjectName(t *testing.T) {
	root := t.TempDir()
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/%2e%2e%2foutside/state")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal state load should 400, got %d", resp.StatusCode)
	}
}

func TestSyncRejectsResourceFileOutsideProject(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	escape := filepath.Join(root, "..", "outside.tf")
	body := `{"resources":[{"id":"aws_vpc.main","type":"aws_vpc","name":"main","file":` +
		`"` + strings.ReplaceAll(escape, `\`, `\\`) + `",` +
		`"properties":{"cidr_block":"10.0.0.0/16"}}]}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=terraform",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("sync with escaping file should 400, got %d", resp.StatusCode)
	}
}
