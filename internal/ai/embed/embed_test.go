package embed

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCosine(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1},
		{"different lengths", []float32{1, 0}, []float32{1, 0, 0}, 0},
		{"zero a", []float32{0, 0, 0}, []float32{1, 0, 0}, 0},
		{"zero b", []float32{1, 0, 0}, []float32{0, 0, 0}, 0},
		{"empty", nil, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Cosine(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("Cosine = %v, want %v", got, tc.want)
			}
		})
	}
}

// Ollama embed endpoint tests — an httptest server stubs /api/embed and
// asserts we marshal / unmarshal the wire format correctly.

func TestOllamaEmbed_HappyPath(t *testing.T) {
	var received ollamaEmbedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&received)
		resp := ollamaEmbedResponse{Embeddings: [][]float32{
			{0.1, 0.2, 0.3},
			{0.4, 0.5, 0.6},
		}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e := NewOllama(Config{Endpoint: srv.URL, Model: "nomic-embed-text", Dim: 3})
	vecs, err := e.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("want 2 vecs, got %d", len(vecs))
	}
	if vecs[0][0] != 0.1 || vecs[1][2] != 0.6 {
		t.Errorf("unexpected vectors: %+v", vecs)
	}
	if received.Model != "nomic-embed-text" || len(received.Input) != 2 {
		t.Errorf("unexpected request: %+v", received)
	}
}

func TestOllamaEmbed_EmptyInputsZeroVectors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server should only see the non-empty inputs.
		var req ollamaEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Input) != 1 || req.Input[0] != "real" {
			t.Errorf("expected single non-empty input, got %+v", req.Input)
		}
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: [][]float32{{1, 2, 3}}})
	}))
	defer srv.Close()

	e := NewOllama(Config{Endpoint: srv.URL, Model: "m", Dim: 3})
	vecs, err := e.Embed(context.Background(), []string{"", "real", "   "})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("want 3 vecs, got %d", len(vecs))
	}
	// First and third are empties — zero vectors preserved.
	for _, i := range []int{0, 2} {
		for _, v := range vecs[i] {
			if v != 0 {
				t.Errorf("vec %d should be zero, got %v", i, vecs[i])
			}
		}
	}
	if vecs[1][2] != 3 {
		t.Errorf("real input not at original index 1: %+v", vecs[1])
	}
}

func TestOllamaEmbed_ConnectionRefusedIsNotReady(t *testing.T) {
	// Close the server before Embed so Dial fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	e := NewOllama(Config{Endpoint: srv.URL, Model: "m", Dim: 3})
	_, err := e.Embed(context.Background(), []string{"x"})
	if !errors.Is(err, ErrNotReady) {
		t.Errorf("want ErrNotReady, got %v", err)
	}
}

func TestOllamaEmbed_DimMismatchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: [][]float32{{1, 2}}})
	}))
	defer srv.Close()

	e := NewOllama(Config{Endpoint: srv.URL, Model: "m", Dim: 3})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Error("expected dim-mismatch error")
	}
}

func TestOllamaEmbed_ErrorFieldSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Error: "model not found: foo"})
	}))
	defer srv.Close()

	e := NewOllama(Config{Endpoint: srv.URL, Model: "foo", Dim: 3})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil || !errContains(err, "model not found") {
		t.Errorf("want model-not-found error, got %v", err)
	}
}

func errContains(err error, needle string) bool {
	return err != nil && strings.Contains(err.Error(), needle)
}
