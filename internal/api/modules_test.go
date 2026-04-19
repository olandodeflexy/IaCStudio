package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/registry"
)

// scaffoldModuleProject builds a tiny project with one root main.tf that
// calls one local module and one registry module, plus the local module's
// own variables.tf/outputs.tf.
func scaffoldModuleProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	rootHCL := `module "networking" {
  source = "./modules/networking"
  cidr   = "10.0.0.0/16"
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"
  name    = "prod-vpc"
}
`
	if err := os.WriteFile(filepath.Join(proj, "main.tf"), []byte(rootHCL), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	modDir := filepath.Join(proj, "modules", "networking")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatalf("mkdir modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "variables.tf"), []byte(`
variable "cidr" {
  description = "VPC CIDR"
  type        = string
}
`), 0o644); err != nil {
		t.Fatalf("write vars: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "outputs.tf"), []byte(`
output "vpc_id" {
  value = aws_vpc.this.id
}
`), 0o644); err != nil {
		t.Fatalf("write outputs: %v", err)
	}
	return root
}

// moduleMux wires just the module routes against a registry pointed at a
// stub server. Keeps endpoint tests hermetic.
func moduleMux(projectsDir string, reg *registry.Client) *http.ServeMux {
	mux := http.NewServeMux()
	registerModuleRoutes(mux, projectsDir, reg)
	return mux
}

// TestListProjectModulesReturnsLocalAndRegistry covers the main
// classification + introspection flow: a project with one local module and
// one registry module produces two ModuleView entries, the local one
// carrying its introspected Interface and the registry one carrying
// RegistryCoords for the UI to follow up on.
func TestListProjectModulesReturnsLocalAndRegistry(t *testing.T) {
	root := scaffoldModuleProject(t)
	reg := registry.New(registry.Config{BaseURL: "http://unused"}) // unused in this test
	srv := httptest.NewServer(moduleMux(root, reg))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/demo/modules")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out projectModulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Modules) != 2 {
		t.Fatalf("want 2 modules, got %d: %+v", len(out.Modules), out.Modules)
	}

	byName := map[string]moduleView{}
	for _, m := range out.Modules {
		byName[m.Name] = m
	}

	local, ok := byName["networking"]
	if !ok {
		t.Fatalf("local module 'networking' missing")
	}
	if local.SourceKind != "local" {
		t.Errorf("local module SourceKind wrong: %q", local.SourceKind)
	}
	if local.Interface == nil {
		t.Fatalf("local module should have introspected Interface")
	}
	if len(local.Interface.Variables) != 1 || local.Interface.Variables[0].Name != "cidr" {
		t.Errorf("local variables wrong: %+v", local.Interface.Variables)
	}
	if len(local.Interface.Outputs) != 1 || local.Interface.Outputs[0].Name != "vpc_id" {
		t.Errorf("local outputs wrong: %+v", local.Interface.Outputs)
	}
	if local.RegistryCoords != nil {
		t.Errorf("local modules shouldn't carry RegistryCoords: %+v", local.RegistryCoords)
	}

	vpc, ok := byName["vpc"]
	if !ok {
		t.Fatalf("registry module 'vpc' missing")
	}
	if vpc.SourceKind != "registry" {
		t.Errorf("registry SourceKind wrong: %q", vpc.SourceKind)
	}
	if vpc.RegistryCoords == nil {
		t.Fatalf("registry module should carry RegistryCoords")
	}
	if vpc.RegistryCoords.Namespace != "terraform-aws-modules" ||
		vpc.RegistryCoords.Name != "vpc" ||
		vpc.RegistryCoords.Provider != "aws" {
		t.Errorf("coords wrong: %+v", vpc.RegistryCoords)
	}
	if vpc.Version != "~> 5.0" {
		t.Errorf("version wrong: %q", vpc.Version)
	}
}

// TestClassifyModuleSource covers the one tricky piece of logic in the
// module API — deciding whether a source string is local, a registry
// address, or something else.
func TestClassifyModuleSource(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"./modules/networking", "local"},
		{"../shared/compute", "local"},
		{"/abs/path", "local"},
		{"terraform-aws-modules/vpc/aws", "registry"},
		{"terraform-aws-modules/vpc/aws//submodules/s3", "registry"},
		{"git::https://example.com/foo.git", "other"},
		{"https://example.com/mod.zip", "other"},
		{"app.terraform.io/org/name/provider", "other"}, // hostname-prefixed = private registry, not shorthand
		{"", "unknown"},
	}
	for _, tc := range cases {
		kind, _ := classifyModuleSource(tc.in)
		if kind != tc.want {
			t.Errorf("classifyModuleSource(%q) = %q, want %q", tc.in, kind, tc.want)
		}
	}
}

// TestResolveLocalModulePathRejectsTraversal — a source string with enough
// "../" to escape the project root must be refused.
func TestResolveLocalModulePathRejectsTraversal(t *testing.T) {
	abs, ok := resolveLocalModulePath("/projects/demo", "../../etc/passwd")
	if ok {
		t.Errorf("escape should be refused, got %q", abs)
	}
}

// TestRegistrySearchProxy — the search endpoint forwards q/limit to the
// registry and returns whatever the registry returns.
func TestRegistrySearchProxy(t *testing.T) {
	var seenQ, seenLimit string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/modules/search") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		seenQ = r.URL.Query().Get("q")
		seenLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`{"modules":[{"name":"vpc","namespace":"terraform-aws-modules","provider":"aws"}]}`))
	}))
	defer stub.Close()

	reg := registry.New(registry.Config{BaseURL: stub.URL})
	srv := httptest.NewServer(moduleMux(t.TempDir(), reg))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/registry/modules/search?q=vpc&limit=5")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if seenQ != "vpc" {
		t.Errorf("q not forwarded: %q", seenQ)
	}
	if seenLimit != "5" {
		t.Errorf("limit not forwarded: %q", seenLimit)
	}
}

// TestRegistryGetProxy — follows the registry Get, including the
// upstream-404 → client-404 mapping so users see a meaningful error
// rather than a generic 502.
func TestRegistryGetProxyMapsNotFound(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":["not found"]}`))
	}))
	defer stub.Close()

	reg := registry.New(registry.Config{BaseURL: stub.URL})
	srv := httptest.NewServer(moduleMux(t.TempDir(), reg))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/registry/modules/nobody/wrong/aws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("404 should map through, got %d", resp.StatusCode)
	}
}
