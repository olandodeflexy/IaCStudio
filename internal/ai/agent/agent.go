// Package agent is the multi-agent orchestrator — a small router that
// picks a specialist sub-agent (Architect / Coder / Policy / Security /
// Reviewer) for a given user prompt and runs the provider's tool-use
// loop on its behalf.
//
// "Multi-agent" here is a prompt-engineering pattern, not separate
// processes: one Provider, one tool Registry, different system prompts
// and tool subsets per specialist. The orchestrator uses a cheaper model
// (Haiku when available) to route and falls back to the single
// configured provider otherwise.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iac-studio/iac-studio/internal/ai/providers"
	"github.com/iac-studio/iac-studio/internal/ai/tools"
)

// runnerAdapter turns a tools.Runner into a providers.ToolRunner. Lives in
// this package (not tools or providers) so tools stays free of provider
// imports and providers stays free of tools imports.
type runnerAdapter struct {
	inner *tools.Runner
}

// Adapt wraps a tools.Runner so Anthropic's ToolLoopRequest can consume it.
// The conversion is mechanical: providers.ToolCall ↔ tools.ToolCall,
// providers.ToolResult ↔ tools.ToolResult.
func Adapt(r *tools.Runner) providers.ToolRunner {
	return &runnerAdapter{inner: r}
}

func (a *runnerAdapter) Run(ctx context.Context, calls []providers.ToolCall) ([]providers.ToolResult, error) {
	inCalls := make([]tools.ToolCall, len(calls))
	for i, c := range calls {
		inCalls[i] = tools.ToolCall{
			ID:   c.ID,
			Name: c.Name,
			Args: json.RawMessage(c.Args),
		}
	}
	results, err := a.inner.Run(ctx, inCalls)
	outResults := make([]providers.ToolResult, len(results))
	for i, r := range results {
		outResults[i] = providers.ToolResult{
			CallID:  r.CallID,
			Content: r.Content,
			IsError: r.IsError,
		}
	}
	return outResults, err
}

// definitionsFromTools maps the registry's Definitions into the neutral
// shape the provider accepts.
func definitionsFromTools(defs []tools.Definition) []providers.ToolDefinition {
	out := make([]providers.ToolDefinition, len(defs))
	for i, d := range defs {
		out[i] = providers.ToolDefinition{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: []byte(d.Schema),
		}
	}
	return out
}

// Specialist is one sub-agent. Name is what the orchestrator routes to;
// System is the specialist's system prompt; ToolAllowList (if non-empty)
// narrows which tools the specialist can see. Empty → all tools.
type Specialist struct {
	Name         string
	System       string
	ToolAllowList []string
}

// DefaultSpecialists returns the five built-in sub-agents. They share the
// same provider + registry — the differentiation is the system prompt and
// the tool subset. Specific system prompts are kept short and concrete:
// telling the model "you are an X" is far less effective than telling it
// "use list_resources then run_policy before proposing any write".
func DefaultSpecialists() []Specialist {
	return []Specialist{
		{
			Name: "architect",
			System: `You are the Architect sub-agent. Your job is to turn a user's
high-level infrastructure request into a concrete plan of resources and
modules. Process: (1) list_resources to see what already exists,
(2) search_registry to find an existing module before creating
resources from scratch, (3) propose files via write_hcl. Never invent
cloud resource types you aren't certain about.`,
			ToolAllowList: []string{"list_resources", "get_resource", "search_registry", "write_hcl"},
		},
		{
			Name: "coder",
			System: `You are the Coder sub-agent. You write or modify specific
Terraform / HCL resources on request. Process: (1) get_resource first
to read current state, (2) write_hcl with the full intended file
content, (3) call run_policy before declaring done.`,
			ToolAllowList: []string{"list_resources", "get_resource", "write_hcl", "run_policy"},
		},
		{
			Name: "policy",
			System: `You are the Policy sub-agent. Your only job is to explain and fix
policy violations. Call run_policy first, then for each blocking
finding either propose a write_hcl change that fixes it or clearly
state why it should be acknowledged as an accepted risk.`,
			ToolAllowList: []string{"list_resources", "get_resource", "run_policy", "write_hcl"},
		},
		{
			Name: "security",
			System: `You are the Security sub-agent. Call run_scan, then walk the
findings by severity (critical first). For each actionable finding,
either propose a write_hcl fix or flag it for human review. Don't
touch resources unrelated to the findings.`,
			ToolAllowList: []string{"list_resources", "get_resource", "run_scan", "write_hcl"},
		},
		{
			Name: "reviewer",
			System: `You are the Reviewer sub-agent. Read current resources + the plan
JSON and give a short critique: what's about to change, risky bits,
and whether the plan matches what the user asked for. Do not modify
files. Return a concise prose summary.`,
			ToolAllowList: []string{"list_resources", "get_resource", "run_policy", "run_scan", "read_plan"},
		},
	}
}

// Route maps a user prompt to a Specialist. Today this is a tiny keyword
// classifier — good enough to get the right tool subset most of the time.
// A later commit can swap in an LLM-based router if the keyword heuristic
// proves too blunt.
//
// The return guarantees a non-nil Specialist: ambiguous prompts default
// to "architect" since it has the broadest tool set.
func Route(userPrompt string, specialists []Specialist) *Specialist {
	p := strings.ToLower(userPrompt)
	type score struct {
		specialist *Specialist
		weight     int
	}
	scores := []score{}
	for i := range specialists {
		s := &specialists[i]
		w := 0
		switch s.Name {
		case "policy":
			w = keywordMatches(p, "policy", "compliance", "tag", "governance", "opa", "sentinel")
		case "security":
			w = keywordMatches(p, "security", "vulnerab", "cve", "exposure", "encrypt", "iam", "public")
		case "reviewer":
			w = keywordMatches(p, "review", "critique", "what will", "explain plan", "walk me through")
		case "coder":
			w = keywordMatches(p, "change", "modify", "update", "fix", "edit", "set")
		case "architect":
			w = keywordMatches(p, "add", "create", "build", "design", "new", "vpc", "cluster")
		}
		scores = append(scores, score{specialist: s, weight: w})
	}
	best := 0
	var winner *Specialist
	for _, s := range scores {
		if s.weight > best {
			best = s.weight
			winner = s.specialist
		}
	}
	if winner != nil {
		return winner
	}
	// Ambiguous prompt → architect, since it has the widest tool allow-list.
	for i := range specialists {
		if specialists[i].Name == "architect" {
			return &specialists[i]
		}
	}
	return &specialists[0]
}

func keywordMatches(text string, keywords ...string) int {
	n := 0
	for _, k := range keywords {
		if strings.Contains(text, k) {
			n++
		}
	}
	return n
}

// Config bundles everything Run needs. ToolRegistry is the full set of
// tools available to any specialist; Route picks one and the orchestrator
// narrows via ToolAllowList.
type Config struct {
	Provider     providers.Provider
	ToolRegistry *tools.Registry
	Specialists  []Specialist
	// MaxTurns caps the tool-use loop. Matches providers.ToolLoopRequest
	// defaulting (8) when zero.
	MaxTurns int
}

// Result is the orchestrator's output. Transcript captures the sub-agent
// path + final text; the HTTP endpoint serialises this into the response
// body. Keeps the audit trail terse — real per-tool logging is inside
// the providers/anthropic_tooluse.go loop if we ever need it.
type Result struct {
	Specialist string `json:"specialist"`
	Reply      string `json:"reply"`
	Turns      int    `json:"turns,omitempty"` // reserved for when the loop surfaces it
}

// Run dispatches one user prompt through the orchestrator. Errors are
// returned verbatim so the HTTP handler can decide status codes.
func Run(ctx context.Context, cfg Config, userPrompt string) (*Result, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("agent: no provider configured")
	}
	if cfg.ToolRegistry == nil {
		return nil, fmt.Errorf("agent: no tool registry configured")
	}
	specialists := cfg.Specialists
	if len(specialists) == 0 {
		specialists = DefaultSpecialists()
	}

	spec := Route(userPrompt, specialists)

	tu, ok := cfg.Provider.(providers.ToolUser)
	if !ok {
		return nil, fmt.Errorf("agent: provider %q does not support tool use — switch to Anthropic to enable the agent", cfg.Provider.Kind())
	}

	// Narrow the tool catalogue to the specialist's allow-list AND build
	// a filtered registry for the Runner to execute against. Advertising
	// fewer tools isn't enough: a model can guess the name of a
	// registered-but-hidden tool and call it anyway. Enforcing the
	// allow-list at execution time keeps the specialist inside its lane.
	allDefs := cfg.ToolRegistry.Definitions()
	execRegistry := cfg.ToolRegistry
	var defs []tools.Definition
	if len(spec.ToolAllowList) == 0 {
		defs = allDefs
	} else {
		allow := make(map[string]struct{}, len(spec.ToolAllowList))
		for _, n := range spec.ToolAllowList {
			allow[n] = struct{}{}
		}
		execRegistry = tools.NewRegistry()
		for _, d := range allDefs {
			if _, ok := allow[d.Name]; !ok {
				continue
			}
			// The registry has no "copy one tool" accessor on purpose —
			// Lookup returns the full Tool struct which is all Register
			// needs.
			if t, ok := cfg.ToolRegistry.Lookup(d.Name); ok {
				execRegistry.Register(t)
				defs = append(defs, d)
			}
		}
	}

	runner := &tools.Runner{Registry: execRegistry, MaxIterations: 50}
	reply, err := tu.RunToolLoop(ctx, providers.ToolLoopRequest{
		System:   spec.System,
		User:     userPrompt,
		Tools:    definitionsFromTools(defs),
		Runner:   Adapt(runner),
		MaxTurns: cfg.MaxTurns,
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Specialist: spec.Name,
		Reply:      reply,
	}, nil
}
