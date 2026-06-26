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

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	return resolved
}

func TestHealthEndpointUsesConfiguredVersion(t *testing.T) {
	root := canonicalTempDir(t)
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Close)
	fw := watcher.New(hub)
	t.Cleanup(fw.Close)
	router := NewRouterWithOptions(
		hub,
		fw,
		ai.NewClient("http://127.0.0.1:1", "ignored"),
		runner.NewSafeRunner(runner.DefaultSafetyConfig()),
		root,
		RouterOptions{Version: "9.8.7-test"},
	)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected health request to return 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected health status ok, got %q", body["status"])
	}
	if body["version"] != "9.8.7-test" {
		t.Fatalf("expected configured version, got %q", body["version"])
	}
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

func TestCORSRejectsDisallowedOriginBeforeHandler(t *testing.T) {
	InitAllowedOrigins("127.0.0.1", 3000)
	t.Cleanup(func() { InitAllowedOrigins("127.0.0.1", 3000) })
	called := false
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/sync", strings.NewReader(`{"resources":[]}`))
	req.Header.Set("Origin", "http://attacker.example")
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected disallowed origin to be rejected with 403, got %d", rec.Code)
	}
	if called {
		t.Fatal("disallowed origin reached wrapped handler")
	}
}

func TestCORSAllowsConfiguredLocalhostOrigin(t *testing.T) {
	InitAllowedOrigins("127.0.0.1", 3000)
	t.Cleanup(func() { InitAllowedOrigins("127.0.0.1", 3000) })
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/sync", strings.NewReader(`{"resources":[]}`))
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected allowed origin through handler, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("expected CORS allow header for localhost, got %q", got)
	}
}

func TestCORSRejectsDisallowedPreflight(t *testing.T) {
	InitAllowedOrigins("127.0.0.1", 3000)
	t.Cleanup(func() { InitAllowedOrigins("127.0.0.1", 3000) })
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodOptions, "/api/projects/demo/run", nil)
	req.Header.Set("Origin", "http://attacker.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected disallowed preflight to be rejected with 403, got %d", rec.Code)
	}
}

func TestWildcardBindDoesNotAllowArbitrarySamePortOrigin(t *testing.T) {
	InitAllowedOrigins("0.0.0.0", 3000)
	t.Cleanup(func() { InitAllowedOrigins("127.0.0.1", 3000) })

	if IsAllowedOrigin("http://192.168.1.25:3000") {
		t.Fatal("wildcard bind should not trust arbitrary LAN origins")
	}
	if !IsAllowedOrigin("http://localhost:3000") || !IsAllowedOrigin("http://127.0.0.1:3000") {
		t.Fatal("wildcard bind should still allow localhost browser origins")
	}
}

func TestInitAllowedOriginsFormatsIPv6LoopbackOrigin(t *testing.T) {
	InitAllowedOrigins("::1", 3000)
	t.Cleanup(func() { InitAllowedOrigins("127.0.0.1", 3000) })

	if !IsAllowedOrigin("http://[::1]:3000") {
		t.Fatal("IPv6 loopback bind should allow bracketed browser origin")
	}
	if IsAllowedOrigin("http://::1:3000") {
		t.Fatal("IPv6 origins should not use unbracketed host syntax")
	}
}

func TestJSONMutationRejectsMissingContentType(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/security/scan", strings.NewReader(`[]`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing content type should 415, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "application/json") {
		t.Fatalf("response body %q does not contain application/json", body)
	}
}

func TestJSONMutationRejectsWrongContentType(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/security/scan", strings.NewReader(`[]`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain content type should 415, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "application/json") {
		t.Fatalf("response body %q does not contain application/json", body)
	}
}

func TestJSONMutationAllowsContentTypeParameters(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/security/scan", strings.NewReader(`[]`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("application/json with charset should 200, got %d", rec.Code)
	}
}

func TestOptionalJSONBodyAllowsEmptyRequestWithoutContentType(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/kill", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("empty optional body should not require content type")
	}
}

func TestOptionalJSONBodyAllowsEmptyRequestWithNonJSONContentType(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/kill", nil)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("empty optional body should not require JSON content type")
	}
}

func TestOptionalJSONBodyAllowsEmptyUnknownLengthRequestWithNonJSONContentType(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/kill", strings.NewReader(""))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("empty unknown-length optional body should not require JSON content type")
	}
}

func TestOptionalJSONBodyRejectsWrongContentTypeWhenBodyPresent(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/kill", strings.NewReader(`{"env":"dev"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("optional JSON body with text/plain should 415, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "application/json") {
		t.Fatalf("response body %q does not contain application/json", body)
	}
}

func TestOptionalJSONBodyRejectsWrongContentTypeWhenUnknownLengthBodyPresent(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/kill", strings.NewReader(`{"env":"dev"}`))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unknown-length optional JSON body with text/plain should 415, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "application/json") {
		t.Fatalf("response body %q does not contain application/json", body)
	}
}

func TestOptionalJSONBodyRestoresPeekedUnknownLengthBody(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/kill", strings.NewReader(`{}`))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadRequest && strings.Contains(rec.Body.String(), "invalid request body") {
		t.Fatalf("peeked unknown-length body was not restored before JSON decode: %s", rec.Body.String())
	}
}

func TestRegisteredMandatoryJSONRoutesRejectWrongContentType(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "module promotion",
			path: "/api/projects/demo/promote-to-module",
			body: `{"module_name":"networking","resource_ids":[]}`,
		},
		{
			name: "agent run",
			path: "/api/projects/demo/ai/agent",
			body: `{"prompt":"summarize this project"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "text/plain")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%s should reject text/plain with 415, got %d: %s", tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRegisteredOptionalJSONRoutesAllowEmptyWrongContentType(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	paths := []string{
		"/api/projects/demo/policy/run",
		"/api/projects/demo/security/scanners/run",
		"/api/projects/demo/ai/index",
		"/api/projects/demo/plan/classify",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			req.Header.Set("Content-Type", "text/plain")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code == http.StatusUnsupportedMediaType {
				t.Fatalf("%s should allow empty optional body despite text/plain", path)
			}
		})
	}
}

func TestRegisteredOptionalJSONRoutesRejectWrongContentTypeWhenBodyPresent(t *testing.T) {
	root := canonicalTempDir(t)
	router := fullRouterForTest(t, root)

	paths := []string{
		"/api/projects/demo/policy/run",
		"/api/projects/demo/security/scanners/run",
		"/api/projects/demo/ai/index",
		"/api/projects/demo/plan/classify",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "text/plain")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%s should reject text/plain body with 415, got %d: %s", path, rec.Code, rec.Body.String())
			}
		})
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

func TestSafeProjectPathAllowsMissingProjectUnderSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "projects-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("create symlinked projects root: %v", err)
	}

	got, err := safeProjectPath(linkRoot, "demo")
	if err != nil {
		t.Fatalf("safeProjectPath should allow missing project under symlinked root: %v", err)
	}
	if got != filepath.Join(linkRoot, "demo") {
		t.Fatalf("safeProjectPath returned %q, want %q", got, filepath.Join(linkRoot, "demo"))
	}
}

func TestSafeProjectPathRejectsExistingProjectSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("create escaping project symlink: %v", err)
	}

	_, err := safeProjectPath(root, "escape")
	if err == nil || !strings.Contains(err.Error(), "project path escapes root") {
		t.Fatalf("expected escaping project symlink rejection, got %v", err)
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

func TestSafeReviewArtifactPathEnforcesPrefix(t *testing.T) {
	reject := []string{
		"main.tf",
		"../escape",
		".iac-studio/snapshots/foo.json",
		".iac-studio/remediations",        // directory itself, no trailing slash
		".iac-studio/rollbacks",           // directory itself, no trailing slash
		".iac-studio/remediations_evil/x", // adjacent directory, not the allowed one
		".iac-studio/remediations/foo/../../outside",
		".iac-studio/rollbacks/foo/../../../etc/passwd",
		"/absolute/path",
	}
	for _, p := range reject {
		t.Run("reject:"+p, func(t *testing.T) {
			if _, err := safeReviewArtifactPath(p); err == nil {
				t.Fatalf("safeReviewArtifactPath(%q) should have returned an error", p)
			}
		})
	}

	accept := []string{
		".iac-studio/remediations/my-drift/README.md",
		".iac-studio/remediations/my-drift/pr-body.md",
		".iac-studio/rollbacks/snap-1/proposal.md",
	}
	for _, p := range accept {
		t.Run("accept:"+p, func(t *testing.T) {
			got, err := safeReviewArtifactPath(p)
			if err != nil {
				t.Fatalf("safeReviewArtifactPath(%q) unexpected error: %v", p, err)
			}
			if got == "" {
				t.Fatalf("safeReviewArtifactPath(%q) returned empty string", p)
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

func TestWriteGeneratedArtifactFileRejectsNonReviewArtifactPath(t *testing.T) {
	projectPath := t.TempDir()

	err := writeGeneratedArtifactFile(projectPath, "main.tf", "resource drift\n")
	if err == nil || !strings.Contains(err.Error(), ".iac-studio review artifacts") {
		t.Fatalf("expected review artifact path rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectPath, "main.tf")); !os.IsNotExist(err) {
		t.Fatalf("non-review artifact write should not create main.tf, stat err = %v", err)
	}
}
