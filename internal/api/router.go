package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/ai/providers"
	"github.com/iac-studio/iac-studio/internal/catalog"
	"github.com/iac-studio/iac-studio/internal/drift"
	"github.com/iac-studio/iac-studio/internal/exporter"
	"github.com/iac-studio/iac-studio/internal/generator"
	"github.com/iac-studio/iac-studio/internal/importer"
	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/project"
	"github.com/iac-studio/iac-studio/internal/registry"
	"github.com/iac-studio/iac-studio/internal/runner"
	"github.com/iac-studio/iac-studio/internal/scaffold"
	"github.com/iac-studio/iac-studio/internal/security"
	"github.com/iac-studio/iac-studio/internal/watcher"
)

// safeProjectPath validates a project name and returns its absolute path under projectsDir.
// It rejects names containing path separators, "..", or other traversal attempts.
func safeProjectPath(projectsDir, name string) (string, error) {
	// Reject empty, dot-prefixed, or names with path separators
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) ||
		strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid project name: %q", name)
	}
	// Only allow alphanumeric, hyphens, and underscores
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			return "", fmt.Errorf("invalid project name: %q (only alphanumeric, hyphens, underscores)", name)
		}
	}
	resolved := filepath.Join(projectsDir, name)
	// Resolve symlinks so a symlink at ~/iac-projects/evil -> /etc/ is caught
	// (and so macOS's /var/folders -> /private/var/folders symlink doesn't
	// cause httptest-based tests to trip the escape check below).
	//
	// filepath.Abs errors surface explicitly — a failure would leave an
	// empty absProjects, which would then let any resolved path pass the
	// HasPrefix check and weaken the symlink-escape protection.
	absProjects, err := filepath.Abs(projectsDir)
	if err != nil {
		return "", fmt.Errorf("resolve projects dir: %w", err)
	}
	if evalProjects, err := filepath.EvalSymlinks(absProjects); err == nil {
		absProjects = evalProjects
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	// If the directory already exists, resolve symlinks in the actual path.
	if evalResolved, err := filepath.EvalSymlinks(resolved); err == nil {
		if absEval, absErr := filepath.Abs(evalResolved); absErr == nil {
			absResolved = absEval
		}
	}
	if !strings.HasPrefix(absResolved, absProjects+string(filepath.Separator)) {
		return "", fmt.Errorf("project path escapes root: %q", name)
	}
	return resolved, nil
}

// safeSubdir resolves a subdirectory beneath projectPath while
// enforcing the same traversal + containment guarantees safeProjectPath
// offers at the project level. Each path segment is validated
// (alphanumeric + hyphen + underscore, no dots, no separators) and
// the final absolute path must stay inside projectPath after symlink
// resolution.
//
// Used by the /api/projects/{name}/run endpoint to rebase execution
// into environments/<env>/ for layered-v1 layouts so the runner finds
// Pulumi.yaml / main.tf in the right workdir.
func safeSubdir(projectPath string, segments ...string) (string, error) {
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." ||
			strings.ContainsAny(seg, `/\`) ||
			strings.Contains(seg, "..") {
			return "", fmt.Errorf("invalid path segment: %q", seg)
		}
		for _, r := range seg {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_') {
				return "", fmt.Errorf("invalid path segment: %q (only alphanumeric, hyphens, underscores)", seg)
			}
		}
	}
	joined := filepath.Join(append([]string{projectPath}, segments...)...)
	absProject, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}
	if eval, err := filepath.EvalSymlinks(absProject); err == nil {
		absProject = eval
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if eval, err := filepath.EvalSymlinks(joined); err == nil {
		if abs, absErr := filepath.Abs(eval); absErr == nil {
			absJoined = abs
		}
	}
	if !strings.HasPrefix(absJoined, absProject+string(filepath.Separator)) {
		return "", fmt.Errorf("subdir escapes project root")
	}
	info, err := os.Stat(joined)
	if err != nil {
		// Don't surface the underlying os.Stat error — it carries the
		// absolute filesystem path which we don't want bubbling up to
		// HTTP clients. Server-side log keeps the detail for ops
		// debugging.
		log.Printf("safeSubdir: stat %s: %v", joined, err)
		if os.IsNotExist(err) {
			return "", fmt.Errorf("subdir does not exist")
		}
		return "", fmt.Errorf("subdir is not accessible")
	}
	// The result is used as cmd.Dir — passing a file would produce a
	// confusing 'not a directory' error mid-exec. Reject here so the
	// 400 carries a targeted message. Don't include the path; the
	// caller already knows what they passed in.
	if !info.IsDir() {
		log.Printf("safeSubdir: not a directory: %s", joined)
		return "", fmt.Errorf("subdir is not a directory")
	}
	return joined, nil
}

func safeProjectFile(projectPath, requested string, allowedExts ...string) (string, error) {
	if requested == "" {
		return "", fmt.Errorf("file path is required")
	}
	target := requested
	if !filepath.IsAbs(target) {
		target = filepath.Join(projectPath, target)
	}
	target = filepath.Clean(target)

	absProject, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	if eval, err := filepath.EvalSymlinks(absProject); err == nil {
		absProject = eval
	}

	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve file path: %w", err)
	}

	if len(allowedExts) > 0 {
		ext := filepath.Ext(absTarget)
		allowed := false
		for _, allowedExt := range allowedExts {
			if ext == allowedExt {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("unsupported file extension: %s", ext)
		}
	}

	parent := filepath.Dir(absTarget)
	evalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file parent directory does not exist")
		}
		return "", fmt.Errorf("file parent directory is not accessible")
	}
	evalParent, err = filepath.Abs(evalParent)
	if err != nil {
		return "", fmt.Errorf("resolve file parent: %w", err)
	}
	evalTargetPath := filepath.Join(evalParent, filepath.Base(absTarget))
	parentRel, err := filepath.Rel(absProject, evalParent)
	if err != nil || strings.HasPrefix(parentRel, ".."+string(filepath.Separator)) || parentRel == ".." {
		return "", fmt.Errorf("file parent escapes project root")
	}
	targetRel, err := filepath.Rel(absProject, evalTargetPath)
	if err != nil || targetRel == "." || strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) || targetRel == ".." {
		return "", fmt.Errorf("file path escapes project root")
	}

	if evalTarget, err := filepath.EvalSymlinks(absTarget); err == nil {
		evalTarget, err = filepath.Abs(evalTarget)
		if err != nil {
			return "", fmt.Errorf("resolve existing file: %w", err)
		}
		targetRel, err := filepath.Rel(absProject, evalTarget)
		if err != nil || strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) || targetRel == ".." {
			return "", fmt.Errorf("existing file escapes project root")
		}
	}

	return absTarget, nil
}

func allowedGeneratedExtensions(tool, defaultExt string) []string {
	if tool == "ansible" {
		return []string{".yml", ".yaml"}
	}
	return []string{defaultExt}
}

func generateForSync(gen generator.Generator, resources []parser.Resource, includeProviders bool) (string, error) {
	if !includeProviders {
		if resourcesOnly, ok := gen.(interface {
			GenerateResourcesOnly([]parser.Resource) (string, error)
		}); ok {
			return resourcesOnly.GenerateResourcesOnly(resources)
		}
	}
	return gen.Generate(resources)
}

// planGate tracks which projects have had a recent plan run.
// Apply/destroy is only allowed after a plan has been run for the same project.
var planGate = struct {
	mu    sync.Mutex
	plans map[string]time.Time // projectPath -> last plan time
}{plans: make(map[string]time.Time)}

// recordPlan marks that a plan was run for a project.
func recordPlan(projectPath string) {
	planGate.mu.Lock()
	planGate.plans[projectPath] = time.Now()
	planGate.mu.Unlock()
}

// hasPlan checks that a plan was run for a project within the last hour.
func hasPlan(projectPath string) bool {
	planGate.mu.Lock()
	defer planGate.mu.Unlock()
	t, ok := planGate.plans[projectPath]
	return ok && time.Since(t) < time.Hour
}

// maxRequestBody is the maximum allowed request body size (1MB).
// Prevents clients from sending oversized payloads to exhaust memory.
const maxRequestBody = 1 << 20

// limitBody wraps r.Body with a MaxBytesReader so oversized payloads
// are rejected before the full body is read into memory.
func limitBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
}

// NewRouter creates the HTTP router with all endpoints.
func NewRouter(hub *Hub, fw *watcher.FileWatcher, aiClient *ai.Client, run *runner.SafeRunner, projectsDir string) *http.ServeMux {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "0.1.0"})
	})

	// List available IaC tools detected on this machine
	mux.HandleFunc("GET /api/tools", func(w http.ResponseWriter, r *http.Request) {
		tools := run.DetectTools()
		_ = json.NewEncoder(w).Encode(tools)
	})

	// Resource catalog — returns all resources for a tool, optionally filtered by provider
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Query().Get("tool")
		if tool == "" {
			tool = "terraform"
		}
		provider := r.URL.Query().Get("provider") // optional: "aws", "google", "azurerm"
		var cat catalog.Catalog
		if provider != "" {
			cat = catalog.GetCatalogByProvider(tool, provider)
		} else {
			cat = catalog.GetCatalog(tool)
		}
		_ = json.NewEncoder(w).Encode(cat)
	})

	// List projects
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(projectsDir)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		projects := []map[string]string{}
		for _, e := range entries {
			if e.IsDir() {
				projects = append(projects, map[string]string{
					"name": e.Name(),
					"path": filepath.Join(projectsDir, e.Name()),
				})
			}
		}
		_ = json.NewEncoder(w).Encode(projects)
	})

	// Create project
	mux.HandleFunc("POST /api/projects", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Name string `json:"name"`
			Tool string `json:"tool"` // terraform | opentofu | ansible
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		projectPath, err := safeProjectPath(projectsDir, req.Name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if entries, err := os.ReadDir(projectPath); err == nil && len(entries) > 0 {
			http.Error(w, "project already exists", http.StatusConflict)
			return
		} else if err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := os.MkdirAll(projectPath, 0755); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Generate initial files based on tool
		gen := generator.ForTool(req.Tool)
		if err := gen.WriteScaffold(projectPath); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Start watching the project directory
		_ = fw.Watch(projectPath)

		_ = json.NewEncoder(w).Encode(map[string]string{
			"name": req.Name,
			"path": projectPath,
			"tool": req.Tool,
		})
	})

	// List registered blueprints (opinionated project layouts).
	// See internal/scaffold for the Blueprint interface and bundled blueprints.
	mux.HandleFunc("GET /api/blueprints", func(w http.ResponseWriter, r *http.Request) {
		type bpView struct {
			ID          string           `json:"id"`
			Name        string           `json:"name"`
			Description string           `json:"description"`
			Tool        string           `json:"tool"`
			Inputs      []scaffold.Input `json:"inputs"`
		}
		list := scaffold.Default.List()
		out := make([]bpView, 0, len(list))
		for _, bp := range list {
			out = append(out, bpView{
				ID:          bp.ID(),
				Name:        bp.Name(),
				Description: bp.Description(),
				Tool:        bp.Tool(),
				Inputs:      bp.Inputs(),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	// Render a blueprint into a new project directory.
	// Body: {"name": "...", "values": {...blueprint-specific inputs...}}
	// The "name" doubles as the project directory name; "values.project_name"
	// is auto-filled from "name" when not explicitly set.
	mux.HandleFunc("POST /api/blueprints/{id}/render", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		id := r.PathValue("id")
		bp, ok := scaffold.Default.Get(id)
		if !ok {
			http.Error(w, "unknown blueprint: "+id, 404)
			return
		}
		var req struct {
			Name   string         `json:"name"`
			Values map[string]any `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Values == nil {
			req.Values = map[string]any{}
		}
		if _, has := req.Values["project_name"]; !has {
			// safeProjectPath accepts underscores and mixed case for directory
			// names, but blueprints apply stricter rules (lowercase + hyphens)
			// on project_name since it lands inside HCL and cloud resource
			// identifiers. Normalise here so a valid-on-disk name doesn't
			// unexpectedly fail blueprint validation.
			req.Values["project_name"] = strings.ReplaceAll(strings.ToLower(req.Name), "_", "-")
		}

		projectPath, err := safeProjectPath(projectsDir, req.Name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		// Render first so an input-validation error (400) never leaves an
		// empty project directory behind. Only create the directory once we
		// know we have files to write.
		files, err := bp.Render(req.Values)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := os.MkdirAll(projectPath, 0755); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := scaffold.Write(projectPath, files); err != nil {
			// Map scaffold error kinds to meaningful HTTP status codes.
			//  - Existing file / symlinked root: 409 Conflict (precondition).
			//  - Blueprint bug (duplicate or invalid emitted path): 500.
			//  - Anything else (I/O, permissions): 500.
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, scaffold.ErrConflict),
				errors.Is(err, scaffold.ErrSymlinkInRoot):
				status = http.StatusConflict
			case errors.Is(err, scaffold.ErrInvalidPath),
				errors.Is(err, scaffold.ErrDuplicatePath):
				status = http.StatusInternalServerError
			}
			http.Error(w, err.Error(), status)
			return
		}

		_ = fw.Watch(projectPath)

		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":      req.Name,
			"path":      projectPath,
			"blueprint": bp.ID(),
			"tool":      bp.Tool(),
			"files":     paths,
		})
	})

	// Parse project files and return resource graph
	mux.HandleFunc("GET /api/projects/{name}/resources", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		tool := r.URL.Query().Get("tool")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Pulumi parsing isn't implemented — parser.ForTool falls
		// back to HCL and would silently return zero resources for an
		// index.ts project, leading the user to believe the canvas is
		// in sync when it isn't. Reject explicitly until a TS-AST
		// parser lands; flat HCL/Ansible projects keep their existing
		// behaviour.
		if tool == "pulumi" {
			http.Error(w, "pulumi resource parsing is not supported yet — edit index.ts directly", 400)
			return
		}

		p := parser.ForTool(tool)
		resources, err := p.ParseDir(projectPath)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(resources)
	})

	// Sync resources from UI to disk
	mux.HandleFunc("POST /api/projects/{name}/sync", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		tool := r.URL.Query().Get("tool")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Pulumi generator is project-shaped (per-env directories with
		// index.ts/Pulumi.yaml), not single-file like HCL. Calling
		// generator.ForTool here would fall through to HCL and write
		// main.tf at the project root — silently shadowing the
		// scaffolded environments/<env>/index.ts. Reject until an
		// AST-aware sync that round-trips through TS lands.
		if tool == "pulumi" {
			http.Error(w, "pulumi sync is not supported yet — edit environments/<env>/index.ts directly", 400)
			return
		}

		limitBody(w, r)
		var body struct {
			Resources []parser.Resource `json:"resources"`
			Code      *string           `json:"code,omitempty"`
			File      string            `json:"file,omitempty"`
			Edges     []struct {
				From  string `json:"from"`  // source node ID
				To    string `json:"to"`    // target node ID
				Field string `json:"field"` // connection field (e.g., "vpc_id")
			} `json:"edges"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", 400)
			return
		}

		gen := generator.ForTool(tool)
		ext := gen.FileExtension()
		allowedExts := allowedGeneratedExtensions(tool, ext)

		if body.Code != nil {
			target := body.File
			if target == "" {
				target = filepath.Join(projectPath, "main"+ext)
			}
			safeTarget, pathErr := safeProjectFile(projectPath, target, allowedExts...)
			if pathErr != nil {
				http.Error(w, "invalid code file: "+pathErr.Error(), 400)
				return
			}

			// Pause watcher to avoid echo
			fw.Pause(projectPath)
			defer fw.Resume(projectPath)

			// Invalidate plan gate — code changed, previous plan is stale
			planGate.mu.Lock()
			delete(planGate.plans, projectPath)
			planGate.mu.Unlock()

			tmpFile := safeTarget + ".tmp"
			if err := os.WriteFile(tmpFile, []byte(*body.Code), 0644); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.Rename(tmpFile, safeTarget); err != nil {
				_ = os.Remove(tmpFile) // best-effort cleanup on failure
				http.Error(w, err.Error(), 500)
				return
			}
			responseFile, relErr := filepath.Rel(projectPath, safeTarget)
			if relErr != nil || responseFile == "." || strings.HasPrefix(responseFile, "..") {
				responseFile = filepath.Base(safeTarget)
			}

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"file": responseFile,
				"code": *body.Code,
			})
			return
		}
		resources := body.Resources

		// Materialize edges into resource properties so the generator knows
		// exactly which target instance to reference (not just "first of type").
		if len(body.Edges) > 0 {
			// Build node ID -> resource index
			idIndex := make(map[string]int)
			for i, r := range resources {
				idIndex[r.ID] = i
			}
			for _, edge := range body.Edges {
				fromIdx, fromOK := idIndex[edge.From]
				toIdx, toOK := idIndex[edge.To]
				if fromOK && toOK {
					if resources[fromIdx].Properties == nil {
						resources[fromIdx].Properties = make(map[string]interface{})
					}
					// Store the exact target name so the generator references the right resource
					resources[fromIdx].Properties["__edge_"+edge.Field] = resources[toIdx].Name
				}
			}
		}

		code, err := gen.Generate(resources)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Pause watcher to avoid echo
		fw.Pause(projectPath)
		defer fw.Resume(projectPath)

		// Invalidate plan gate — code changed, previous plan is stale
		planGate.mu.Lock()
		delete(planGate.plans, projectPath)
		planGate.mu.Unlock()

		// Group resources by source file so we write back to original files.
		// Resources without a source file go to main.tf/main.yml.
		fileGroups := make(map[string][]parser.Resource)
		for _, r := range resources {
			target := r.File
			if target == "" {
				target = filepath.Join(projectPath, "main"+ext)
			}
			safeTarget, pathErr := safeProjectFile(projectPath, target, allowedExts...)
			if pathErr != nil {
				http.Error(w, "invalid resource file: "+pathErr.Error(), 400)
				return
			}
			fileGroups[safeTarget] = append(fileGroups[safeTarget], r)
		}

		// If all resources have no file origin, write to main file
		if len(fileGroups) == 0 {
			mainFile, pathErr := safeProjectFile(projectPath, filepath.Join(projectPath, "main"+ext), allowedExts...)
			if pathErr != nil {
				http.Error(w, "invalid main file: "+pathErr.Error(), 400)
				return
			}
			fileGroups[mainFile] = resources
		}

		// Read preserved blocks from existing files (variables, outputs, etc.)
		p := parser.ForTool(tool)
		preservedByFile := make(map[string][]parser.PreservedBlock)
		projectHasProvider := false
		if hclParser, ok := p.(*parser.HCLParser); ok && tool != "ansible" {
			existingFiles, _ := filepath.Glob(filepath.Join(projectPath, "*.tf"))
			for _, f := range existingFiles {
				result, err := hclParser.ParseFileFull(f)
				if err != nil || result == nil {
					continue
				}
				absFile, pathErr := safeProjectFile(projectPath, f, allowedExts...)
				if pathErr != nil {
					continue
				}
				preservedByFile[absFile] = result.PreservedBlocks
				for _, b := range result.PreservedBlocks {
					if b.Type == "provider" {
						projectHasProvider = true
						break
					}
				}
			}
		}

		// Write each file atomically (temp file + rename)
		var mainCode string
		rootMainFile, pathErr := safeProjectFile(projectPath, filepath.Join(projectPath, "main"+ext), allowedExts...)
		if pathErr != nil {
			http.Error(w, "invalid main file: "+pathErr.Error(), 400)
			return
		}
		for file, fileResources := range fileGroups {
			includeProviders := !projectHasProvider && file == rootMainFile
			fileCode, err := generateForSync(gen, fileResources, includeProviders)
			if err != nil {
				continue
			}

			// Prepend preserved blocks for this file
			if blocks := preservedByFile[file]; len(blocks) > 0 {
				var preserved strings.Builder
				for _, b := range blocks {
					preserved.WriteString(b.Content)
					preserved.WriteString("\n\n")
				}
				fileCode = preserved.String() + fileCode
			}

			// Atomic write: write to temp file, then rename
			tmpFile := file + ".tmp"
			if err := os.WriteFile(tmpFile, []byte(fileCode), 0644); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.Rename(tmpFile, file); err != nil {
				_ = os.Remove(tmpFile) // best-effort cleanup on failure
				http.Error(w, err.Error(), 500)
				return
			}

			if file == rootMainFile {
				mainCode = fileCode
			}
		}

		// If mainCode is empty, use the full generated code
		if mainCode == "" {
			mainCode = code
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"file": filepath.Join(projectPath, "main"+ext),
			"code": mainCode,
		})
	})

	// Run IaC command (init, plan, apply)
	mux.HandleFunc("POST /api/projects/{name}/run", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		limitBody(w, r)
		var req struct {
			Tool     string `json:"tool"`
			Command  string `json:"command"`  // init | plan | apply | check | playbook
			Approved bool   `json:"approved"` // must be true for apply/destroy
			// Acknowledged explicitly overrides the policy gate — the caller
			// is telling us they've read the findings and still want to
			// proceed. Logged server-side so the override is audit-trailable.
			Acknowledged bool `json:"acknowledged"`
			// Env names the environment subdirectory to execute in for
			// layered-v1 projects (environments/<env>/...). Empty runs
			// commands at the project root (flat layout). Validated as a
			// safe path segment so a bad value can't traverse.
			Env string `json:"env,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// When Env is set, rebase projectPath into environments/<env>
		// so the runner finds Pulumi.yaml / main.tf in the right
		// working directory. The subdir must exist and be contained
		// in projectPath — safeSubdir below rejects traversal and
		// rejects paths that point at a file instead of a directory.
		if req.Env != "" {
			subPath, subErr := safeSubdir(projectPath, "environments", req.Env)
			if subErr != nil {
				http.Error(w, "invalid env: "+subErr.Error(), 400)
				return
			}
			projectPath = subPath
		}

		// Block apply/destroy unless:
		// 1. A plan was run for this project within the last hour (server-verified)
		// 2. The client explicitly confirms (approved:true)
		// 3. No error-severity policy findings exist, OR the client sets
		//    acknowledged:true after reading the findings.
		if run.RequiresApproval(req.Command) {
			if !hasPlan(projectPath) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":  "plan_required",
					"detail": "run plan first — no plan has been run for this project recently",
				})
				return
			}
			if !req.Approved {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":  "approval_required",
					"detail": "plan exists — re-submit with approved:true to proceed",
				})
				return
			}
			if !req.Acknowledged {
				// Pulumi has no parser or plan-JSON path today, so the
				// builtin policies see zero resources and plan-based
				// engines have no input — every Pulumi mutating
				// command would silently bypass policy. Fail closed:
				// require the caller to opt in with acknowledged:true
				// so the override is explicit + audit-trailable. When
				// a Pulumi parser/policy adapter lands this branch
				// goes away.
				if req.Tool == "pulumi" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error":  "policy_unsupported",
						"detail": "policy evaluation is not implemented for pulumi yet — re-submit with acknowledged:true to proceed without server-side policy checks",
					})
					return
				}
				// Walk every available engine against the project so we can
				// surface blocking findings before the apply runs. On any
				// error (engine crash, missing binary, malformed plan) we
				// fall through to execution — apply should not be gated by
				// a broken policy engine.
				if findings, blocking := evaluateBlockingPolicies(r.Context(), projectPath, req.Tool); blocking {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error":    "policy_blocked",
						"detail":   "policy engine returned error-severity findings — re-submit with acknowledged:true to override",
						"findings": findings,
					})
					return
				}
			} else {
				log.Printf("apply gate: policy findings acknowledged by client for %s (command=%s tool=%s)", name, req.Command, req.Tool)
			}
		}

		// Execute in background. Use context.Background() — not r.Context() —
		// because the HTTP handler returns 202 immediately, which would cancel
		// a request-scoped context and kill the command. SafeRunner applies its
		// own per-command timeout (init=5m, plan=10m, apply=30m).
		go func() {
			result, err := run.Execute(context.Background(), projectPath, req.Tool, req.Command, req.Env)
			// Only record a successful plan — failed/cancelled plans don't count.
			// 'preview' is Pulumi's equivalent of terraform plan; without it
			// here, a pulumi up following a successful preview would be
			// blocked with 'plan_required'. projectPath already reflects
			// the env rebase so dev + prod track their plan state
			// independently.
			if err == nil && (req.Command == "plan" || req.Command == "preview" || req.Command == "check") {
				recordPlan(projectPath)
			}
			msg := map[string]interface{}{
				"type":    "terminal",
				"project": name,
			}
			if result != nil {
				msg["output"] = result.Output
				msg["status"] = result.Status
				msg["duration"] = result.Duration.String()
			}
			if err != nil {
				msg["error"] = err.Error()
			}
			data, _ := json.Marshal(msg)
			hub.Broadcast(data)
		}()

		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "running"})
	})

	// Kill a running command
	mux.HandleFunc("POST /api/projects/{name}/kill", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		// SafeRunner keys active executions by the exact workdir the
		// run handler passed in. When /run was invoked with env set,
		// that workdir was rebased to environments/<env> — so kill
		// must be able to rebase the same way to find the execution.
		// Env is optional on kill; an empty body still works for
		// project-root runs.
		limitBody(w, r)
		var req struct {
			Env string `json:"env,omitempty"`
		}
		// A missing body (EOF) is fine — kill defaults to the project
		// root. Any other decode failure is a client error; treating
		// it as "no env" would silently target the wrong execution.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}
		if req.Env != "" {
			sub, subErr := safeSubdir(projectPath, "environments", req.Env)
			if subErr != nil {
				http.Error(w, "invalid env: "+subErr.Error(), 400)
				return
			}
			projectPath = sub
		}

		if err := run.Kill(projectPath); err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	})

	// AI chat
	mux.HandleFunc("POST /api/ai/chat", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Populate RAG context when a project is named + indexed. Failures
		// are swallowed — chat degrades to ungrounded rather than erroring.
		if req.Project != "" {
			if projectPath, err := safeProjectPath(projectsDir, req.Project); err == nil {
				req.ProjectContext = sharedRAG.Context(r.Context(), projectPath, req.Message, 5)
			}
		}

		response, resources, err := aiClient.GenerateIaC(r.Context(), req)
		if err != nil {
			log.Printf("AI unavailable, using pattern matching: %v", err)
			response, resources = ai.PatternMatch(req.Message, req.Tool, req.Provider)
		}

		// Also return suggestions for what to add next
		suggestions := ai.SuggestNext(req.Tool, req.Provider, req.Canvas)

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"message":     response,
			"resources":   resources,
			"suggestions": suggestions,
		})
	})

	// Streaming AI chat via Server-Sent Events.
	// Event types emitted:
	//   - "delta"     — {text: "..."}        for every incremental chunk
	//   - "complete"  — {message, resources, suggestions}  on successful finish
	//   - "error"     — {error: "..."}       when the provider call fails
	// The non-streaming /api/ai/chat handler above is retained so older
	// clients keep working; new clients should prefer this endpoint.
	mux.HandleFunc("POST /api/ai/chat/stream", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Grounding on the project's own code — same degrade-silent path
		// as the non-streaming endpoint.
		if req.Project != "" {
			if projectPath, err := safeProjectPath(projectsDir, req.Project); err == nil {
				req.ProjectContext = sharedRAG.Context(r.Context(), projectPath, req.Message, 5)
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported by this server", 500)
			return
		}
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			http.Error(w, "failed to enable streaming", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // bypass nginx/proxy buffering
		w.WriteHeader(http.StatusOK)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		writeEvent := func(event string, payload any) error {
			data, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}

		var streamErr error
		onDelta := func(chunk string) {
			if streamErr != nil {
				return
			}
			if err := writeEvent("delta", map[string]string{"text": chunk}); err != nil {
				streamErr = err
				cancel()
			}
		}

		response, resources, err := aiClient.StreamChat(ctx, req, onDelta)
		if streamErr != nil || errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		if err != nil {
			// Notify clients that the provider stream failed using the
			// standard non-terminal error event so they can continue waiting
			// for the deterministic fallback completion below.
			// A write failure here just means the client already disconnected,
			// in which case the fallback below is wasted work but harmless.
			log.Printf("AI stream failed, falling back to pattern match: %v", err)
			_ = writeEvent("error", map[string]string{"error": err.Error()})

			// Fall back to deterministic pattern matching so users aren't
			// left hanging when the provider is unreachable, matching the
			// non-streaming handler's behaviour.
			response, resources = ai.PatternMatch(req.Message, req.Tool, req.Provider)
		}
		suggestions := ai.SuggestNext(req.Tool, req.Provider, req.Canvas)

		if err := writeEvent("complete", map[string]interface{}{
			"message":     response,
			"resources":   resources,
			"suggestions": suggestions,
		}); err != nil {
			cancel()
			return
		}
	})

	// Smart resource suggestions based on canvas state
	mux.HandleFunc("POST /api/ai/suggest", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Tool     string              `json:"tool"`
			Provider string              `json:"provider"`
			Canvas   []ai.CanvasResource `json:"canvas"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		suggestions := ai.SuggestNext(req.Tool, req.Provider, req.Canvas)
		_ = json.NewEncoder(w).Encode(suggestions)
	})

	// Analyze plan/apply output and suggest fixes
	mux.HandleFunc("POST /api/ai/fix", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.PlanFixRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		fix, err := aiClient.AnalyzePlanOutput(r.Context(), req)
		if err != nil {
			log.Printf("AI unavailable for plan fix, using fallback: %v", err)
			fix = ai.AnalyzePlanFallback(req.Output, req.ExitCode)
		}

		_ = json.NewEncoder(w).Encode(fix)
	})

	// ─── Project State Persistence ───

	pm := project.NewManager(projectsDir)

	// List all projects with their saved state
	mux.HandleFunc("GET /api/projects/states", func(w http.ResponseWriter, _ *http.Request) {
		states, err := pm.ListAll()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(states)
	})

	// Load project state (canvas positions, edges, tool)
	mux.HandleFunc("GET /api/projects/{name}/state", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, err := safeProjectPath(projectsDir, name); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		state, err := pm.Load(name)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if state == nil {
			_ = json.NewEncoder(w).Encode(nil)
			return
		}
		_ = json.NewEncoder(w).Encode(state)
	})

	// Save project state
	mux.HandleFunc("PUT /api/projects/{name}/state", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		limitBody(w, r)
		var state project.State
		if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		state.Name = name
		state.Path = projectPath
		if err := pm.Save(name, &state); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
	})

	// Open project directory in OS file manager
	mux.HandleFunc("POST /api/projects/{name}/reveal", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if _, err := os.Stat(projectPath); os.IsNotExist(err) {
			http.Error(w, "project directory not found", 404)
			return
		}
		// Detect OS and open file manager
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", projectPath)
		case "windows":
			cmd = exec.Command("explorer", projectPath)
		default: // linux
			cmd = exec.Command("xdg-open", projectPath)
		}
		if err := cmd.Start(); err != nil {
			http.Error(w, fmt.Sprintf("failed to open: %v", err), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened", "path": projectPath})
	})

	// Delete a project (removes directory and state)
	mux.HandleFunc("DELETE /api/projects/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Remove state from manager — best-effort, directory removal is the source of truth.
		_ = pm.Delete(name)
		// Remove the project directory
		if err := os.RemoveAll(projectPath); err != nil {
			http.Error(w, fmt.Sprintf("failed to delete: %v", err), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})
	})

	// ─── AI Settings ───

	// Get current AI provider config
	mux.HandleFunc("GET /api/ai/settings", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(aiClient.GetConfig())
	})

	isLikelyMaskedAPIKey := func(apiKey string) bool {
		return apiKey != "" && (strings.Contains(apiKey, "*") || strings.Contains(apiKey, "•"))
	}

	// Update AI provider config (supports Ollama, OpenAI-compatible, and Anthropic).
	// Type is validated explicitly so a user selecting "anthropic" in the UI
	// isn't silently downgraded to the OpenAI path just because they supplied
	// an API key.
	getConfiguredProviderAPIKey := func(kind providers.Kind, cfg ai.ProviderConfig) string {
		if apiKey := strings.TrimSpace(cfg.APIKey); apiKey != "" {
			return apiKey
		}
		switch kind {
		case providers.KindOpenAI:
			return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		case providers.KindAnthropic:
			return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		default:
			return ""
		}
	}

	hasConfiguredProviderAPIKey := func(kind providers.Kind, cfg ai.ProviderConfig) bool {
		return getConfiguredProviderAPIKey(kind, cfg) != ""
	}

	resolveRequestedAPIKey := func(kind providers.Kind, submitted string, cfg ai.ProviderConfig) (string, error) {
		currentAPIKey := getConfiguredProviderAPIKey(kind, cfg)
		hasExistingAPIKey := hasConfiguredProviderAPIKey(kind, cfg)

		if submitted == "" {
			if hasExistingAPIKey {
				// Keep the existing configured key, regardless of whether it was
				// persisted directly in config or supplied via environment.
				return currentAPIKey, nil
			}
			return "", nil
		}

		if isLikelyMaskedAPIKey(submitted) {
			if hasExistingAPIKey {
				// Treat a masked placeholder as "no change".
				return currentAPIKey, nil
			}
			return "", errors.New("api key placeholder submitted; provide a new api key instead of the masked value")
		}

		return submitted, nil
	}

	mux.HandleFunc("PUT /api/ai/settings", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.ProviderConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		req.Type = strings.TrimSpace(req.Type)
		if req.Type == "custom" {
			req.Type = string(providers.KindOpenAI)
		}
		req.Model = strings.TrimSpace(req.Model)
		req.Endpoint = strings.TrimSpace(req.Endpoint)
		req.APIKey = strings.TrimSpace(req.APIKey)

		currentCfg := aiClient.GetConfig()

		if req.Model == "" {
			http.Error(w, "model is required", 400)
			return
		}
		// Only providers with a known built-in public default may omit an
		// endpoint. Others must provide one explicitly.
		kind := providers.Kind(req.Type)
		if kind == "" {
			currentKind := providers.Kind(strings.TrimSpace(currentCfg.Type))
			if currentKind != "" {
				kind = currentKind
			} else if req.APIKey != "" {
				kind = providers.KindOpenAI
			} else {
				kind = providers.KindOllama
			}
		}

		resolvedAPIKey, err := resolveRequestedAPIKey(kind, req.APIKey, currentCfg)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		req.APIKey = resolvedAPIKey

		switch kind {
		case providers.KindOllama:
			if req.Endpoint == "" {
				http.Error(w, "endpoint is required for ollama", 400)
				return
			}
		case providers.KindOpenAI:
			if req.APIKey == "" && !hasConfiguredProviderAPIKey(kind, currentCfg) {
				http.Error(w, "api key is required for openai", 400)
				return
			}
			// endpoint optional — provider falls back to the public OpenAI default.
		case providers.KindAnthropic:
			if req.APIKey == "" && !hasConfiguredProviderAPIKey(kind, currentCfg) {
				http.Error(w, "api key is required for anthropic", 400)
				return
			}
			// endpoint optional — provider falls back to a public default.
		default:
			http.Error(w, "unsupported provider type: "+req.Type, 400)
			return
		}
		aiClient.UpdateConfigKind(kind, req.Endpoint, req.Model, req.APIKey)
		_ = json.NewEncoder(w).Encode(aiClient.GetConfig())
	})

	// ─── Import & Filesystem Browser ───

	// Browse local filesystem directories
	mux.HandleFunc("GET /api/browse", func(w http.ResponseWriter, r *http.Request) {
		dir := r.URL.Query().Get("path")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = home
		}
		entries, err := importer.BrowseDir(dir)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Include parent path for navigation
		parent := filepath.Dir(dir)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"path":    dir,
			"parent":  parent,
			"entries": entries,
		})
	})

	// Scan and import an existing project directory
	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Path == "" {
			http.Error(w, "path is required", 400)
			return
		}

		project, err := importer.ScanProject(req.Path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		// Start watching the imported project directory
		_ = fw.Watch(req.Path)

		_ = json.NewEncoder(w).Encode(project)
	})

	// AI topology builder — runs async, sends progress via WebSocket, returns result via HTTP
	mux.HandleFunc("POST /api/ai/topology", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.TopologyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Send immediate acknowledgment
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "generating"})

		// Run AI generation in background, broadcast result via WebSocket
		go func() {
			// Send progress indicator
			progressMsg, _ := json.Marshal(map[string]string{
				"type":    "ai_progress",
				"status":  "generating",
				"message": "AI is designing your infrastructure...",
			})
			hub.Broadcast(progressMsg)

			msg, resources, err := aiClient.GenerateTopology(context.Background(), req)

			result := map[string]interface{}{
				"type": "ai_topology_result",
			}
			if err != nil {
				result["error"] = err.Error()
				result["message"] = fmt.Sprintf("Topology generation failed: %v", err)
			} else {
				result["message"] = msg
				result["resources"] = resources
			}
			data, _ := json.Marshal(result)
			hub.Broadcast(data)
		}()
	})

	// ─── Security Scanner ───

	secScanner := security.New()

	mux.HandleFunc("POST /api/projects/{name}/security", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		tool := r.URL.Query().Get("tool")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		p := parser.ForTool(tool)
		resources, err := p.ParseDir(projectPath)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		report := secScanner.Scan(resources)
		_ = json.NewEncoder(w).Encode(report)
	})

	// Security scan from canvas resources (no project dir needed)
	mux.HandleFunc("POST /api/security/scan", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var resources []parser.Resource
		if err := json.NewDecoder(r.Body).Decode(&resources); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		report := secScanner.Scan(resources)
		_ = json.NewEncoder(w).Encode(report)
	})

	// ─── Drift Detection ───

	driftDetector := drift.New()

	mux.HandleFunc("POST /api/projects/{name}/drift", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		tool := r.URL.Query().Get("tool")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		p := parser.ForTool(tool)
		resources, err := p.ParseDir(projectPath)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Build code resource index
		codeResources := make(map[string]map[string]interface{})
		for _, res := range resources {
			codeResources[res.Type+"."+res.Name] = res.Properties
		}
		report, err := driftDetector.Detect(projectPath, codeResources)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(report)
	})

	// ─── Multi-Format Export ───

	exp := exporter.New()

	mux.HandleFunc("GET /api/export/formats", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(exp.SupportedFormats())
	})

	mux.HandleFunc("POST /api/export", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Format    string            `json:"format"`
			Resources []parser.Resource `json:"resources"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		result, err := exp.Export(req.Format, req.Resources)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	// WebSocket for live sync
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	})

	// Policy engines — builtin + OPA (embedded) + Conftest + Sentinel (shell-out).
	registerPolicyRoutes(mux, projectsDir)

	// Security scanner plugins — graph + Checkov + Trivy + Terrascan + KICS.
	registerScannerRoutes(mux, projectsDir)

	// Terraform modules — introspect local modules + proxy the registry.
	regClient := registry.New(registry.Config{})
	registerModuleRoutes(mux, projectsDir, regClient)

	// AI agent — tool-use orchestrator that drives list_resources, run_policy,
	// run_scan, write_hcl, etc. against the configured Anthropic provider.
	registerAgentRoutes(mux, projectsDir, aiClient, regClient)

	// RAG — build & query a per-project embedding index so chat / topology
	// / fix responses are grounded on the project's own code instead of
	// generic best-practice knowledge.
	registerRAGRoutes(mux, projectsDir, aiClient)

	// Vision — diagram-to-topology endpoint; multimodal provider call
	// through Anthropic's image content blocks.
	registerVisionRoutes(mux, aiClient)

	return mux
}

// allowedOrigins is populated at startup from the server's actual bind address.
var allowedOrigins = map[string]bool{}

// serverPort stores the configured port for same-origin checks.
var serverPort string

// isWildcardBind is true when the server binds to 0.0.0.0 or [::].
var isWildcardBind bool

// InitAllowedOrigins builds the origin allowlist from the server's host and port.
// Called once at startup so the list matches the actual deployment.
func InitAllowedOrigins(host string, port int) {
	serverPort = fmt.Sprintf("%d", port)
	isWildcardBind = host == "0.0.0.0" || host == "::" || host == ""

	// Always allow localhost variants
	for _, h := range []string{"localhost", "127.0.0.1"} {
		allowedOrigins["http://"+h+":"+serverPort] = true
	}
	// If binding a specific host, allow that too
	if !isWildcardBind {
		allowedOrigins["http://"+host+":"+serverPort] = true
	}
	// Also allow the Vite dev server (port 5173) for development
	for _, h := range []string{"localhost", "127.0.0.1"} {
		allowedOrigins["http://"+h+":5173"] = true
	}
}

// IsAllowedOrigin checks whether an origin is in the allowlist.
// When the server binds to 0.0.0.0, browsers send the LAN IP as the origin
// (e.g. http://192.168.1.5:3000). We can't predict all LAN IPs at startup,
// so for wildcard binds we also accept any origin whose port matches the
// configured server port — the same trust level as listening on all interfaces.
func IsAllowedOrigin(origin string) bool {
	if allowedOrigins[origin] {
		return true
	}
	if isWildcardBind && strings.HasPrefix(origin, "http://") && strings.HasSuffix(origin, ":"+serverPort) {
		return true
	}
	return false
}

// CORS restricts cross-origin requests to the localhost allowlist.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && IsAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
