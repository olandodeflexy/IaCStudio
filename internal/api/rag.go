package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/ai/embed"
	"github.com/iac-studio/iac-studio/internal/ai/rag"
)

// ragBuildMu serialises per-project index rebuilds so two concurrent
// POSTs don't fight over the same on-disk file. A finer-grained mutex
// map would let different projects build in parallel, but embeds saturate
// the model serving thread anyway, so a global lock is simplest.
var ragBuildMu sync.Mutex

// ragEmbeddingEndpoint / Model / Dim are the defaults the HTTP layer
// uses when the user hasn't overridden via settings. Nomic-embed-text
// is the Ollama-side pick because it's small (~300MB) and runs
// comfortably on CPU — matches the privacy-first posture from issue #8.
const (
	defaultEmbedEndpoint = "http://localhost:11434"
	defaultEmbedModel    = "nomic-embed-text"
	defaultEmbedDim      = 768
)

// registerRAGRoutes wires the RAG endpoints onto mux. Isolated so it's
// testable without spinning up the full router + every other subsystem.
func registerRAGRoutes(mux *http.ServeMux, projectsDir string, aiClient *ai.Client) {
	// GET /api/projects/{name}/ai/index — current index stats. Present:
	// false when no index exists (not an error).
	mux.HandleFunc("GET /api/projects/{name}/ai/index", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		stats, err := rag.StatsFor(projectPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stats)
	})

	// POST /api/projects/{name}/ai/index — rebuild. Body is optional; when
	// provided it can override the embedding model/endpoint/dim on a per-
	// call basis (useful for CI benchmarks). Returns the new Stats.
	mux.HandleFunc("POST /api/projects/{name}/ai/index", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		type overrideBody struct {
			Endpoint string `json:"endpoint,omitempty"`
			Model    string `json:"model,omitempty"`
			Dim      int    `json:"dim,omitempty"`
		}
		var body overrideBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		em := embed.NewOllama(embed.Config{
			Endpoint: pick(body.Endpoint, defaultEmbedEndpoint),
			Model:    pick(body.Model, defaultEmbedModel),
			Dim:      pickInt(body.Dim, defaultEmbedDim),
			Timeout:  2 * time.Minute,
		})

		ragBuildMu.Lock()
		idx, buildErr := rag.Build(r.Context(), projectPath, em, rag.BuildOptions{})
		ragBuildMu.Unlock()

		// Build returns a partial index + error when embedding fails — we
		// still surface the partial so operators can see which files were
		// at least chunked. The client gets a 502 so it knows the build
		// didn't complete.
		stats, _ := rag.StatsFor(projectPath)
		if buildErr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": buildErr.Error(),
				"stats": stats,
				"partial_chunks": func() int {
					if idx == nil {
						return 0
					}
					return len(idx.Chunks)
				}(),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stats)
	})

	// Expose a retrieval helper for the chat/suggest/fix/topology handlers
	// to call when building prompts. Kept as a package-level function via
	// the sharedRAG singleton so the handlers don't each need to re-
	// instantiate an embedder.
	sharedRAG.configure(aiClient)
}

// sharedRAG is the process-wide retrieval helper used by the
// chat/topology/fix/suggest handlers. It lazily constructs the default
// Ollama embedder on first use and caches loaded indexes per project
// (invalidating on file-mtime change of the rag-index.json file) to
// avoid re-reading a 1-5MB JSON blob on every chat turn.
var sharedRAG = &ragHelper{}

type ragHelper struct {
	mu sync.RWMutex
	em embed.Embedder
	// cache maps projectDir → (mtime, loaded index). A bigger cache
	// would evict under pressure; for IaC Studio's typical scale
	// (≤ 20 open projects, single user) simplicity wins.
	cache map[string]cachedIndex
}

type cachedIndex struct {
	mtime time.Time
	idx   *rag.Index
}

func (h *ragHelper) configure(client *ai.Client) {
	_ = client // reserved for future: pull embed settings from client
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.em == nil {
		h.em = embed.NewOllama(embed.Config{
			Endpoint: defaultEmbedEndpoint,
			Model:    defaultEmbedModel,
			Dim:      defaultEmbedDim,
			Timeout:  30 * time.Second,
		})
	}
	if h.cache == nil {
		h.cache = map[string]cachedIndex{}
	}
}

// Context retrieves top-k chunks for query and returns a formatted
// prompt preamble. Returns empty string when:
//   - no index exists yet
//   - the embedder is unavailable
//   - retrieval returned nothing
//
// Never returns an error: RAG is best-effort context, and failing the
// whole chat over a missing embedder would be strictly worse than
// running chat without grounding.
func (h *ragHelper) Context(ctx context.Context, projectDir, query string, k int) string {
	if h == nil || h.em == nil || projectDir == "" {
		return ""
	}
	idx, err := h.indexFor(projectDir)
	if err != nil || idx == nil {
		return ""
	}
	hits, err := rag.RetrieveText(ctx, h.em, idx, query, k)
	if err != nil {
		// ErrNotReady and friends — degrade silently.
		return ""
	}
	return rag.FormatContext(hits)
}

func (h *ragHelper) indexFor(projectDir string) (*rag.Index, error) {
	h.mu.RLock()
	cached, ok := h.cache[projectDir]
	h.mu.RUnlock()

	path := rag.IndexPath(projectDir)
	info, err := statMod(path)
	if err != nil {
		return nil, nil
	}
	if ok && cached.mtime.Equal(info) {
		return cached.idx, nil
	}

	idx, err := rag.Load(projectDir)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.cache[projectDir] = cachedIndex{mtime: info, idx: idx}
	h.mu.Unlock()
	return idx, nil
}

// statMod returns the mtime of path, or a zero time + error if missing.
func statMod(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// pick / pickInt are tiny helpers for "use body value if set, else default".
func pick(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func pickInt(a, b int) int {
	if a > 0 {
		return a
	}
	return b
}
