package scanners

import (
	"context"

	"github.com/iac-studio/iac-studio/internal/security"
)

// graphScanner wraps the legacy internal/security.Scanner — the graph-based
// cross-resource attack-path analyzer — behind the Scanner interface so it
// runs alongside the shell-out adapters in a unified multi-scanner pass.
//
// It's the one scanner that's genuinely unique to IaC Studio (others are
// thin wrappers around third-party tools), so we keep it registered first
// and always-available.
type graphScanner struct {
	inner *security.Scanner
}

// NewGraph constructs the default-built-in graph scanner. No arguments because
// the underlying Scanner is stateless.
func NewGraph() Scanner {
	return &graphScanner{inner: security.New()}
}

func (g *graphScanner) Name() string    { return "graph" }
func (g *graphScanner) Available() bool { return true }

func (g *graphScanner) Scan(_ context.Context, in ScanInput) (Result, error) {
	res := Result{Scanner: g.Name(), Available: true}
	if len(in.Resources) == 0 {
		// Empty project → nothing to analyse. Not an error — keeps the
		// multi-scanner pass quiet on fresh projects.
		return res, nil
	}
	report := g.inner.Scan(in.Resources)
	res.Findings = append(res.Findings, report.Findings...)
	return res, nil
}
