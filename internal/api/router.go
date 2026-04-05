package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/catalog"
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
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "0.1.0"})
	})

	// List available IaC tools detected on this machine
	mux.HandleFunc("GET /api/tools", func(w http.ResponseWriter, r *http.Request) {
		tools := run.DetectTools()
		json.NewEncoder(w).Encode(tools)
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
		json.NewEncoder(w).Encode(cat)
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

		ext := gen.FileExtension()
		mainFile := filepath.Join(projectPath, "main"+ext)

		// Pause watcher to avoid echo
		fw.Pause(projectPath)
		defer fw.Resume(projectPath)

		// Invalidate plan gate — code changed, previous plan is stale
		planGate.mu.Lock()
		delete(planGate.plans, projectPath)
		planGate.mu.Unlock()

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
				json.NewEncoder(w).Encode(map[string]string{
					"error":  "plan_required",
					"detail": "run plan first — no plan has been run for this project recently",
				})
				return
			}
			if !req.Approved {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
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

		json.NewEncoder(w).Encode(map[string]interface{}{
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
		json.NewEncoder(w).Encode(suggestions)
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

		json.NewEncoder(w).Encode(fix)
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
