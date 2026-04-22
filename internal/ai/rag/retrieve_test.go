package rag

import (
	"strings"
	"testing"
)

func TestRetrieve_RanksByCosine(t *testing.T) {
	// Three chunks, three hand-chosen vectors. Query is aligned with
	// the second chunk, so we expect it to top the ranking.
	idx := &Index{
		Dim: 3,
		Chunks: []Chunk{
			{ID: "a", Source: "a.tf", Text: "a"},
			{ID: "b", Source: "b.tf", Text: "b"},
			{ID: "c", Source: "c.tf", Text: "c"},
		},
		Vectors: [][]float32{
			{1, 0, 0},
			{0, 1, 0},
			{0, 0, 1},
		},
	}

	hits := Retrieve(idx, []float32{0, 1, 0}, 2)
	if len(hits) != 1 {
		// Only one vector has positive cosine with {0,1,0}; zeros get
		// filtered (< 0 or exactly 0 both return).
		t.Fatalf("want 1 hit, got %d", len(hits))
	}
	if hits[0].Chunk.ID != "b" {
		t.Errorf("want top hit 'b', got %q", hits[0].Chunk.ID)
	}
}

func TestRetrieve_HandlesEmptyInputs(t *testing.T) {
	if h := Retrieve(nil, []float32{1, 0}, 5); h != nil {
		t.Errorf("nil index should return nil, got %v", h)
	}
	if h := Retrieve(&Index{Dim: 3}, nil, 5); h != nil {
		t.Errorf("nil query should return nil, got %v", h)
	}
	if h := Retrieve(&Index{Dim: 3}, []float32{1, 2}, 5); h != nil {
		t.Errorf("dim mismatch should return nil, got %v", h)
	}
}

func TestRetrieve_AppliesKLimit(t *testing.T) {
	idx := &Index{Dim: 2,
		Chunks:  []Chunk{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Vectors: [][]float32{{1, 0}, {0.9, 0.1}, {0.8, 0.2}},
	}
	hits := Retrieve(idx, []float32{1, 0}, 2)
	if len(hits) != 2 {
		t.Errorf("want 2 hits, got %d", len(hits))
	}
}

func TestFormatContext_EmptyReturnsEmpty(t *testing.T) {
	if got := FormatContext(nil); got != "" {
		t.Errorf("empty hits should return empty string, got %q", got)
	}
}

func TestFormatContext_IncludesCitationAndFence(t *testing.T) {
	hits := []Hit{
		{Chunk: Chunk{
			Source: "modules/networking/main.tf",
			StartLine: 10, EndLine: 20,
			Kind: "hcl_resource",
			Text: "resource \"aws_vpc\" \"main\" {\n  cidr_block = \"10.0.0.0/16\"\n}\n",
		}, Score: 0.84},
	}
	got := FormatContext(hits)

	want := []string{
		"modules/networking/main.tf",
		"lines 10-20",
		"0.84",
		"```hcl\n",
		"aws_vpc",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("FormatContext missing %q in:\n%s", w, got)
		}
	}
}
