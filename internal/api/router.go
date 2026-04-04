package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/generator"
	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/runner"
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
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "0.1.0"})
	})

	// List available IaC tools detected on this machine
	mux.HandleFunc("GET /api/tools", func(w http.ResponseWriter, r *http.Request) {
		tools := run.DetectTools()
		json.NewEncoder(w).Encode(tools)
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
		json.NewEncoder(w).Encode(projects)
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
		fw.Watch(projectPath)

		json.NewEncoder(w).Encode(map[string]string{
			"name": req.Name,
			"path": projectPath,
			"tool": req.Tool,
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
		json.NewEncoder(w).Encode(resources)
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
		var resources []parser.Resource
		if err := json.NewDecoder(r.Body).Decode(&resources); err != nil {
			http.Error(w, "invalid resources", 400)
			return
		}

		gen := generator.ForTool(tool)
		code, err := gen.Generate(resources)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		ext := gen.FileExtension()
		mainFile := filepath.Join(projectPath, "main"+ext)

		// Pause watcher to avoid echo
		fw.Pause(projectPath)
		defer fw.Resume(projectPath)

		if err := os.WriteFile(mainFile, []byte(code), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"file": mainFile,
			"code": code,
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
			Tool    string `json:"tool"`
			Command string `json:"command"` // init | plan | apply | check | playbook
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Block apply/destroy behind approval gate
		if run.RequiresApproval(req.Command) {
			http.Error(w, "apply/destroy requires plan review first — use /plan-review then /run with approved=true", http.StatusConflict)
			return
		}

		// Execute with context cancellation and timeouts via SafeRunner
		go func() {
			result, err := run.Execute(r.Context(), projectPath, req.Tool, req.Command)
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
		json.NewEncoder(w).Encode(map[string]string{"status": "running"})
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
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	})

	// AI chat
	mux.HandleFunc("POST /api/ai/chat", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		var req struct {
			Message string `json:"message"`
			Tool    string `json:"tool"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		response, resources, err := aiClient.GenerateIaC(r.Context(), req.Message, req.Tool)
		if err != nil {
			// Fallback to pattern matching if AI is unavailable
			log.Printf("AI unavailable, using pattern matching: %v", err)
			response, resources = ai.PatternMatch(req.Message, req.Tool)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"message":   response,
			"resources": resources,
		})
	})

	// WebSocket for live sync
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r)
	})

	return mux
}

// allowedOrigins are the only origins accepted for CORS and WebSocket.
// These cover the default dev and production bind addresses.
var allowedOrigins = map[string]bool{
	"http://localhost:3000":   true,
	"http://127.0.0.1:3000":  true,
	"http://localhost:5173":   true, // Vite dev server
	"http://127.0.0.1:5173":  true,
}

// IsAllowedOrigin checks whether an origin is in the allowlist.
func IsAllowedOrigin(origin string) bool {
	return allowedOrigins[origin]
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
