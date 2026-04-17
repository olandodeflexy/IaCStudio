// Package scaffold renders opinionated project layouts (blueprints) onto disk.
//
// A Blueprint declares a set of input fields and a Render function that turns
// those inputs into a map of relative file paths → file contents. The scaffold
// package writes those files to disk, respecting script-executable bits and
// creating any missing directories along the way.
//
// Blueprints are registered in a Registry at package init time. The API layer
// asks the Registry for the list of available blueprints, shows inputs to the
// user, then calls Render with the resolved input values.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Input describes a single user-supplied value a Blueprint needs to render.
type Input struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"` // "string" | "bool" | "select" | "multiselect"
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Required    bool     `json:"required,omitempty"`
}

// File is the rendered content of a single path. Executable is honored for
// shell scripts and similar.
type File struct {
	Path       string
	Content    []byte
	Executable bool
}

// Blueprint is an opinionated project layout generator.
type Blueprint interface {
	ID() string
	Name() string
	Description() string
	Tool() string    // "terraform" | "pulumi" | "ansible" | "multi"
	Inputs() []Input // static declaration of expected inputs
	Render(values map[string]any) ([]File, error)
}

// Registry holds registered blueprints, keyed by ID.
type Registry struct {
	blueprints map[string]Blueprint
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{blueprints: make(map[string]Blueprint)}
}

// Register adds a blueprint. Later registrations with the same ID replace earlier ones.
func (r *Registry) Register(bp Blueprint) {
	r.blueprints[bp.ID()] = bp
}

// Get returns a blueprint by ID.
func (r *Registry) Get(id string) (Blueprint, bool) {
	bp, ok := r.blueprints[id]
	return bp, ok
}

// List returns all registered blueprints, sorted by ID for stable output.
func (r *Registry) List() []Blueprint {
	out := make([]Blueprint, 0, len(r.blueprints))
	for _, bp := range r.blueprints {
		out = append(out, bp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Default is the package-level registry populated by init() in blueprint files.
var Default = NewRegistry()

// resolveWritePath validates a rendered file path and resolves it under root.
func resolveWritePath(root, p string) (string, string, error) {
	if p == "" {
		return "", "", fmt.Errorf("invalid empty file path")
	}
	if filepath.IsAbs(p) {
		return "", "", fmt.Errorf("invalid absolute file path %q", p)
	}

	cleaned := filepath.Clean(p)
	if cleaned == "." || cleaned == "" {
		return "", "", fmt.Errorf("invalid file path %q", p)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("invalid file path %q", p)
	}

	full := filepath.Join(root, cleaned)
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", "", fmt.Errorf("resolve %q: %w", p, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("file path escapes root %q", p)
	}

	return cleaned, full, nil
}

// Write materializes a rendered blueprint onto disk under root.
//
// Existing files are NOT overwritten; Write returns an error listing the
// conflicting paths so callers can surface them to the user. This avoids
// clobbering in-progress user work when scaffolding into a non-empty directory.
func Write(root string, files []File) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root %s: %w", root, err)
	}

	type resolvedFile struct {
		file    File
		path    string
		full    string
	}

	resolved := make([]resolvedFile, 0, len(files))
	var conflicts []string
	for _, f := range files {
		cleaned, full, err := resolveWritePath(root, f.Path)
		if err != nil {
			return err
		}
		if _, err := os.Stat(full); err == nil {
			conflicts = append(conflicts, cleaned)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", cleaned, err)
		}
		resolved = append(resolved, resolvedFile{
			file: f,
			path: cleaned,
			full: full,
		})
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("refusing to overwrite existing files: %s", strings.Join(conflicts, ", "))
	}

	for _, rf := range resolved {
		if err := os.MkdirAll(filepath.Dir(rf.full), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(rf.full), err)
		}
		mode := os.FileMode(0o644)
		if rf.file.Executable {
			mode = 0o755
		}
		if err := os.WriteFile(rf.full, rf.file.Content, mode); err != nil {
			return fmt.Errorf("write %s: %w", rf.path, err)
		}
	}
	return nil
}

// stringInput fetches a string input with a default fallback.
func stringInput(values map[string]any, key, fallback string) string {
	if v, ok := values[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return fallback
}

// stringSliceInput fetches a []string, accepting []any (JSON decoded) or []string.
func stringSliceInput(values map[string]any, key string, fallback []string) []string {
	v, ok := values[key]
	if !ok {
		return fallback
	}
	switch t := v.(type) {
	case []string:
		if len(t) == 0 {
			return fallback
		}
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return fallback
		}
		return out
	}
	return fallback
}
