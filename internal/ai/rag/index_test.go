package rag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/iac-studio/iac-studio/internal/ai/embed"
)

// stubEmbedServer returns a fake Ollama that replies with deterministic
// vectors so the tests can assert on persistence + loading without
// rolling real floats through the pipeline.
func stubEmbedServer(t *testing.T, dim int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input []string `json:"input"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := make([][]float32, len(req.Input))
		for i := range req.Input {
			v := make([]float32, dim)
			v[i%dim] = 1
			out[i] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
}

func TestBuildSaveLoad_RoundTrip(t *testing.T) {
	dir := scaffoldProject(t)
	srv := stubEmbedServer(t, 4)
	defer srv.Close()
	em := embed.NewOllama(embed.Config{Endpoint: srv.URL, Model: "test-embed", Dim: 4})

	idx, err := Build(context.Background(), dir, em, BuildOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if idx.Dim != 4 || idx.Model != "test-embed" {
		t.Errorf("wrong metadata: dim=%d model=%q", idx.Dim, idx.Model)
	}
	if len(idx.Chunks) == 0 || len(idx.Vectors) != len(idx.Chunks) {
		t.Fatalf("chunk/vector mismatch: %d chunks, %d vectors", len(idx.Chunks), len(idx.Vectors))
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil || len(loaded.Chunks) != len(idx.Chunks) {
		t.Fatalf("Load returned %v", loaded)
	}
	if loaded.Vectors[0][0] != idx.Vectors[0][0] {
		t.Errorf("vectors did not round-trip")
	}
}

func TestLoad_MissingIndexIsNilNil(t *testing.T) {
	dir := t.TempDir()
	idx, err := Load(dir)
	if err != nil || idx != nil {
		t.Errorf("Load(empty) = %v, %v — want nil, nil", idx, err)
	}
}

func TestBuild_EmbedderNotReadyDegradesGracefully(t *testing.T) {
	dir := scaffoldProject(t)
	srv := stubEmbedServer(t, 4)
	srv.Close() // kill the server before Build dials it

	em := embed.NewOllama(embed.Config{Endpoint: srv.URL, Model: "x", Dim: 4})
	idx, err := Build(context.Background(), dir, em, BuildOptions{})
	if !errors.Is(err, embed.ErrNotReady) {
		t.Fatalf("want ErrNotReady, got %v", err)
	}
	if idx == nil || len(idx.Chunks) == 0 {
		t.Error("want partial index with chunks populated, got nil/empty")
	}
}

func TestStatsFor_ReflectsIndex(t *testing.T) {
	dir := scaffoldProject(t)
	srv := stubEmbedServer(t, 4)
	defer srv.Close()
	em := embed.NewOllama(embed.Config{Endpoint: srv.URL, Model: "m", Dim: 4})

	stats, _ := StatsFor(dir)
	if stats.Present {
		t.Error("stats should not be present before build")
	}

	if _, err := Build(context.Background(), dir, em, BuildOptions{}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	stats, err := StatsFor(dir)
	if err != nil {
		t.Fatalf("StatsFor: %v", err)
	}
	if !stats.Present || stats.ChunkCount == 0 || stats.Dim != 4 {
		t.Errorf("unexpected stats: %+v", stats)
	}
	if _, err := os.Stat(filepath.Join(dir, indexDirName, indexFileName)); err != nil {
		t.Errorf("index file not written: %v", err)
	}
}
