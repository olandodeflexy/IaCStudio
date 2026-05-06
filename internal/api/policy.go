package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/policy/engines"
	"github.com/iac-studio/iac-studio/internal/policy/engines/conftest"
	"github.com/iac-studio/iac-studio/internal/policy/engines/crossguard"
	"github.com/iac-studio/iac-studio/internal/policy/engines/opa"
	"github.com/iac-studio/iac-studio/internal/policy/engines/sentinel"
	pulumigen "github.com/iac-studio/iac-studio/internal/pulumi"
)

// defaultPolicyEngines is the list of engines registered on startup. Order
// controls the UI's default presentation (builtin first because it's always
// available; OPA next because it ships embedded; shell-out engines last).
func defaultPolicyEngines() []engines.PolicyEngine {
	return []engines.PolicyEngine{
		engines.NewBuiltin(),
		opa.New(),
		conftest.New(),
		sentinel.New(),
		crossguard.New(),
	}
}

// policyRunRequest is the request shape for POST /api/projects/{name}/policy/run.
// PlanJSON, when provided, is used directly. When empty, the handler falls
// back to tfplan.json in the active IaC workdir. In layered projects, that is
// environments/<env>. When neither is available, engines that require
// plan JSON surface a clear error on their Result.
type policyRunRequest struct {
	PlanJSON json.RawMessage `json:"plan_json,omitempty"`
	// EngineFilter, when non-empty, limits the run to the named engines.
	// Unknown names are ignored (not an error).
	EngineFilter []string `json:"engines,omitempty"`
	// Tool selects the parser to use for the resource walk. Defaults to
	// terraform.
	Tool string `json:"tool,omitempty"`
	// Env names a layered-v1 environment. When set, resources and plan JSON
	// are loaded from environments/<env>, while policy bundles are discovered
	// from the owning project root.
	Env string `json:"env,omitempty"`
}

// policyRunResponse returns per-engine Result plus a merged Findings list
// already sorted blocking-first so the UI can render a single feed.
type policyRunResponse struct {
	Results  []engines.Result  `json:"results"`
	Findings []engines.Finding `json:"findings"`
	// Blocking is true when any finding has error severity — callers that
	// want to gate apply on policy violations can check this flag instead
	// of re-scanning findings.
	Blocking bool `json:"blocking"`
}

// registerPolicyRoutes wires the policy endpoints onto mux. Exposed as a
// helper rather than inlined in NewRouter so the policy surface stays
// discoverable and testable.
func registerPolicyRoutes(mux *http.ServeMux, projectsDir string) {
	engs := defaultPolicyEngines()

	// Lists every registered engine plus its availability. The UI uses this
	// to show "Conftest: not installed" hints next to greyed-out toggles.
	mux.HandleFunc("GET /api/policy/engines", func(w http.ResponseWriter, r *http.Request) {
		type engineView struct {
			Name      string `json:"name"`
			Available bool   `json:"available"`
		}
		out := make([]engineView, 0, len(engs))
		for _, e := range engs {
			out = append(out, engineView{Name: e.Name(), Available: e.Available()})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	// Runs every registered engine (optionally filtered) against the project
	// and returns a unified findings feed.
	mux.HandleFunc("POST /api/projects/{name}/policy/run", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		var req policyRunRequest
		// A missing body (EOF) is fine — it means "use on-disk plan + all
		// engines". Malformed JSON, however, is a client bug and should 400
		// instead of silently falling back to defaults.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}

		tool := effectiveProjectTool(projectPath, req.Tool, req.Env)
		if tool == "multi" {
			http.Error(w, "env is required when running policy for hybrid projects", 400)
			return
		}
		projectRoot := projectPath
		workDir := projectPath
		if req.Env != "" {
			subPath, subErr := safeSubdir(projectPath, "environments", req.Env)
			if subErr != nil {
				http.Error(w, "invalid env: "+subErr.Error(), 400)
				return
			}
			workDir = subPath
		}

		planJSON := []byte(req.PlanJSON)
		if len(planJSON) == 0 {
			// Fall back to the active workdir's tfplan.json. Layered
			// Terraform/hybrid scripts write one under environments/<env>.
			// Missing is not an error here — plan-less engines still run.
			if data, err := os.ReadFile(filepath.Join(workDir, "tfplan.json")); err == nil {
				planJSON = data
			}
		}
		resources := parsePolicyResources(workDir, tool)

		selected := filterEngines(engs, req.EngineFilter)
		results := engines.RunAll(r.Context(), selected, engines.EvalInput{
			ProjectDir: projectRoot,
			WorkDir:    workDir,
			Resources:  resources,
			PlanJSON:   planJSON,
		})
		findings := engines.MergeFindings(results)

		blocking := false
		for _, f := range findings {
			if f.Severity.IsBlocking() {
				blocking = true
				break
			}
		}

		_ = json.NewEncoder(w).Encode(policyRunResponse{
			Results:  results,
			Findings: findings,
			Blocking: blocking,
		})
	})
}

// filterEngines returns the subset of engs whose Name matches one of the
// names in filter. An empty filter returns the full list unchanged.
func filterEngines(engs []engines.PolicyEngine, filter []string) []engines.PolicyEngine {
	if len(filter) == 0 {
		return engs
	}
	wanted := make(map[string]struct{}, len(filter))
	for _, n := range filter {
		wanted[n] = struct{}{}
	}
	out := make([]engines.PolicyEngine, 0, len(engs))
	for _, e := range engs {
		if _, ok := wanted[e.Name()]; ok {
			out = append(out, e)
		}
	}
	return out
}

// evaluateBlockingPolicies runs every registered engine against the project
// and reports whether any error-severity findings exist. Returns the merged
// finding list (blocking-first) so the caller can surface it verbatim to the
// user, and a boolean for a quick gate check.
//
// Plan JSON and resources are loaded from workDir, while policy bundles are
// discovered from projectRoot. That keeps layered/hybrid env plans scoped to
// environments/<env> without hiding root-level policies from OPA, Conftest,
// and Sentinel.
//
// Any engine error is swallowed here — apply should not be gated by a
// broken policy engine. The caller can still observe non-blocking findings
// via POST /api/projects/{name}/policy/run if needed.
func evaluateBlockingPolicies(ctx context.Context, projectRoot, workDir, tool string) ([]engines.Finding, bool) {
	if tool == "" {
		tool = "terraform"
	}
	if workDir == "" {
		workDir = projectRoot
	}
	resources := parsePolicyResources(workDir, tool)

	var planJSON []byte
	if data, err := os.ReadFile(filepath.Join(workDir, "tfplan.json")); err == nil {
		planJSON = data
	}

	results := engines.RunAll(ctx, defaultPolicyEngines(), engines.EvalInput{
		ProjectDir: projectRoot,
		WorkDir:    workDir,
		Resources:  resources,
		PlanJSON:   planJSON,
	})
	findings := engines.MergeFindings(results)
	for _, f := range findings {
		if f.Severity.IsBlocking() {
			return findings, true
		}
	}
	return findings, false
}

func parsePolicyResources(workDir, tool string) []parser.Resource {
	var (
		resources []parser.Resource
		err       error
	)
	if tool == "pulumi" {
		resources, err = (&pulumigen.TSParser{}).ParseDir(workDir)
	} else if p := parser.ForTool(tool); p != nil {
		resources, err = p.ParseDir(workDir)
	}
	if err != nil {
		return nil
	}
	return resources
}
