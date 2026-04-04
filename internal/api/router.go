package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/generator"
	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/runner"
	"github.com/iac-studio/iac-studio/internal/watcher"
)

// NewRouter creates the HTTP router with all endpoints.
func NewRouter(hub *Hub, fw *watcher.FileWatcher, aiClient *ai.OllamaClient, run *runner.Runner, projectsDir string) *http.ServeMux {
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
		var req struct {
			Name string `json:"name"`
			Tool string `json:"tool"` // terraform | opentofu | ansible
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		projectPath := filepath.Join(projectsDir, req.Name)
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
		projectPath := filepath.Join(projectsDir, name)

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
		projectPath := filepath.Join(projectsDir, name)

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
		projectPath := filepath.Join(projectsDir, name)

		var req struct {
			Tool    string `json:"tool"`
			Command string `json:"command"` // init | plan | apply | check | playbook
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}

		// Stream output via WebSocket
		go func() {
			output, err := run.Execute(projectPath, req.Tool, req.Command)
			msg := map[string]interface{}{
				"type":    "terminal",
				"project": name,
				"output":  output,
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

	// AI chat
	mux.HandleFunc("POST /api/ai/chat", func(w http.ResponseWriter, r *http.Request) {
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

// CORS wraps a handler with permissive CORS headers for local development.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
