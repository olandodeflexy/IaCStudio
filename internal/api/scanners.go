package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/security/scanners"
	"github.com/iac-studio/iac-studio/internal/security/scanners/checkov"
	"github.com/iac-studio/iac-studio/internal/security/scanners/kics"
	"github.com/iac-studio/iac-studio/internal/security/scanners/terrascan"
	"github.com/iac-studio/iac-studio/internal/security/scanners/trivy"
)

// defaultSecurityScanners is the list of scanners registered on startup.
// Order controls the UI's default presentation — graph first because it's
// always available and IaC Studio's differentiated capability; the wrapped
// third-party tools follow alphabetically so the list is stable.
func defaultSecurityScanners() []scanners.Scanner {
	return []scanners.Scanner{
		scanners.NewGraph(),
		checkov.New(),
		kics.New(),
		terrascan.New(),
		trivy.New(),
	}
}

// scannersRunRequest is the body for POST /api/projects/{name}/security/scanners/run.
// ScannerFilter, when non-empty, limits the run to the named scanners.
// Unknown names are dropped silently — they're informational (maybe the UI
// is behind a server upgrade that added a scanner), not a client bug.
type scannersRunRequest struct {
	ScannerFilter []string `json:"scanners,omitempty"`
	Tool          string   `json:"tool,omitempty"`
}

// scannersRunResponse carries the per-scanner Results plus a merged
// Findings feed already sorted severity-first so the UI renders a single
// list without re-sorting.
type scannersRunResponse struct {
	Results  []scanners.Result  `json:"results"`
	Findings []scanners.Finding `json:"findings"`
	Blocking bool               `json:"blocking"`
}

// registerScannerRoutes wires the scanner endpoints onto mux. Called from
// NewRouter so the HTTP surface is discovered in one place.
func registerScannerRoutes(mux *http.ServeMux, projectsDir string) {
	scans := defaultSecurityScanners()

	// Lists every registered scanner plus its availability. The UI uses this
	// to render "Checkov: not installed" hints next to greyed-out toggles.
	mux.HandleFunc("GET /api/security/scanners", func(w http.ResponseWriter, r *http.Request) {
		type scannerView struct {
			Name      string `json:"name"`
			Available bool   `json:"available"`
		}
		out := make([]scannerView, 0, len(scans))
		for _, s := range scans {
			out = append(out, scannerView{Name: s.Name(), Available: s.Available()})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	// Runs every registered scanner (optionally filtered) against the project
	// and returns a unified Findings feed.
	mux.HandleFunc("POST /api/projects/{name}/security/scanners/run", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		var req scannersRunRequest
		// Empty body (EOF) is fine — means "run all scanners, parse code for
		// resources". Malformed JSON is a client bug that should 400.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}

		tool := req.Tool
		if tool == "" {
			tool = "terraform"
		}
		var resources []parser.Resource
		if p := parser.ForTool(tool); p != nil {
			if parsed, perr := p.ParseDir(projectPath); perr == nil {
				resources = parsed
			}
		}

		selected := filterScanners(scans, req.ScannerFilter)
		results := scanners.RunAll(r.Context(), selected, scanners.ScanInput{
			ProjectDir: projectPath,
			Resources:  resources,
			Tool:       tool,
		})
		findings := scanners.MergeFindings(results)

		blocking := false
		for _, f := range findings {
			if scanners.IsBlocking(f.Severity) {
				blocking = true
				break
			}
		}

		_ = json.NewEncoder(w).Encode(scannersRunResponse{
			Results:  results,
			Findings: findings,
			Blocking: blocking,
		})
	})

	// Generates a .github/workflows/iac-scan.yml that runs the same scanner
	// set in CI. Keeps local and CI output identical so CI doesn't flag
	// something users haven't already seen locally.
	mux.HandleFunc("GET /api/security/scanners/ci-workflow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="iac-scan.yml"`)
		_, _ = io.WriteString(w, ciWorkflowYAML)
	})
}

// filterScanners returns the subset of scans whose Name matches one of the
// names in filter. An empty filter returns the full list unchanged. Unknown
// filter entries are dropped silently.
func filterScanners(scans []scanners.Scanner, filter []string) []scanners.Scanner {
	if len(filter) == 0 {
		return scans
	}
	wanted := make(map[string]struct{}, len(filter))
	for _, n := range filter {
		wanted[n] = struct{}{}
	}
	out := make([]scanners.Scanner, 0, len(scans))
	for _, s := range scans {
		if _, ok := wanted[s.Name()]; ok {
			out = append(out, s)
		}
	}
	return out
}

// ciWorkflowYAML is the GitHub Actions workflow template emitted by
// /api/security/scanners/ci-workflow. Runs each scanner independently with
// continue-on-error so one missing CLI doesn't block the rest, then uploads
// each tool's native report as an artifact for auditors.
const ciWorkflowYAML = `# Generated by IaC Studio — runs the same scanner set the studio does locally.
# Drop into .github/workflows/iac-scan.yml and commit to enforce parity between
# local development and CI.

name: IaC Security Scan

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

jobs:
  scan:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Checkov
        uses: bridgecrewio/checkov-action@master
        continue-on-error: true
        with:
          directory: .
          output_format: json
          output_file_path: checkov-report.json
          soft_fail: true

      - name: Trivy (config / IaC)
        uses: aquasecurity/trivy-action@master
        continue-on-error: true
        with:
          scan-type: config
          scan-ref: .
          format: json
          output: trivy-report.json
          exit-code: '0'

      - name: Terrascan
        uses: tenable/terrascan-action@main
        continue-on-error: true
        with:
          iac_type: terraform
          only_warn: true

      - name: KICS
        uses: checkmarx/kics-github-action@master
        continue-on-error: true
        with:
          path: .
          output_path: kics-report
          output_formats: json
          fail_on: high

      - name: Upload reports
        uses: actions/upload-artifact@v4
        if: always()
        with:
          name: iac-security-reports
          path: |
            checkov-report.json
            trivy-report.json
            kics-report/
            terrascan.sarif.json
          if-no-files-found: ignore
`
