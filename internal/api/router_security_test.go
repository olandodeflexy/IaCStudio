package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/drift"
	"github.com/iac-studio/iac-studio/internal/runner"
	"github.com/iac-studio/iac-studio/internal/watcher"
)

func fullRouterForTest(t *testing.T, projectsDir string) *http.ServeMux {
	t.Helper()
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Close)
	fw := watcher.New(hub)
	t.Cleanup(fw.Close)
	return NewRouter(
		hub,
		fw,
		ai.NewClient("http://127.0.0.1:1", "ignored"),
		runner.NewSafeRunner(runner.DefaultSafetyConfig()),
		projectsDir,
	)
}

func assertResponseBodyContains(t *testing.T, resp *http.Response, want ...string) {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	body := string(data)
	for _, text := range want {
		if !strings.Contains(body, text) {
			t.Fatalf("response body %q does not contain %q", body, text)
		}
	}
}

func TestStateRoutesRejectTraversalProjectName(t *testing.T) {
	root := t.TempDir()
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/projects/%2e%2e%2foutside/state")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal state load should 400, got %d", resp.StatusCode)
	}
}

func TestSyncRejectsResourceFileOutsideProject(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	escape := filepath.Join(root, "..", "outside.tf")
	body := `{"resources":[{"id":"aws_vpc.main","type":"aws_vpc","name":"main","file":` +
		`"` + strings.ReplaceAll(escape, `\`, `\\`) + `",` +
		`"properties":{"cidr_block":"10.0.0.0/16"}}]}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=terraform",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("sync with escaping file should 400, got %d", resp.StatusCode)
	}
}

func TestSyncCodeWritesMainFile(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"file":"main.tf","code":"resource \"aws_vpc\" \"main\" {}\n"}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=terraform",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code sync should 200, got %d", resp.StatusCode)
	}
	var got struct {
		File string `json:"file"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.File != "main.tf" {
		t.Fatalf("code sync should return project-relative file, got %q", got.File)
	}
	data, err := os.ReadFile(filepath.Join(projectDir, "main.tf"))
	if err != nil {
		t.Fatalf("read synced file: %v", err)
	}
	if got := string(data); got != "resource \"aws_vpc\" \"main\" {}\n" {
		t.Fatalf("unexpected synced file content: %q", got)
	}
}

func TestSyncCodeInvalidatesLayeredEnvPlan(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	envDir := filepath.Join(projectDir, "environments", "dev")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	invalidatePlan(projectDir, envDir)
	recordPlan(projectDir)
	recordPlan(envDir)

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"file":"environments/dev/main.tf","code":"resource \"aws_vpc\" \"main\" {}\n"}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=terraform",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code sync should 200, got %d", resp.StatusCode)
	}
	if hasPlan(projectDir) {
		t.Fatal("root plan gate should be invalidated after sync")
	}
	if hasPlan(envDir) {
		t.Fatal("env plan gate should be invalidated after layered sync")
	}
}

func TestSyncCodeRejectsFileOutsideProject(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"file":"../outside.tf","code":"resource \"aws_vpc\" \"main\" {}\n"}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=terraform",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("code sync with escaping file should 400, got %d", resp.StatusCode)
	}
}

func TestSyncResourcesInvalidatesLayeredEnvPlan(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	envDir := filepath.Join(projectDir, "environments", "dev")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	invalidatePlan(projectDir, envDir)
	recordPlan(projectDir)
	recordPlan(envDir)

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"resources":[{"id":"aws_vpc.main","type":"aws_vpc","name":"main","file":"environments/dev/main.tf","properties":{"cidr_block":"10.0.0.0/16"}}]}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=terraform",
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
	if hasPlan(projectDir) {
		t.Fatal("root plan gate should be invalidated after resource sync")
	}
	if hasPlan(envDir) {
		t.Fatal("env plan gate should be invalidated after layered resource sync")
	}
}

func TestSyncDoesNotInjectProviderIntoNestedMainFile(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	moduleDir := filepath.Join(projectDir, "modules", "networking")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("mkdir module: %v", err)
	}

	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"resources":[{"id":"aws_vpc.main","type":"aws_vpc","name":"main","file":"modules/networking/main.tf","properties":{"cidr_block":"10.0.0.0/16"}}]}`
	resp, err := http.Post(
		srv.URL+"/api/projects/demo/sync?tool=terraform",
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
	data, err := os.ReadFile(filepath.Join(moduleDir, "main.tf"))
	if err != nil {
		t.Fatalf("read synced module file: %v", err)
	}
	if strings.Contains(string(data), `provider "aws"`) {
		t.Fatalf("nested module main.tf should not receive provider block:\n%s", string(data))
	}
}

func TestSafeProjectFilePathRejectsTraversal(t *testing.T) {
	projectPath := t.TempDir()

	cases := []struct {
		relPath string
		desc    string
	}{
		{"../escape", "parent traversal"},
		{"../../etc/passwd", "deep parent traversal"},
		{"/absolute/path", "absolute path"},
		{".", "dot only"},
		{"..", "double-dot only"},
		{"subdir/../../escape", "traversal through subdirectory"},
		{"", "empty path"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := safeProjectFilePath(projectPath, tc.relPath)
			if err == nil {
				t.Fatalf("safeProjectFilePath(%q) should have returned an error", tc.relPath)
			}
		})
	}
}

func TestSafeProjectFilePathAcceptsValidPaths(t *testing.T) {
	projectPath := t.TempDir()

	cases := []struct {
		relPath  string
		wantSuff string
	}{
		{".iac-studio/remediations/my-drift/README.md", filepath.Join(".iac-studio", "remediations", "my-drift", "README.md")},
		{"main.tf", "main.tf"},
		{"modules/vpc/main.tf", filepath.Join("modules", "vpc", "main.tf")},
	}

	for _, tc := range cases {
		t.Run(tc.relPath, func(t *testing.T) {
			got, err := safeProjectFilePath(projectPath, tc.relPath)
			if err != nil {
				t.Fatalf("safeProjectFilePath(%q) unexpected error: %v", tc.relPath, err)
			}
			if !strings.HasSuffix(got, tc.wantSuff) {
				t.Fatalf("safeProjectFilePath(%q) = %q, want suffix %q", tc.relPath, got, tc.wantSuff)
			}
		})
	}
}

func TestWriteRenderedRemediationArtifactsRejectsSymlinkDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	projectPath := t.TempDir()
	outside := t.TempDir()
	linkParent := filepath.Join(projectPath, ".iac-studio", "remediations")
	if err := os.MkdirAll(linkParent, 0o755); err != nil {
		t.Fatalf("mkdir link parent: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(linkParent, "evil")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	err := writeRenderedRemediationArtifacts(projectPath, []drift.RenderedRemediationArtifact{{
		RemediationArtifactFile: drift.RemediationArtifactFile{
			Path: ".iac-studio/remediations/evil/README.md",
			Kind: "runbook",
		},
		Content: "should not escape\n",
	}})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("artifact write escaped through symlink, stat err = %v", err)
	}
}
