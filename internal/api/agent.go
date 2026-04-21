package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/ai/agent"
	"github.com/iac-studio/iac-studio/internal/ai/tools"
	"github.com/iac-studio/iac-studio/internal/policy/engines"
	iacregistry "github.com/iac-studio/iac-studio/internal/registry"
	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// agentRunRequest is the body of POST /api/projects/{name}/ai/agent.
// Prompt is what the user typed; MaxTurns bounds the tool-use loop.
type agentRunRequest struct {
	Prompt   string `json:"prompt"`
	MaxTurns int    `json:"max_turns,omitempty"`
}

// agentRunResponse echoes the agent.Result plus server-assigned metadata
// the UI wants for its audit log.
type agentRunResponse struct {
	Specialist string `json:"specialist"`
	Reply      string `json:"reply"`
	// DurationMs lets the UI render timing; the orchestrator itself
	// doesn't emit this since it depends on the HTTP boundary.
	DurationMs int64 `json:"duration_ms"`
}

// auditLog records every agent invocation server-side so an admin can
// replay what the agent did even after the UI's session is gone. It's
// intentionally minimal — the per-tool logs live in the provider and are
// noisy enough that we don't want them in the audit trail by default.
type auditEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Project    string    `json:"project"`
	Prompt     string    `json:"prompt"`
	Specialist string    `json:"specialist"`
	ReplyBytes int       `json:"reply_bytes"`
	Err        string    `json:"error,omitempty"`
	DurationMs int64     `json:"duration_ms"`
}

// agentAudit is an in-memory ring buffer of recent agent runs. Tiny and
// best-effort — we're not promising persistence. A future commit can
// write this to disk if operators want durability.
type agentAudit struct {
	mu      sync.Mutex
	entries []auditEntry
	maxKeep int
}

func newAgentAudit(maxKeep int) *agentAudit {
	if maxKeep <= 0 {
		maxKeep = 100
	}
	return &agentAudit{maxKeep: maxKeep}
}

func (a *agentAudit) record(e auditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	if len(a.entries) > a.maxKeep {
		a.entries = a.entries[len(a.entries)-a.maxKeep:]
	}
}

func (a *agentAudit) snapshot() []auditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]auditEntry, len(a.entries))
	copy(out, a.entries)
	return out
}

// registerAgentRoutes wires the agent endpoints. Called from NewRouter.
//
// The agent reuses the same policy engines + scanners + registry client
// registered elsewhere in the router so tool output matches what the
// per-endpoint routes return. That way users can compare "what the agent
// says" against "what /api/projects/X/policy/run returns" byte-for-byte.
func registerAgentRoutes(
	mux *http.ServeMux,
	projectsDir string,
	aiClient *ai.Client,
	regClient *iacregistry.Client,
) {
	audit := newAgentAudit(100)

	// POST /api/projects/{name}/ai/agent
	// Routes the prompt to a sub-agent and runs the tool-use loop against
	// the project. Returns the specialist used and the final text.
	mux.HandleFunc("POST /api/projects/{name}/ai/agent", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		var req agentRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}
		if req.Prompt == "" {
			http.Error(w, "prompt is required", 400)
			return
		}

		// Build a per-request tool registry scoped to this project so a
		// model can't leak reads/writes across projects in a shared
		// server.
		reg := tools.NewRegistry()
		tools.RegisterIaCTools(reg, tools.IaCToolDeps{
			ProjectDir:     projectPath,
			PolicyEngines:  defaultPolicyEngines(),
			Scanners:       defaultSecurityScanners(),
			RegistryClient: regClient,
		})

		start := time.Now()
		result, runErr := agent.Run(r.Context(), agent.Config{
			Provider:     aiClient.Provider(),
			ToolRegistry: reg,
			MaxTurns:     req.MaxTurns,
		}, req.Prompt)
		elapsed := time.Since(start)

		entry := auditEntry{
			Timestamp:  time.Now().UTC(),
			Project:    name,
			Prompt:     req.Prompt,
			DurationMs: elapsed.Milliseconds(),
		}
		if runErr != nil {
			entry.Err = runErr.Error()
			audit.record(entry)
			// 400 when the provider simply doesn't support tool use (user
			// picked Ollama and pressed the agent button); 502 for the
			// rest (transport / provider / loop errors) so the UI can
			// treat the two distinctly.
			status := http.StatusBadGateway
			if isClientError(runErr) {
				status = http.StatusBadRequest
			}
			http.Error(w, runErr.Error(), status)
			return
		}
		entry.Specialist = result.Specialist
		entry.ReplyBytes = len(result.Reply)
		audit.record(entry)

		log.Printf("agent: project=%s specialist=%s duration=%dms bytes=%d",
			name, result.Specialist, elapsed.Milliseconds(), len(result.Reply))

		_ = json.NewEncoder(w).Encode(agentRunResponse{
			Specialist: result.Specialist,
			Reply:      result.Reply,
			DurationMs: elapsed.Milliseconds(),
		})
	})

	// GET /api/ai/agent/audit
	// Returns the in-memory audit log — most-recent-last. Useful for
	// post-incident replay and for the UI's "recent agent runs" panel.
	mux.HandleFunc("GET /api/ai/agent/audit", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"entries": audit.snapshot(),
		})
	})
}

// isClientError distinguishes user-caused errors (picked a non-tool-use
// provider, misconfiguration) from transport/provider errors. The heuristic
// is good enough for 400-vs-502 routing; we don't need typed errors.
func isClientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{
		"does not support tool use",
		"no provider configured",
		"no tool registry configured",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// Compile-time checks that the packages we depend on still expose what we
// use — cheap way to surface a breaking rename at build time.
var (
	_ = engines.SeverityError
	_ = scanners.SeverityHigh
)
