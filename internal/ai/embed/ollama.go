package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ollamaEmbedder talks to a local Ollama server's /api/embed endpoint.
// Ollama accepts a batch in the `input` field and returns one vector
// per input, which matches our Embedder contract directly — no
// per-chunk round-trip.
type ollamaEmbedder struct {
	endpoint string
	model    string
	dim      int
	client   *http.Client
}

// Config bundles the wire params. Timeout defaults to 60s because
// embedding a batch of 256 chunks on CPU can easily take 20-30 seconds
// on modest hardware.
type Config struct {
	Endpoint string
	Model    string
	// Dim is the vector width the configured model produces.
	// nomic-embed-text = 768, mxbai-embed-large = 1024, bge-m3 = 1024.
	// Set explicitly so the index can pre-validate; we don't probe the
	// server on startup because that would fail fast in the common
	// "Ollama not running yet" dev path.
	Dim     int
	Timeout time.Duration
}

// NewOllama constructs an Embedder that speaks to an Ollama instance.
// Returns a concrete type (not an interface) so callers holding the
// pointer can introspect; the package's Embedder interface is the
// contract for everything else.
func NewOllama(cfg Config) Embedder {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Dim == 0 {
		// Default to nomic-embed-text's width so a caller that forgot to
		// set Dim still gets something sane. If the actual model produces
		// a different width, Embed will surface the mismatch at runtime.
		cfg.Dim = 768
	}
	return &ollamaEmbedder{
		endpoint: strings.TrimSuffix(cfg.Endpoint, "/"),
		model:    cfg.Model,
		dim:      cfg.Dim,
		client:   &http.Client{Timeout: cfg.Timeout},
	}
}

func (e *ollamaEmbedder) Dim() int      { return e.dim }
func (e *ollamaEmbedder) Model() string { return e.model }

// ollamaEmbedRequest matches Ollama's /api/embed request shape.
// `input` is either a string or []string — we always send the slice
// form because the receiving code is identical for N=1 and spares us
// a shape branch here.
type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	// Ollama surfaces errors in a sibling `error` field when the model
	// is missing or the request is malformed.
	Error string `json:"error,omitempty"`
}

func (e *ollamaEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	// Partition into embeddable vs. empty — empties get zero vectors to
	// keep the caller's slice alignment; the retriever treats zero
	// vectors as non-matches.
	nonEmptyIdx := make([]int, 0, len(inputs))
	nonEmpty := make([]string, 0, len(inputs))
	for i, s := range inputs {
		if strings.TrimSpace(s) == "" {
			continue
		}
		nonEmptyIdx = append(nonEmptyIdx, i)
		nonEmpty = append(nonEmpty, s)
	}

	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = make([]float32, e.dim)
	}
	if len(nonEmpty) == 0 {
		return out, nil
	}

	body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Input: nonEmpty})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		// Connection refused, DNS miss, etc. — surface as ErrNotReady so
		// the indexer knows to degrade gracefully rather than erroring
		// the whole index run.
		return nil, fmt.Errorf("%w: %v", ErrNotReady, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embed: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embed: ollama status %d: %s", resp.StatusCode, string(raw))
	}

	var decoded ollamaEmbedResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if decoded.Error != "" {
		return nil, fmt.Errorf("embed: ollama error: %s", decoded.Error)
	}
	if len(decoded.Embeddings) != len(nonEmpty) {
		return nil, fmt.Errorf("embed: ollama returned %d vectors for %d inputs", len(decoded.Embeddings), len(nonEmpty))
	}

	// Merge the embedded vectors back into the output at their original
	// positions; empties keep their zero-initialised slots.
	for vi, origIdx := range nonEmptyIdx {
		v := decoded.Embeddings[vi]
		if len(v) != e.dim {
			return nil, fmt.Errorf("embed: dim mismatch: got %d, want %d", len(v), e.dim)
		}
		out[origIdx] = v
	}
	return out, nil
}
