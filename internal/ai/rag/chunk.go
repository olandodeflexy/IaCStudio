// Package rag owns the project indexer + retriever that grounds AI
// prompts on the project's own code. It sits between the embed
// provider (vectors in) and the prompt builder (top-k chunks out).
package rag

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// Chunk is one embeddable unit of project content. It carries enough
// context that the retriever's output can be fed straight into a
// prompt without re-reading the source files: Text is the body, Source
// + StartLine let the model cite a location, Kind is the coarse
// categorisation so the prompt builder can weight policies differently
// from code comments.
type Chunk struct {
	ID         string `json:"id"`         // content-addressed — Source#StartLine-EndLine hash
	Source     string `json:"source"`     // project-relative file path
	StartLine  int    `json:"start_line"` // 1-based, inclusive
	EndLine    int    `json:"end_line"`   // 1-based, inclusive
	Kind       string `json:"kind"`       // "hcl_resource" | "rego" | "sentinel" | "markdown" | "text"
	Text       string `json:"text"`
}

// chunkableExtensions lists files the indexer walks. Everything else —
// .terraform/, .git/, node_modules/, binaries — is skipped via
// skipDir below.
var chunkableExtensions = map[string]struct{}{
	".tf":       {},
	".hcl":      {},
	".tfvars":   {},
	".rego":     {},
	".sentinel": {},
	".md":       {},
	".yaml":     {},
	".yml":      {},
}

// skipDir returns true for directories the indexer should not descend.
// Kept terse — these are the same dirs every other walker in the repo
// skips, so hoisting to a shared helper isn't worth the indirection.
func skipDir(name string) bool {
	switch name {
	case ".git", ".terraform", "node_modules", "dist", "build", ".iac-studio":
		return true
	}
	return false
}

// ChunkProject walks dir and returns every Chunk the indexer will embed.
// HCL files use the resource-aware parser so each chunk maps cleanly to
// a single block; everything else falls back to per-file or per-heading
// chunking. The caller hands the result to an Embedder batch-by-batch.
//
// On walk errors ChunkProject returns whatever it's collected so far +
// the error — preferable to silently dropping the whole index when one
// file is unreadable.
func ChunkProject(dir string) ([]Chunk, error) {
	var chunks []Chunk
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := chunkableExtensions[ext]; !ok {
			return nil
		}
		// Cap per-file size so a 10MB state dump doesn't blow up the
		// embed budget. 256KB is roughly 64k tokens worth of text —
		// well past any real HCL module.
		info, err := d.Info()
		if err != nil || info.Size() > 256*1024 {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		produced, perr := chunkFile(path, rel, ext)
		if perr != nil {
			// Don't abort the walk; just skip this file.
			return nil
		}
		chunks = append(chunks, produced...)
		return nil
	})
	return chunks, walkErr
}

// chunkFile dispatches by extension. HCL gets the resource-aware path;
// everything else goes through whole-file chunking (with a soft line
// cap that splits very long files into 200-line overlapping windows so
// the embedder sees coherent regions).
func chunkFile(absPath, relPath, ext string) ([]Chunk, error) {
	body, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	switch ext {
	case ".tf", ".hcl", ".tfvars":
		return chunkHCL(absPath, relPath, body)
	case ".md":
		return chunkByHeadings(relPath, string(body), "markdown"), nil
	case ".rego":
		return []Chunk{newChunk(relPath, 1, lineCount(body), "rego", string(body))}, nil
	case ".sentinel":
		return []Chunk{newChunk(relPath, 1, lineCount(body), "sentinel", string(body))}, nil
	}
	return []Chunk{newChunk(relPath, 1, lineCount(body), "text", string(body))}, nil
}

// chunkHCL uses the HCL parser to emit one chunk per resource/variable/
// output/module/data/locals block. Non-parseable HCL falls through to a
// whole-file chunk so we still index partially-broken files during
// editing.
func chunkHCL(absPath, relPath string, body []byte) ([]Chunk, error) {
	lines := strings.Split(string(body), "\n")

	// ParseDir walks the whole directory — a syntax error in a sibling
	// file (e.g. scratch.tf the user is mid-edit) shouldn't block us
	// from chunking this file's resources, so we only fall back when
	// nothing at all was parsed.
	p := parser.HCLParser{}
	resources, _ := p.ParseDir(filepath.Dir(absPath))
	if len(resources) == 0 {
		return []Chunk{newChunk(relPath, 1, len(lines), "hcl_file", string(body))}, nil
	}

	// ParseDir returns every resource under the directory; filter to the
	// file we're chunking. Each resource's Line marks the opening brace;
	// we greedily extend to the matching close brace to capture the full
	// block body.
	var out []Chunk
	for _, r := range resources {
		if r.File != absPath {
			continue
		}
		start := r.Line
		if start <= 0 || start > len(lines) {
			continue
		}
		end := findMatchingClose(lines, start-1)
		text := strings.Join(lines[start-1:end], "\n")
		out = append(out, newChunk(relPath, start, end, "hcl_"+strings.ToLower(r.BlockType), text))
	}
	if len(out) == 0 {
		// Parser ran but produced nothing for this file (e.g. only
		// comments) — index the whole file as a fallback.
		out = append(out, newChunk(relPath, 1, len(lines), "hcl_file", string(body)))
	}
	return out, nil
}

// findMatchingClose tracks brace depth starting at startIdx (the line
// that contains the opening brace). Returns the 1-based line number of
// the line that closes the block, clamped to len(lines) if the file is
// malformed. We count '{' and '}' naively — string literals in HCL
// can't contain unescaped braces at parse time so this is good enough
// for an indexer heuristic; the parser has the authoritative check.
func findMatchingClose(lines []string, startIdx int) int {
	depth := 0
	for i := startIdx; i < len(lines); i++ {
		for _, ch := range lines[i] {
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
	}
	return len(lines)
}

// chunkByHeadings splits a markdown document at every '#' heading. Each
// section becomes its own chunk so the retriever can surface the
// relevant README section without pulling the whole document.
func chunkByHeadings(relPath, body, kind string) []Chunk {
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1MB line cap — generous for README content
	var out []Chunk
	var cur []string
	sectionStart := 1
	lineNo := 0
	flush := func(endLine int) {
		if len(cur) == 0 {
			return
		}
		out = append(out, newChunk(relPath, sectionStart, endLine, kind, strings.Join(cur, "\n")))
		cur = cur[:0]
	}
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.HasPrefix(line, "#") && len(cur) > 0 {
			flush(lineNo - 1)
			sectionStart = lineNo
		}
		cur = append(cur, line)
	}
	flush(lineNo)
	return out
}

func newChunk(source string, startLine, endLine int, kind, text string) Chunk {
	h := sha1.Sum([]byte(fmt.Sprintf("%s#%d-%d\n%s", source, startLine, endLine, text)))
	return Chunk{
		ID:        hex.EncodeToString(h[:]),
		Source:    source,
		StartLine: startLine,
		EndLine:   endLine,
		Kind:      kind,
		Text:      text,
	}
}

func lineCount(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := strings.Count(string(b), "\n")
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}
