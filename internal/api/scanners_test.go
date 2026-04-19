package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/security/scanners"
)

// scaffoldScannerProject writes a tiny project tree with the given HCL — all
// we need for the API-level tests to exercise the route and the parser.
func scaffoldScannerProject(t *testing.T, hcl string) string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "demo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	return root
}

// scannerMux wires just the scanner routes onto a fresh mux — enough for
// endpoint-level tests without the full NewRouter stack.
func scannerMux(projectsDir string) *http.ServeMux {
	mux := http.NewServeMux()
	registerScannerRoutes(mux, projectsDir)
	return mux
}

// TestScannersListReportsAvailability — every registered scanner shows up
// and the graph scanner always reports Available=true.
func TestScannersListReportsAvailability(t *testing.T) {
	srv := httptest.NewServer(scannerMux(t.TempDir()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/security/scanners")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out []struct {
		Name      string `json:"name"`
		Available bool   `json:"available"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	avail := map[string]bool{}
	for _, s := range out {
		avail[s.Name] = s.Available
	}
	for _, want := range []string{"graph", "checkov", "kics", "terrascan", "trivy"} {
		if _, ok := avail[want]; !ok {
			t.Errorf("scanner %q missing from response: %+v", want, out)
		}
	}
	if !avail["graph"] {
		t.Error("graph scanner must always be available")
	}
}

// TestScannersRunGraphOnly — an exposed S3 bucket triggers the graph
// scanner's cross-resource checks; the response should surface those
// findings with Blocking=true if severity is high/critical.
func TestScannersRunGraphOnly(t *testing.T) {
	hcl := `resource "aws_s3_bucket" "public_data" {
  bucket = "demo"
  acl    = "public-read"
}
`
	root := scaffoldScannerProject(t, hcl)
	srv := httptest.NewServer(scannerMux(root))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"scanners": []string{"graph"}})
	resp, err := http.Post(srv.URL+"/api/projects/demo/security/scanners/run", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var got scannersRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Scanner != "graph" {
		t.Fatalf("expected a single graph result, got %+v", got.Results)
	}
	if len(got.Findings) == 0 {
		t.Fatal("graph scanner should have produced findings for public-read bucket")
	}
	// Findings list is sorted severity-first so the UI renders a stable feed.
	if got.Findings[0].Severity == scanners.SeverityInfo {
		t.Errorf("first finding should not be info when exposed buckets are present: %+v", got.Findings[0])
	}
}

// TestScannersRunUnknownFilterDropsSilently — an unknown scanner name in the
// filter should NOT 400 (filters are informational) — it just drops that
// name and runs the rest.
func TestScannersRunUnknownFilterDropsSilently(t *testing.T) {
	root := scaffoldScannerProject(t, `# empty`)
	srv := httptest.NewServer(scannerMux(root))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"scanners": []string{"made-up", "graph"}})
	resp, err := http.Post(srv.URL+"/api/projects/demo/security/scanners/run", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var got scannersRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Scanner != "graph" {
		t.Errorf("expected only graph result, got: %+v", got.Results)
	}
}

// TestScannersRunMalformedBody400 — guards the contract we agreed with
// reviewers: empty body is fine, malformed JSON is a client bug that 400s.
func TestScannersRunMalformedBody400(t *testing.T) {
	root := scaffoldScannerProject(t, `# empty`)
	srv := httptest.NewServer(scannerMux(root))
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/api/projects/demo/security/scanners/run", "application/json", strings.NewReader("{not json"))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 400 {
		t.Errorf("malformed JSON should 400, got %d", resp.StatusCode)
	}
}

// TestCIWorkflowExportServesYAML — the generator endpoint returns a non-empty
// YAML body with the right Content-Type and filename.
func TestCIWorkflowExportServesYAML(t *testing.T) {
	srv := httptest.NewServer(scannerMux(t.TempDir()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/security/scanners/ci-workflow")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/yaml") {
		t.Errorf("Content-Type should be text/yaml, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "name: IaC Security Scan") {
		t.Errorf("workflow body missing expected header:\n%s", string(body))
	}
	// Every scanner we ship should have a step in the emitted workflow.
	for _, tool := range []string{"Checkov", "Trivy", "Terrascan", "KICS"} {
		if !strings.Contains(string(body), tool) {
			t.Errorf("workflow missing step for %s", tool)
		}
	}
}
