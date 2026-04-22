// Package embed owns the embedding-provider surface for IaC Studio's
// RAG pipeline. An Embedder turns a batch of text chunks into fixed-
// width float vectors; the rest of the system — indexer, retriever,
// prompt builder — is provider-agnostic and plugs into whichever
// Embedder the user's AI settings resolve to.
//
// Today we ship an Ollama adapter (privacy-first: the embedding model
// runs on-device and the project's HCL never leaves the machine). A
// remote adapter (Voyage, OpenAI, Cohere) can be added later without
// changing call sites.
package embed

import (
	"context"
	"errors"
	"math"
)

// Embedder is what every adapter implements. Embed is the batch API —
// embedding one chunk per call would be three orders of magnitude
// slower for a project-sized index.
//
// Returned vectors have a stable length per Embedder instance; callers
// must not mix outputs from Embedders with different Dim() values in
// the same index.
type Embedder interface {
	// Embed returns one vector per input, in the same order. Empty
	// strings are passed through as zero vectors so the caller's slice
	// alignment is preserved; the retriever ignores zero vectors at
	// query time.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)

	// Dim is the vector width this Embedder produces. Used by the
	// index to validate incoming vectors and to pre-size storage.
	Dim() int

	// Model is the string identifier (e.g. "nomic-embed-text") that
	// callers can surface in audit logs + stats so operators know
	// what was used to build a given index.
	Model() string
}

// ErrNotReady signals the Embedder is configured but unavailable — the
// Ollama server isn't running, the model isn't pulled yet, the API key
// is wrong, etc. Callers (the indexer especially) check for it to
// gracefully degrade: skip reindex, surface a clear error in the
// stats feed, and leave the existing index intact.
var ErrNotReady = errors.New("embedder not ready")

// Cosine returns the cosine similarity of two vectors. Exported because
// the retriever needs it and unit tests want to assert on it directly
// — centralising it here keeps the distance metric in one place.
//
// Zero vectors on either side yield 0 rather than NaN so an unembed-
// dable chunk doesn't poison the ranking.
func Cosine(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / float32(math.Sqrt(float64(na))*math.Sqrt(float64(nb)))
}
