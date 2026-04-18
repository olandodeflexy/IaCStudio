package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
)

// scaffoldPolicyProject writes a tiny project tree with the given HCL + an
// optional tfplan.json + an optional policies/opa/*.rego file.
func scaffoldPolicyProject(t *testing.T, hcl string, planJSON string, rego string) string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "demo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	if planJSON != "" {
		if err := os.WriteFile(filepath.Join(project, "tfplan.json"), []byte(planJSON), 0o644); err != nil {
			t.Fatalf("write tfplan: %v", err)
		}
	}
	if rego != "" {
		if err := os.MkdirAll(filepath.Join(project, "policies", "opa"), 0o755); err != nil {
			t.Fatalf("mkdir policies: %v", err)
		}
		if err := os.WriteFile(filepath.Join(project, "policies", "opa", "policy.rego"), []byte(rego), 0o644); err != nil {
			t.Fatalf("write rego: %v", err)
		}
	}
	return root
}

// policyMux wires just the policy routes onto a fresh mux — enough for
// endpoint-level tests without spinning up the full NewRouter stack.
func policyMux(projectsDir string) *http.ServeMux {
	mux := http.NewServeMux()
	registerPolicyRoutes(mux, projectsDir)
	return mux
}

// TestPolicyEnginesEndpointReportsAvailability asserts every registered
// engine is listed and that the builtin always reports Available=true.
func TestPolicyEnginesEndpointReportsAvailability(t *testing.T) {
	srv := httptest.NewServer(policyMux(t.TempDir()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/policy/engines")
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
	names := map[string]bool{}
	for _, e := range out {
		names[e.Name] = e.Available
	}
	for _, want := range []string{"builtin", "opa", "conftest", "sentinel"} {
		if _, ok := names[want]; !ok {
			t.Errorf("engine %q missing from response: %+v", want, out)
		}
	}
	// The builtin engine requires no external binary.
	if !names["builtin"] {
		t.Error("builtin engine must report Available=true")
	}
}

// TestPolicyRunBuiltinOnly drives the run endpoint against a project with
// only the builtin engine being useful — validates resource-walk findings
// flow through to the response.
func TestPolicyRunBuiltinOnly(t *testing.T) {
	// An untagged S3 bucket — the builtin engine's tag-required rule fires.
	hcl := `resource "aws_s3_bucket" "data" {
  bucket = "demo"
}
`
	root := scaffoldPolicyProject(t, hcl, "", "")
	srv := httptest.NewServer(policyMux(root))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"engines": []string{"builtin"}})
	resp, err := http.Post(srv.URL+"/api/projects/demo/policy/run", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var got policyRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Engine != "builtin" {
		t.Fatalf("expected a single builtin result, got %+v", got.Results)
	}
	if len(got.Findings) == 0 {
		t.Fatal("builtin engine should have found at least one violation on untagged bucket")
	}
	if !got.Blocking {
		t.Error("tag-required violations are error severity → Blocking should be true")
	}
	// Findings must be sorted blocking-first by MergeFindings.
	if got.Findings[0].Severity != engines.SeverityError {
		t.Errorf("first finding should be error-severity, got %s", got.Findings[0].Severity)
	}
}

// TestPolicyRunReadsOnDiskPlanJSON verifies the fallback path: no plan in
// the request body, but a tfplan.json sitting in the project root is picked
// up by the embedded OPA engine.
func TestPolicyRunReadsOnDiskPlanJSON(t *testing.T) {
	// A deny rule that always fires — keeps the test deterministic without
	// needing a realistic plan shape.
	rego := `package iacstudio.test

deny[msg] {
  msg := "policy always denies (for testing)"
}
`
	planJSON := `{"resource_changes":[]}`
	root := scaffoldPolicyProject(t, `# empty`, planJSON, rego)
	srv := httptest.NewServer(policyMux(root))
	defer srv.Close()

	// Empty body → handler should fall back to tfplan.json on disk.
	resp, err := http.Post(srv.URL+"/api/projects/demo/policy/run", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var got policyRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Find the OPA result — at least one always-deny finding expected.
	var opaFound bool
	for _, r := range got.Results {
		if r.Engine != "opa" {
			continue
		}
		opaFound = true
		if len(r.Findings) == 0 {
			t.Errorf("OPA should have produced a finding, got: %+v", r)
		}
	}
	if !opaFound {
		t.Error("expected an opa result in the response")
	}
}

// TestPolicyRunUnknownFilterIsQuiet — an engines filter naming unknown
// engines should quietly drop them rather than error out; listed engines
// that do exist still run.
func TestPolicyRunUnknownFilterIsQuiet(t *testing.T) {
	root := scaffoldPolicyProject(t, `# empty`, "", "")
	srv := httptest.NewServer(policyMux(root))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"engines": []string{"made-up", "builtin"}})
	resp, _ := http.Post(srv.URL+"/api/projects/demo/policy/run", "application/json", strings.NewReader(string(body)))
	defer func() { _ = resp.Body.Close() }()

	var got policyRunResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Results) != 1 || got.Results[0].Engine != "builtin" {
		t.Errorf("expected only builtin, got: %+v", got.Results)
	}
}
