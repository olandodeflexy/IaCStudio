package rag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scaffoldProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(p, body string) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.tf", `resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  tags = { Owner = "team-platform" }
}

resource "aws_s3_bucket" "logs" {
  bucket = "acme-logs"
}
`)
	write("policies/opa/tags.rego", `package main

deny[msg] { not input.tags.Owner; msg := "missing owner" }
`)
	write("README.md", `# Acme Infra

## Modules
Networking, compute, data.

## Policies
Required tags: Owner, CostCenter.
`)
	// Files we shouldn't index.
	write(".git/config", "ignored")
	write(".terraform/junk", "ignored")
	write("node_modules/bad/file.tf", "ignored")
	return dir
}

func TestChunkProject_EmitsHCLRego_And_MarkdownSections(t *testing.T) {
	dir := scaffoldProject(t)
	chunks, err := ChunkProject(dir)
	if err != nil {
		t.Fatalf("ChunkProject: %v", err)
	}

	kinds := map[string]int{}
	for _, c := range chunks {
		kinds[c.Kind]++
		if strings.Contains(c.Source, ".git") || strings.Contains(c.Source, ".terraform") || strings.Contains(c.Source, "node_modules") {
			t.Errorf("indexed a skip-dir file: %s", c.Source)
		}
	}

	if kinds["hcl_resource"] != 2 {
		t.Errorf("want 2 hcl_resource chunks (vpc + s3), got %d", kinds["hcl_resource"])
	}
	if kinds["rego"] != 1 {
		t.Errorf("want 1 rego chunk, got %d", kinds["rego"])
	}
	// Markdown split at headings: # Acme, ## Modules, ## Policies → 3 sections.
	if kinds["markdown"] < 2 {
		t.Errorf("want ≥ 2 markdown sections, got %d", kinds["markdown"])
	}
}

func TestChunkProject_ChunksAreContentAddressed(t *testing.T) {
	dir := scaffoldProject(t)
	chunks1, _ := ChunkProject(dir)
	chunks2, _ := ChunkProject(dir)

	if len(chunks1) != len(chunks2) {
		t.Fatalf("non-deterministic chunk count: %d vs %d", len(chunks1), len(chunks2))
	}
	for i := range chunks1 {
		if chunks1[i].ID != chunks2[i].ID {
			t.Errorf("chunk ids not deterministic at %d: %s vs %s", i, chunks1[i].ID, chunks2[i].ID)
		}
	}
}

func TestChunkProject_HonorsSizeCap(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("x", 300*1024)
	if err := os.WriteFile(filepath.Join(dir, "big.md"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	chunks, err := ChunkProject(dir)
	if err != nil {
		t.Fatalf("ChunkProject: %v", err)
	}
	for _, c := range chunks {
		if c.Source == "big.md" {
			t.Errorf("300KB file should have been skipped, got chunk: %+v", c)
		}
	}
}

func TestFindMatchingClose(t *testing.T) {
	src := `resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  tags = {
    Owner = "me"
  }
}
extra line
`
	end := findMatchingClose(strings.Split(src, "\n"), 0)
	if end != 6 {
		t.Errorf("findMatchingClose = %d, want 6", end)
	}
}
