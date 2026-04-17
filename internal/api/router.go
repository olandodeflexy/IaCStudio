package api

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/iac-studio/iac-studio/internal/catalog"
	"github.com/iac-studio/iac-studio/internal/drift"
	"github.com/iac-studio/iac-studio/internal/exporter"
	"github.com/iac-studio/iac-studio/internal/generator"
	"github.com/iac-studio/iac-studio/internal/importer"
	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/project"
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
	absProjects, _ := filepath.Abs(projectsDir)
	absResolved, _ := filepath.Abs(resolved)
	// If the directory already exists, resolve symlinks in the actual path
	if evalResolved, err := filepath.EvalSymlinks(resolved); err == nil {
		absResolved, _ = filepath.Abs(evalResolved)
	}
	if !strings.HasPrefix(absResolved, absProjects+string(filepath.Separator)) {
		return "", fmt.Errorf("project path escapes root: %q", name)
	}
	return resolved, nil
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
func NewRouter(hub *Hub, fw *watcher.FileWatcher, aiClient *ai.OllamaClient, run *runner.SafeRunner, projectsDir string) *http.ServeMux {
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
			req.Values["project_name"] = req.Name
		}

		projectPath, err := safeProjectPath(projectsDir, req.Name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := os.MkdirAll(projectPath, 0755); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		files, err := bp.Render(req.Values)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := scaffold.Write(projectPath, files); err != nil {
			http.Error(w, err.Error(), 409)
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

		limitBody(w, r)
		var body struct {
			Resources []parser.Resource `json:"resources"`
			Edges     []struct {
				From  string `json:"from"`       // source node ID
				To    string `json:"to"`         // target node ID
				Field string `json:"field"`      // connection field (e.g., "vpc_id")
			} `json:"edges"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", 400)
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

		gen := generator.ForTool(tool)
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
		ext := gen.FileExtension()
		fileGroups := make(map[string][]parser.Resource)
		for _, r := range resources {
			target := r.File
			if target == "" {
				target = filepath.Join(projectPath, "main"+ext)
			}
			fileGroups[target] = append(fileGroups[target], r)
		}

		// If all resources have no file origin, write to main file
		if len(fileGroups) == 0 {
			fileGroups[filepath.Join(projectPath, "main"+ext)] = resources
		}

		// Read preserved blocks from existing files (variables, outputs, etc.)
		p := parser.ForTool(tool)
		if hclParser, ok := p.(*parser.HCLParser); ok && tool != "ansible" {
			existingFiles, _ := filepath.Glob(filepath.Join(projectPath, "*.tf"))
			for _, f := range existingFiles {
				result, err := hclParser.ParseFileFull(f)
				if err != nil || result == nil {
					continue
				}
				// If this file has preserved blocks but no resources being written to it,
				// don't touch it — leave it as-is
				if _, hasResources := fileGroups[f]; !hasResources && len(result.PreservedBlocks) > 0 {
					continue
				}
			}
		}

		// Write each file atomically (temp file + rename)
		var mainCode string
		for file, fileResources := range fileGroups {
			fileCode, err := gen.Generate(fileResources)
			if err != nil {
				continue
			}

			// Prepend preserved blocks for this file
			if hclParser, ok := p.(*parser.HCLParser); ok && tool != "ansible" {
				result, err := hclParser.ParseFileFull(file)
				if err == nil && result != nil && len(result.PreservedBlocks) > 0 {
					var preserved strings.Builder
					for _, b := range result.PreservedBlocks {
						preserved.WriteString(b.Content)
						preserved.WriteString("\n\n")
					}
					fileCode = preserved.String() + fileCode
				}
			}

			// Atomic write: write to temp file, then rename
			tmpFile := file + ".tmp"
			if err := os.WriteFile(tmpFile, []byte(fileCode), 0644); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.Rename(tmpFile, file); err != nil {
				os.Remove(tmpFile) // cleanup on failure
				http.Error(w, err.Error(), 500)
				return
			}

			if strings.HasSuffix(file, "main"+ext) {
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
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Block apply/destroy unless:
		// 1. A plan was run for this project within the last hour (server-verified)
		// 2. The client explicitly confirms (approved:true)
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
		}

		// Execute in background. Use context.Background() — not r.Context() —
		// because the HTTP handler returns 202 immediately, which would cancel
		// a request-scoped context and kill the command. SafeRunner applies its
		// own per-command timeout (init=5m, plan=10m, apply=30m).
		go func() {
			result, err := run.Execute(context.Background(), projectPath, req.Tool, req.Command)
			// Only record a successful plan — failed/cancelled plans don't count
			if err == nil && (req.Command == "plan" || req.Command == "check") {
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

	// Smart resource suggestions based on canvas state
	mux.HandleFunc("POST /api/ai/suggest", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Tool     string             `json:"tool"`
			Provider string             `json:"provider"`
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
		limitBody(w, r)
		var state project.State
		if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		state.Name = name
		state.Path = filepath.Join(projectsDir, name)
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

	// Update AI provider config (supports Ollama and OpenAI-compatible APIs)
	mux.HandleFunc("PUT /api/ai/settings", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req ai.ProviderConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Endpoint == "" || req.Model == "" {
			http.Error(w, "endpoint and model are required", 400)
			return
		}
		aiClient.UpdateConfig(req.Endpoint, req.Model, req.APIKey)
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
