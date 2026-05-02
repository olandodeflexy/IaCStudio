// Package engines is the multi-engine policy evaluation surface for IaC Studio.
//
// A PolicyEngine takes an EvalInput (project directory, parsed resources,
// optional Terraform plan JSON) and produces a Result of structured Findings.
// The interface is intentionally minimal so a wide range of engines can fit:
//
//   - the built-in Go rules engine (fast, no dependencies, walks resources);
//   - OPA/Rego evaluated in-process (walks plan JSON);
//   - Conftest shelled out (also walks plan JSON, against the same Rego files);
//   - HashiCorp Sentinel shelled out (walks plan JSON via tfplan/v2 import);
//   - Pulumi CrossGuard shelled out through `pulumi preview --policy-pack`.
//
// Engines that don't apply to a given input return an empty Result without
// error — for example, the Sentinel adapter returns nothing when no .sentinel
// files exist under policies/sentinel.
//
// All engines flow into the same Findings slice so the API and Plan→Apply gate
// can reason about violations uniformly.
package engines

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// Severity describes how serious a finding is. The string values match the
// existing internal/policy.Rule severities so reports stay comparable across
// the built-in engine and the new pluggable adapters.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// IsBlocking reports whether a finding of this severity should block apply.
// Only "error" blocks today; warnings and info are surfaced but do not gate.
func (s Severity) IsBlocking() bool { return s == SeverityError }

// Finding is the engine-agnostic representation of a single policy violation.
// It carries enough context for the UI to deep-link to the offending policy
// file (Engine + PolicyFile) and the offending HCL resource (Resource).
type Finding struct {
	Engine     string   `json:"engine"`      // "builtin" | "opa" | "conftest" | "sentinel" | "crossguard"
	PolicyID   string   `json:"policy_id"`   // stable id ("aws-s3-encryption", "terraform.tags.deny", …)
	PolicyName string   `json:"policy_name"` // human-readable name
	Severity   Severity `json:"severity"`
	Category   string   `json:"category,omitempty"` // tagging|encryption|networking|access|naming|cost|compliance
	Resource   string   `json:"resource,omitempty"` // "aws_s3_bucket.data" if known
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`  // optional remediation hint
	PolicyFile string   `json:"policy_file,omitempty"` // path to the source rule file (Rego/Sentinel)
}

// Result is what one engine returns from a single Evaluate call.
type Result struct {
	Engine    string    `json:"engine"`
	Available bool      `json:"available"` // false → engine present but not runnable (e.g. binary missing)
	Findings  []Finding `json:"findings,omitempty"`
	Error     string    `json:"error,omitempty"` // surface-able error (binary missing, parse failure, etc.)
}

// HasBlocking returns true when at least one finding has error severity.
func (r Result) HasBlocking() bool {
	for _, f := range r.Findings {
		if f.Severity.IsBlocking() {
			return true
		}
	}
	return false
}

// EvalInput is the data every engine receives. Engines pick whichever fields
// they care about and ignore the rest — the built-in walks Resources, OPA and
// Conftest walk PlanJSON, Sentinel walks PlanJSON via its own importer, and
// CrossGuard runs Pulumi preview against local policy packs.
type EvalInput struct {
	// ProjectDir is the absolute path to the project root. Engines that load
	// policy bundles from disk (OPA, Conftest, Sentinel) resolve their files
	// relative to here — typically policies/opa/*.rego, policies/sentinel/*.sentinel.
	ProjectDir string
	// Resources is the parsed resource graph from the project's .tf/.yml
	// files. The built-in Go engine walks this directly.
	Resources []parser.Resource
	// PlanJSON is the output of `terraform show -json tfplan` (or empty when
	// no plan is available). Engines that operate on plan changes (OPA,
	// Conftest, Sentinel) require this; without it they short-circuit with a
	// clear error so callers know to run plan first.
	PlanJSON json.RawMessage
}

// PolicyEngine is the contract every adapter implements. Concrete engines
// live in internal/policy/engines/{builtin,opa,conftest,sentinel,crossguard}.
type PolicyEngine interface {
	// Name returns the stable identifier ("builtin", "opa", "conftest", …).
	Name() string
	// Available reports whether the engine can actually run in the current
	// environment — e.g. the conftest adapter returns false when the binary
	// isn't on PATH. Callers can show this in the UI as "Conftest: not
	// installed" rather than treating it as a hard error.
	Available() bool
	// Evaluate runs the engine against the input. It must always return a
	// Result populated with Engine + Available; an error is reserved for
	// truly unexpected failures (the engine ran but its output was unparsable,
	// for example). "Engine not installed" is NOT an error — it surfaces via
	// Result.Available=false plus Result.Error.
	Evaluate(ctx context.Context, in EvalInput) (Result, error)
}

// RunAll evaluates the input against every engine in order and returns one
// merged Result per engine. Engines that fail with an error still produce a
// Result so the UI can show "OPA crashed" alongside other engines' findings.
func RunAll(ctx context.Context, engs []PolicyEngine, in EvalInput) []Result {
	out := make([]Result, 0, len(engs))
	for _, e := range engs {
		r, err := e.Evaluate(ctx, in)
		if r.Engine == "" {
			r.Engine = e.Name()
		}
		if err != nil && r.Error == "" {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out
}

// MergeFindings flattens results into a single fully-ordered finding slice.
// The sort is stable and layered: severity → engine → policy id → policy file
// → resource → message. The extra tie-breakers matter when, for example, the
// same Rego rule fires on multiple resource_changes entries (same severity,
// engine, and policy id) — without them the relative order would depend on
// evaluation order and flip between runs.
func MergeFindings(results []Result) []Finding {
	var all []Finding
	for _, r := range results {
		all = append(all, r.Findings...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Severity != b.Severity {
			return severityRank(a.Severity) < severityRank(b.Severity)
		}
		if a.Engine != b.Engine {
			return a.Engine < b.Engine
		}
		if a.PolicyID != b.PolicyID {
			return a.PolicyID < b.PolicyID
		}
		if a.PolicyFile != b.PolicyFile {
			return a.PolicyFile < b.PolicyFile
		}
		if a.Resource != b.Resource {
			return a.Resource < b.Resource
		}
		return a.Message < b.Message
	})
	return all
}

func severityRank(s Severity) int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	}
	return 3
}
