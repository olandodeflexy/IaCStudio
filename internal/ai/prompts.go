package ai

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"text/template"
)

// Prompt files live as Markdown with YAML frontmatter:
//
//     ---
//     id: system
//     description: ...
//     ---
//     Prompt body, optionally with {{.Field}} template placeholders.
//
// The frontmatter is metadata only — the renderer drops it before compiling
// the body as a text/template. Keeping the files editable as plain .md means
// a non-engineer can tweak wording without touching Go.
//
//go:embed prompts/*.md
var promptFS embed.FS

// promptEntry is one parsed prompt: the metadata from its frontmatter plus
// the compiled template ready to render.
type promptEntry struct {
	ID          string
	Description string
	Template    *template.Template
}

// promptSet is loaded once at package init and read-only thereafter.
var promptSet = loadPrompts()

func loadPrompts() map[string]*promptEntry {
	out := map[string]*promptEntry{}
	err := fs.WalkDir(promptFS, "prompts", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		raw, err := promptFS.ReadFile(path)
		if err != nil {
			return err
		}
		entry, err := parsePrompt(path, raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		out[entry.ID] = entry
		return nil
	})
	if err != nil {
		// Prompts are bundled via go:embed so any walk error here is a
		// compile/build issue, not a runtime one — panic loud enough that
		// tests catch it during package init.
		panic(fmt.Sprintf("loading prompts: %v", err))
	}
	return out
}

// parsePrompt splits the frontmatter from the body and compiles the body as
// a text/template. Frontmatter parsing is intentionally minimal — we only
// support `key: value` lines (no YAML lists or nested structures) because
// the metadata is purely documentation.
func parsePrompt(path string, raw []byte) (*promptEntry, error) {
	content := string(raw)
	const delim = "---\n"
	if !strings.HasPrefix(content, delim) {
		return nil, fmt.Errorf("missing opening frontmatter")
	}
	rest := content[len(delim):]
	end := strings.Index(rest, delim)
	if end == -1 {
		return nil, fmt.Errorf("missing closing frontmatter")
	}
	fmText := rest[:end]
	body := rest[end+len(delim):]

	entry := &promptEntry{
		// Default ID falls back to the file stem so prompts with missing id:
		// keys still load without a crash.
		ID: strings.TrimSuffix(filepath.Base(path), ".md"),
	}
	for _, line := range strings.Split(fmText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "id":
			entry.ID = v
		case "description":
			entry.Description = v
		}
	}

	tmpl, err := template.New(entry.ID).Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	entry.Template = tmpl
	return entry, nil
}

// renderPrompt executes a named template against data. Panics on unknown IDs
// because every caller references a compile-time-known ID — a miss here is a
// programming error, not a runtime one.
func renderPrompt(id string, data any) string {
	entry, ok := promptSet[id]
	if !ok {
		panic(fmt.Sprintf("prompt %q not registered", id))
	}
	var buf bytes.Buffer
	if err := entry.Template.Execute(&buf, data); err != nil {
		// Same reasoning: template errors here are programmer errors.
		panic(fmt.Sprintf("render prompt %q: %v", id, err))
	}
	return buf.String()
}
