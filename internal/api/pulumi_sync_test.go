package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
