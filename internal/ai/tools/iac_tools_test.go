package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/policy/engines"
	"github.com/iac-studio/iac-studio/internal/registry"
)

// scaffoldToolProject seeds a minimal Terraform project for the tool tests.
func scaffoldToolProject(t *testing.T, hcl string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return dir
}

// registerAndLookup is a tiny helper that registers the IaC tools and
// returns the one with the given name.
func registerAndLookup(t *testing.T, deps IaCToolDeps, name string) Tool {
	t.Helper()
	reg := NewRegistry()
	RegisterIaCTools(reg, deps)
	tool, ok := reg.Lookup(name)
	if !ok {
		t.Fatalf("tool %q not registered", name)
	}
	return tool
}

// TestListResourcesTool — walks the project and returns every resource,
// with the optional type filter narrowing results.
func TestListResourcesTool(t *testing.T) {
	dir := scaffoldToolProject(t, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }
resource "aws_s3_bucket" "data" { bucket = "d" }
resource "aws_s3_bucket" "logs" { bucket = "l" }
`)
	tool := registerAndLookup(t, IaCToolDeps{ProjectDir: dir}, "list_resources")

	// No filter → three resources.
	out, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	result := out.(listResourcesResult)
	if len(result.Resources) != 3 {
		t.Errorf("want 3 resources, got %d", len(result.Resources))
	}

	// Type filter → only S3 buckets.
	out, _ = tool.Handler(context.Background(), json.RawMessage(`{"type":"aws_s3_bucket"}`))
	result = out.(listResourcesResult)
	if len(result.Resources) != 2 {
		t.Errorf("filter by type should yield 2 buckets, got %d", len(result.Resources))
	}
}

// TestGetResourceTool — fetching by ID returns the full resource with
// properties; a missing ID yields a clear error.
func TestGetResourceTool(t *testing.T) {
	dir := scaffoldToolProject(t, `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`)
	tool := registerAndLookup(t, IaCToolDeps{ProjectDir: dir}, "get_resource")

	// Happy path.
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"id":"aws_vpc.main"}`))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), "10.0.0.0/16") {
		t.Errorf("resource properties missing from response: %s", raw)
	}

	// Missing.
	_, err = tool.Handler(context.Background(), json.RawMessage(`{"id":"aws_vpc.ghost"}`))
	if err == nil || !strings.Contains(err.Error(), "aws_vpc.ghost") {
		t.Errorf("missing ID should surface in error, got: %v", err)
	}

	// Bad args.
	_, err = tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Error("empty id should be rejected")
	}
}

// TestWriteHCLTool — writes inside the project, refuses escapes, refuses
// non-.tf paths. Atomic replace means the old content is gone once the
// call returns.
func TestWriteHCLTool(t *testing.T) {
	dir := scaffoldToolProject(t, `# original`)
	tool := registerAndLookup(t, IaCToolDeps{ProjectDir: dir}, "write_hcl")

	// Happy path — replace main.tf.
	body, _ := json.Marshal(map[string]string{
		"rel_path": "main.tf",
		"content":  `resource "aws_vpc" "main" { cidr_block = "10.0.0.0/16" }`,
	})
	out, err := tool.Handler(context.Background(), body)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if out.(writeHCLResult).Bytes == 0 {
		t.Error("result should report bytes written")
	}
	newContent, _ := os.ReadFile(filepath.Join(dir, "main.tf"))
	if !strings.Contains(string(newContent), "aws_vpc") {
		t.Errorf("file not replaced: %s", newContent)
	}

	// Path escape attempts.
	for _, bad := range []string{"../escape.tf", "/etc/passwd", "modules/../../escape.tf", "."} {
		body, _ := json.Marshal(map[string]string{"rel_path": bad, "content": "x"})
		if _, err := tool.Handler(context.Background(), body); err == nil {
			t.Errorf("path %q should be rejected", bad)
		}
	}

	// Non-.tf extension.
	body, _ = json.Marshal(map[string]string{"rel_path": "config.yml", "content": "x"})
	if _, err := tool.Handler(context.Background(), body); err == nil {
		t.Error("non-.tf path should be rejected")
	}
}

// TestWriteHCLToolCreatesSubdirs — writing to modules/new/main.tf should
// create intermediate directories.
func TestWriteHCLToolCreatesSubdirs(t *testing.T) {
	dir := scaffoldToolProject(t, `# root`)
	tool := registerAndLookup(t, IaCToolDeps{ProjectDir: dir}, "write_hcl")
	body, _ := json.Marshal(map[string]string{
		"rel_path": "modules/newmod/main.tf",
		"content":  `# module`,
	})
	if _, err := tool.Handler(context.Background(), body); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "modules", "newmod", "main.tf")); err != nil {
		t.Errorf("subdirs not created: %v", err)
	}
}

// TestRunPolicyTool — with the builtin engine, an untagged S3 bucket
// triggers a blocking finding. Agent sees the blocking flag.
func TestRunPolicyTool(t *testing.T) {
	dir := scaffoldToolProject(t, `resource "aws_s3_bucket" "data" { bucket = "d" }`)
	tool := registerAndLookup(t, IaCToolDeps{
		ProjectDir:    dir,
		PolicyEngines: []engines.PolicyEngine{engines.NewBuiltin()},
	}, "run_policy")

	out, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	m := out.(map[string]any)
	if m["blocking"] != true {
		t.Errorf("untagged S3 bucket should produce a blocking finding, got: %+v", m)
	}
}

// TestReadPlanToolMissingFile — clear error when no plan exists so the
// agent knows it needs to run terraform plan first.
func TestReadPlanToolMissingFile(t *testing.T) {
	dir := scaffoldToolProject(t, `# empty`)
	tool := registerAndLookup(t, IaCToolDeps{ProjectDir: dir}, "read_plan")
	_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "terraform plan") {
		t.Errorf("missing plan should nudge the agent toward terraform plan, got: %v", err)
	}
}

// TestReadPlanToolHappyPath — valid tfplan.json round-trips through
// parsing so the agent sees a decoded structure.
func TestReadPlanToolHappyPath(t *testing.T) {
	dir := scaffoldToolProject(t, `# empty`)
	if err := os.WriteFile(filepath.Join(dir, "tfplan.json"), []byte(`{"format_version":"1.0","resource_changes":[]}`), 0o644); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	tool := registerAndLookup(t, IaCToolDeps{ProjectDir: dir}, "read_plan")
	out, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	m := out.(map[string]any)
	if m["format_version"] != "1.0" {
		t.Errorf("decoded plan wrong: %+v", m)
	}
}

// TestSearchRegistryTool — proxies to the registry client via a stubbed
// upstream.
func TestSearchRegistryTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "vpc" {
			t.Errorf("q not forwarded: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"modules":[{"name":"vpc","namespace":"terraform-aws-modules","provider":"aws"}]}`))
	}))
	defer srv.Close()
	tool := registerAndLookup(t, IaCToolDeps{
		ProjectDir:     t.TempDir(),
		RegistryClient: registry.New(registry.Config{BaseURL: srv.URL}),
	}, "search_registry")

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"vpc","limit":5}`))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"name":"vpc"`) {
		t.Errorf("result missing expected module: %s", raw)
	}
}

// TestSearchRegistryToolNoClient — when no RegistryClient is configured,
// the tool surfaces that instead of panicking on a nil pointer.
func TestSearchRegistryToolNoClient(t *testing.T) {
	tool := registerAndLookup(t, IaCToolDeps{ProjectDir: t.TempDir()}, "search_registry")
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"vpc"}`))
	if err == nil {
		t.Error("missing registry client should surface as an error")
	}
}

// TestRegisterIaCToolsRegistersAllNames — regression guard: if someone
// adds a new tool but forgets to Register it, this fails.
func TestRegisterIaCToolsRegistersAllNames(t *testing.T) {
	reg := NewRegistry()
	RegisterIaCTools(reg, IaCToolDeps{ProjectDir: t.TempDir()})
	want := []string{
		"list_resources", "get_resource", "write_hcl",
		"run_policy", "run_scan", "search_registry", "read_plan",
	}
	for _, name := range want {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("tool %q not registered", name)
		}
	}
}
