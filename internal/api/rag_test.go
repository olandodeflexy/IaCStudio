package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/ai/rag"
)

// ragMux wires just the RAG routes so tests don't spin up the full
// router. safeProjectPath is shared so we still exercise the project-
// scoping logic.
func ragMux(t *testing.T, projectsDir string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	client := ai.NewClient("http://127.0.0.1:1", "ignored")
	registerRAGRoutes(mux, projectsDir, client)
	return mux
}

func scaffoldRAGProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.tf"),
		[]byte(`resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`),
		0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRAGStatsEndpoint_EmptyProjectReturnsNotPresent(t *testing.T) {
	root := scaffoldRAGProject(t)
	srv := httptest.NewServer(ragMux(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/demo/ai/index")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var stats rag.Stats
	_ = json.NewDecoder(resp.Body).Decode(&stats)
	if stats.Present {
		t.Errorf("expected !Present for unindexed project, got %+v", stats)
	}
}

func TestRAGStatsEndpoint_PathTraversal(t *testing.T) {
	root := scaffoldRAGProject(t)
	srv := httptest.NewServer(ragMux(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/%2e%2e%2fetc/ai/index")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 400 {
		t.Errorf("traversal should 400, got %d", resp.StatusCode)
	}
}

func TestRAGBuildEndpoint_OllamaUnavailableReturns502WithPartial(t *testing.T) {
	root := scaffoldRAGProject(t)
	srv := httptest.NewServer(ragMux(t, root))
	defer srv.Close()

	// Point the request at a dead endpoint; Build degrades to chunk-only.
	body := strings.NewReader(`{"endpoint":"http://127.0.0.1:1","model":"nomic-embed-text","dim":768}`)
	resp, err := http.Post(srv.URL+"/api/projects/demo/ai/index", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadGateway {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var payload struct {
		Error          string    `json:"error"`
		PartialChunks  int       `json:"partial_chunks"`
		Stats          rag.Stats `json:"stats"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	if payload.Error == "" {
		t.Error("expected error field")
	}
	if payload.PartialChunks == 0 {
		t.Error("expected partial_chunks > 0 — chunks should be counted even when embedding fails")
	}
}
