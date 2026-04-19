// Package scanners is the multi-scanner IaC security surface.
//
// A Scanner takes a ScanInput (project directory, parsed resources) and
// produces a Result of [security.Finding] values. The interface is
// deliberately minimal so a wide range of tools fit:
//
//   - the built-in graph scanner (no external binary, walks cross-resource
//     attack paths — the differentiated capability we already had);
//   - Checkov / Trivy / Terrascan / KICS shelled out against the project
//     directory, each with its own JSON output format normalised to the
//     shared Finding shape.
//
// Scanners that don't apply to the current project return an empty Result
// without error — for example, Checkov returns nothing when run against a
// directory with no .tf files.
//
// All scanners flow into the same Findings slice so the API, UI, and
// potential CI export can reason about results uniformly.
package scanners

import (
	"context"
	"sort"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/security"
)

// Finding is the engine-agnostic representation used by every scanner, aliased
// to the existing security.Finding so the API response shape doesn't change.
type Finding = security.Finding

// Severity string constants match the values the existing security package
// already emits so aggregating across scanners keeps a single vocabulary.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"
)

// IsBlocking reports whether a finding with this severity should gate apply.
// "critical" and "high" both block; "medium" and below are surfaced but do
// not gate. The threshold is opinionated — callers that want a stricter or
// looser bar can read severity directly.
func IsBlocking(sev string) bool {
	return sev == SeverityCritical || sev == SeverityHigh
}

// Result is what one scanner returns from a single Scan call.
type Result struct {
	Scanner   string    `json:"scanner"`             // "graph" | "checkov" | "trivy" | ...
	Available bool      `json:"available"`           // false → scanner present but not runnable (e.g. binary missing)
	Findings  []Finding `json:"findings,omitempty"`
	Error     string    `json:"error,omitempty"`     // surface-able error message
}

// HasBlocking is a quick check callers use to gate apply without re-scanning
// the findings.
func (r Result) HasBlocking() bool {
	for _, f := range r.Findings {
		if IsBlocking(f.Severity) {
			return true
		}
	}
	return false
}

// ScanInput is the data every scanner receives. Scanners use whichever fields
// they need and ignore the rest — the built-in graph scanner walks Resources
// directly, while shell-out scanners walk ProjectDir.
type ScanInput struct {
	// ProjectDir is the absolute path to the project root. Shell-out
	// scanners (Checkov, Trivy, …) point their CLI here.
	ProjectDir string
	// Resources is the parsed resource graph. The built-in graph scanner
	// consumes this directly.
	Resources []parser.Resource
	// Tool narrows what's on disk ("terraform" | "opentofu" | "ansible").
	// Scanners that only target Terraform use this to short-circuit on
	// non-Terraform projects rather than failing with noisy output.
	Tool string
}

// Scanner is the contract every scanner implementation satisfies.
type Scanner interface {
	// Name returns the stable identifier ("graph", "checkov", "trivy", …).
	Name() string
	// Available reports whether the scanner can actually run in the current
	// environment. Shell-out scanners return false when their CLI isn't on
	// PATH; the built-in graph scanner is always available.
	Available() bool
	// Scan runs the scanner against the input. It must always return a
	// Result populated with at least Scanner + Available. Missing-binary
	// scenarios are NOT errors — they surface via Available=false plus
	// Result.Error. Reserve the error return for truly unexpected failures
	// (malformed output from the CLI, I/O errors, etc.).
	Scan(ctx context.Context, in ScanInput) (Result, error)
}

// RunAll evaluates the input against every scanner in order and returns one
// Result per scanner. A scanner that fails still produces a Result so the UI
// can render "Checkov crashed" alongside findings from other scanners.
func RunAll(ctx context.Context, scanners []Scanner, in ScanInput) []Result {
	out := make([]Result, 0, len(scanners))
	for _, s := range scanners {
		r, err := s.Scan(ctx, in)
		if r.Scanner == "" {
			r.Scanner = s.Name()
		}
		if err != nil && r.Error == "" {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out
}

// MergeFindings flattens results into a single fully-ordered finding slice.
// The sort is layered so ties are broken deterministically across runs:
// severity → scanner → id → title → resources.
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
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return resourceKey(a) < resourceKey(b)
	})
	return all
}

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityHigh:
		return 1
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 3
	case SeverityInfo:
		return 4
	}
	return 5
}

// resourceKey returns the first affected resource address for stable sorting.
// Findings with no resources sort last in their severity band.
func resourceKey(f Finding) string {
	if len(f.Resources) == 0 {
		return "\uffff" // sorts after real resource addresses
	}
	return f.Resources[0]
}
