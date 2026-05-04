package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/parser"
	pulumigen "github.com/iac-studio/iac-studio/internal/pulumi"
)

func writePulumiEnv(t *testing.T, projectDir, env string, resources []parser.Resource) {
	t.Helper()
	files, err := pulumigen.GenerateProject(pulumigen.ProjectConfig{
		Name:         "demo-" + env,
		Environments: []string{env},
		Resources:    resources,
	})
	if err != nil {
		t.Fatalf("GenerateProject: %v", err)
	}
	envDir := filepath.Join(projectDir, "environments", env)
	for _, file := range files {
		path := filepath.Join(envDir, file.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, file.Content, 0o644); err != nil {
			t.Fatalf("write %s: %v", file.Path, err)
		}
	}
}

func TestPulumiResourcesParsesLayeredEnv(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	writePulumiEnv(t, projectDir, "dev", []parser.Resource{{
		ID: "aws_vpc.main", Type: "aws_vpc", Name: "main",
		Properties: map[string]any{"cidr_block": "10.0.0.0/16"},
	}})

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/demo/resources?tool=pulumi&env=dev")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources should 200, got %d", resp.StatusCode)
	}
	var resources []parser.Resource
	if err := json.NewDecoder(resp.Body).Decode(&resources); err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	if len(resources) != 1 || resources[0].Type != "aws_vpc" || resources[0].Name != "main" {
		t.Fatalf("unexpected resources: %+v", resources)
	}
}

func TestHybridResourcesParseEveryEnvironmentWithItsTool(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(projectDir, "environments", "prod"), 0o755); err != nil {
		t.Fatalf("mkdir prod env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio.json"), []byte(`{
  "layout": "layered-v1",
  "tool": "multi",
  "environments": ["dev", "prod"],
  "environment_tools": {"dev": "pulumi", "prod": "terraform"}
}`), 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	writePulumiEnv(t, projectDir, "dev", []parser.Resource{{
		ID: "aws_s3_bucket.logs", Type: "aws_s3_bucket", Name: "logs",
		Properties: map[string]any{"bucket": "demo-dev-logs"},
	}})
	if err := os.WriteFile(filepath.Join(projectDir, "environments", "prod", "main.tf"), []byte(`resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`), 0o644); err != nil {
		t.Fatalf("write prod main.tf: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/demo/resources?tool=multi")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources should 200, got %d", resp.StatusCode)
	}
	var resources []parser.Resource
	if err := json.NewDecoder(resp.Body).Decode(&resources); err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	seen := map[string]bool{}
	for _, resource := range resources {
		seen[resource.Type+"."+resource.Name] = true
	}
	if !seen["aws_s3_bucket.logs"] || !seen["aws_vpc.main"] {
		t.Fatalf("hybrid resources did not include both tools: %+v", resources)
	}
}

func TestEffectiveProjectToolIgnoresUnknownDescriptorTool(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio.json"), []byte(`{"tool":"definitely-not-a-tool"}`), 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}

	if got := effectiveProjectTool(projectDir, "", ""); got != "terraform" {
		t.Fatalf("effectiveProjectTool with unknown descriptor tool = %q, want terraform", got)
	}
}

func TestParseProjectResourcesRejectsUnresolvedHybridEnv(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "environments", "dev"), 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}

	_, err := parseProjectResources(projectDir, "multi", "dev")
	if err == nil {
		t.Fatal("expected unresolved hybrid tool error, got nil")
	}
	if status := resourceParseErrorStatus(err); status != http.StatusBadRequest {
		t.Fatalf("unresolved hybrid tool status = %d, want 400", status)
	}
}

func TestHybridResourcesSortsDescriptorMapEnvironments(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	for _, env := range []string{"prod", "dev"} {
		envDir := filepath.Join(projectDir, "environments", env)
		if err := os.MkdirAll(envDir, 0o755); err != nil {
			t.Fatalf("mkdir %s env: %v", env, err)
		}
		if err := os.WriteFile(filepath.Join(envDir, "main.tf"), []byte(`resource "aws_vpc" "`+env+`" {
  cidr_block = "10.0.0.0/16"
}
`), 0o644); err != nil {
			t.Fatalf("write %s main.tf: %v", env, err)
		}
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio.json"), []byte(`{
  "layout": "layered-v1",
  "tool": "multi",
  "environment_tools": {"prod": "terraform", "dev": "terraform"}
}`), 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}

	resources, err := parseHybridProjectResources(projectDir)
	if err != nil {
		t.Fatalf("parse hybrid resources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("resources length = %d, want 2: %+v", len(resources), resources)
	}
	if resources[0].Name != "dev" || resources[1].Name != "prod" {
		t.Fatalf("resources should be sorted by env name, got %+v", resources)
	}
}

func TestResourcesRejectInvalidEnvAsBadRequest(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/demo/resources?tool=terraform&env=..")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid env should 400, got %d", resp.StatusCode)
	}
}

func TestHybridResourceSyncResolvesSimpleRelativeFileUnderEnv(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	envDir := filepath.Join(projectDir, "environments", "prod")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir prod env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio.json"), []byte(`{
  "layout": "layered-v1",
  "tool": "multi",
  "environments": ["prod"],
  "environment_tools": {"prod": "terraform"}
}`), 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"resources":[{"id":"aws_vpc.main","type":"aws_vpc","name":"main","file":"main.tf","properties":{"cidr_block":"10.0.0.0/16"}}]}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=multi&env=prod",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync should 200, got %d", resp.StatusCode)
	}
	var got struct {
		File string `json:"file"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if filepath.ToSlash(got.File) != "environments/prod/main.tf" {
		t.Fatalf("sync response file = %q, want environments/prod/main.tf", got.File)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "main.tf")); !os.IsNotExist(err) {
		t.Fatalf("simple relative resource file should not write root main.tf, stat err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(envDir, "main.tf"))
	if err != nil {
		t.Fatalf("read env main.tf: %v", err)
	}
	if !strings.Contains(string(data), `resource "aws_vpc" "main"`) {
		t.Fatalf("sync did not write terraform env file:\n%s", string(data))
	}
}

func TestHybridSyncResolvesEnvironmentTool(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	envDir := filepath.Join(projectDir, "environments", "prod")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir prod env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".iac-studio.json"), []byte(`{
  "layout": "layered-v1",
  "tool": "multi",
  "environments": ["prod"],
  "environment_tools": {"prod": "terraform"}
}`), 0o644); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"file":"main.tf","code":"resource \"aws_vpc\" \"main\" {}\n"}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=multi&env=prod",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync should 200, got %d", resp.StatusCode)
	}
	data, err := os.ReadFile(filepath.Join(envDir, "main.tf"))
	if err != nil {
		t.Fatalf("read env main.tf: %v", err)
	}
	if !strings.Contains(string(data), `resource "aws_vpc" "main"`) {
		t.Fatalf("sync did not write terraform env file:\n%s", string(data))
	}
}

func TestPulumiSyncWritesEnvIndexAndPreservesHelpers(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	writePulumiEnv(t, projectDir, "dev", []parser.Resource{{
		ID: "aws_vpc.main", Type: "aws_vpc", Name: "main",
		Properties: map[string]any{"cidr_block": "10.0.0.0/16"},
	}})
	indexPath := filepath.Join(projectDir, "environments", "dev", "index.ts")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	data = bytes.Replace(data, []byte("const main = "), []byte("// User helper must stay.\nfunction keepMe() { return environment; }\n\nconst main = "), 1)
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatalf("write index with helper: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"resources":[` +
		`{"id":"aws_vpc.main","type":"aws_vpc","name":"main","properties":{"cidr_block":"10.0.0.0/16"}},` +
		`{"id":"aws_s3_bucket.logs","type":"aws_s3_bucket","name":"logs","properties":{"bucket":"demo-logs"}}` +
		`]}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=pulumi&env=dev",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync should 200, got %d", resp.StatusCode)
	}

	updated, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read updated index: %v", err)
	}
	got := string(updated)
	if !strings.Contains(got, "function keepMe()") {
		t.Fatalf("helper was not preserved:\n%s", got)
	}
	if !strings.Contains(got, `new aws.s3.Bucket("logs"`) {
		t.Fatalf("new bucket was not written:\n%s", got)
	}
}

func TestMaterializeSyncEdgesPreservesMultipleTargets(t *testing.T) {
	resources := []parser.Resource{
		{
			ID:         "aws_instance.app",
			Type:       "aws_instance",
			Name:       "app",
			Properties: map[string]any{},
		},
		{ID: "aws_security_group.web", Type: "aws_security_group", Name: "web"},
		{ID: "aws_security_group.admin", Type: "aws_security_group", Name: "admin"},
	}

	materializeSyncEdges(resources, []syncEdge{
		{From: "aws_instance.app", To: "aws_security_group.web", Field: "vpc_security_group_ids"},
		{From: "aws_instance.app", To: "aws_security_group.admin", Field: "vpc_security_group_ids"},
		{From: "aws_instance.app", To: "aws_security_group.web", Field: "vpc_security_group_ids"},
	})

	got, ok := resources[0].Properties["__edge_vpc_security_group_ids"].([]string)
	if !ok {
		t.Fatalf("expected []string edge targets, got %#v", resources[0].Properties["__edge_vpc_security_group_ids"])
	}
	want := []string{"web", "admin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edge targets = %#v, want %#v", got, want)
	}
}
