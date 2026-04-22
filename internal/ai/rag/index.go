package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai/embed"
)

// indexFileName is the on-disk name of the serialised index. Lives
// under <project>/.iac-studio/ so it's scoped per project and can be
// .gitignore'd or cache-invalidated without affecting source files.
const indexFileName = "rag-index.json"

// indexDirName is the subdirectory we create inside each project to
// host caches and the index. Exported via IndexPath so the file
// watcher and gitignore helpers can reference the same path.
const indexDirName = ".iac-studio"

// Index is the in-memory representation of a built index.
type Index struct {
	Dim     int       `json:"dim"`
	Model   string    `json:"model"`
	BuiltAt time.Time `json:"built_at"`
	// Chunks and Vectors are parallel slices indexed by position. Kept
	// parallel (rather than embedding Vector in Chunk) so the marshal-
	// ler can emit compact JSON — vectors are the bulk of the file and
	// paired-slice layout is half the size of an embedded field.
	Chunks  []Chunk     `json:"chunks"`
	Vectors [][]float32 `json:"vectors"`
}

// IndexPath returns the absolute path where the index for projectDir
// is stored. Exposed so the API handlers can surface it in audit logs.
func IndexPath(projectDir string) string {
	return filepath.Join(projectDir, indexDirName, indexFileName)
}

// BuildOptions controls indexing. BatchSize caps how many chunks go
// into a single Embed call — Ollama keeps the model hot between
// batches, but very large batches push memory pressure; 64 is a safe
// default on a laptop.
type BuildOptions struct {
	BatchSize int
}

// Build runs the full index: walk the project, chunk, embed, persist.
// Returns the fresh Index plus any write error from the persistence
// step; a partial chunk-only result (zero vectors) is still returned
// when the Embedder is not ready, so the caller can present a clear
// "ollama not running" status without losing the walk.
func Build(ctx context.Context, projectDir string, em embed.Embedder, opts BuildOptions) (*Index, error) {
	if em == nil {
		return nil, fmt.Errorf("rag.Build: embedder is nil")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 64
	}

	chunks, walkErr := ChunkProject(projectDir)
	idx := &Index{
		Dim:     em.Dim(),
		Model:   em.Model(),
		BuiltAt: time.Now().UTC(),
		Chunks:  chunks,
		Vectors: make([][]float32, len(chunks)),
	}

	// Embed in batches. A single ErrNotReady from any batch kills the
	// whole run (degrade gracefully) but everything else — a 500 from
	// one batch, a timeout — is fatal so callers see the real error.
	for start := 0; start < len(chunks); start += opts.BatchSize {
		end := start + opts.BatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		texts := make([]string, end-start)
		for i := start; i < end; i++ {
			texts[i-start] = chunks[i].Text
		}
		vecs, err := em.Embed(ctx, texts)
		if err != nil {
			return idx, err
		}
		for i, v := range vecs {
			idx.Vectors[start+i] = v
		}
	}

	if err := Save(projectDir, idx); err != nil {
		return idx, err
	}
	return idx, walkErr
}

// Save writes the index to <projectDir>/.iac-studio/rag-index.json.
// Uses an atomic temp-file + rename so a crashed mid-write can't
// leave the file half-truncated.
func Save(projectDir string, idx *Index) error {
	if idx == nil {
		return fmt.Errorf("rag.Save: nil index")
	}
	dir := filepath.Join(projectDir, indexDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, indexFileName+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, filepath.Join(dir, indexFileName))
}

// Load reads a previously-saved index. Returns (nil, nil) when the
// file doesn't exist — callers treat "no index yet" as a valid empty
// state, not an error.
func Load(projectDir string) (*Index, error) {
	path := IndexPath(projectDir)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("rag.Load: %w", err)
	}
	return &idx, nil
}

// Stats is the lightweight status struct surfaced to the HTTP layer —
// returned from /api/projects/{name}/ai/index for both GET (current
// state) and POST (rebuild result).
type Stats struct {
	Present    bool      `json:"present"`
	Dim        int       `json:"dim,omitempty"`
	Model      string    `json:"model,omitempty"`
	BuiltAt    time.Time `json:"built_at,omitempty"`
	ChunkCount int       `json:"chunk_count,omitempty"`
	SizeBytes  int64     `json:"size_bytes,omitempty"`
}

// StatsFor returns the on-disk stats for projectDir's index. Missing
// index → Stats{Present: false}, not an error.
func StatsFor(projectDir string) (Stats, error) {
	path := IndexPath(projectDir)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Stats{}, nil
		}
		return Stats{}, err
	}
	idx, err := Load(projectDir)
	if err != nil || idx == nil {
		return Stats{Present: true, SizeBytes: info.Size()}, err
	}
	return Stats{
		Present:    true,
		Dim:        idx.Dim,
		Model:      idx.Model,
		BuiltAt:    idx.BuiltAt,
		ChunkCount: len(idx.Chunks),
		SizeBytes:  info.Size(),
	}, nil
}
