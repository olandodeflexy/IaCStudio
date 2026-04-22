package rag

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/iac-studio/iac-studio/internal/ai/embed"
)

// Hit is one retrieval result — a Chunk plus its similarity score to
// the query. The prompt builder uses Score to decide how prominently
// to present each piece of context (top hit as a strong signal, tail
// hits as "may also be relevant").
type Hit struct {
	Chunk Chunk   `json:"chunk"`
	Score float32 `json:"score"`
}

// Retrieve returns the top-k most similar chunks to queryVec from idx.
// Ties are broken by original chunk order (stable sort) so the output
// is deterministic across runs with the same index + query.
//
// A nil index or zero-length queryVec returns nil — not an error —
// because "no index yet" and "empty query" are both legitimate states
// the caller surfaces to the user as "no grounding, model runs as
// before".
func Retrieve(idx *Index, queryVec []float32, k int) []Hit {
	if idx == nil || len(queryVec) == 0 || len(idx.Chunks) == 0 {
		return nil
	}
	if idx.Dim != len(queryVec) {
		return nil
	}
	hits := make([]Hit, 0, len(idx.Chunks))
	for i := range idx.Chunks {
		score := embed.Cosine(queryVec, idx.Vectors[i])
		if score <= 0 {
			continue
		}
		hits = append(hits, Hit{Chunk: idx.Chunks[i], Score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

// RetrieveText is the common-case helper: embed the query string, then
// retrieve top-k. Returns nil (no error) when the embedder isn't ready
// — callers log this and fall through to the no-RAG prompt so the user
// still gets a response.
func RetrieveText(ctx context.Context, em embed.Embedder, idx *Index, query string, k int) ([]Hit, error) {
	if em == nil || idx == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	vecs, err := em.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("rag.RetrieveText: expected 1 vector, got %d", len(vecs))
	}
	return Retrieve(idx, vecs[0], k), nil
}

// FormatContext renders a list of hits as a prompt-ready preamble. The
// format is deliberately plain-text-first so every provider sees the
// same payload; markdown-style fenced blocks let the model parse HCL
// correctly without leaking through as literal ``` characters in a
// chat response.
//
// An empty slice returns empty string so callers can unconditionally
// concat without guards.
func FormatContext(hits []Hit) string {
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Here is relevant context from the user's project:\n\n")
	for i, h := range hits {
		fmt.Fprintf(&b, "--- context %d: %s (lines %d-%d, relevance %.2f) ---\n",
			i+1, h.Chunk.Source, h.Chunk.StartLine, h.Chunk.EndLine, h.Score)
		lang := languageFromKind(h.Chunk.Kind)
		if lang != "" {
			b.WriteString("```" + lang + "\n")
		}
		b.WriteString(h.Chunk.Text)
		if !strings.HasSuffix(h.Chunk.Text, "\n") {
			b.WriteString("\n")
		}
		if lang != "" {
			b.WriteString("```\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Use this context to match the project's conventions (naming, tagging, module structure). Cite specific files when relevant. If the context doesn't cover what's needed, proceed using general best practices.\n")
	return b.String()
}

// languageFromKind maps our chunk Kind to a markdown fence language tag
// so the model gets syntax-highlighted context. Unknown kinds → empty
// (plain text fence).
func languageFromKind(kind string) string {
	switch {
	case strings.HasPrefix(kind, "hcl_"):
		return "hcl"
	case kind == "rego":
		return "rego"
	case kind == "sentinel":
		return "sentinel"
	case kind == "markdown":
		return "markdown"
	}
	return ""
}
