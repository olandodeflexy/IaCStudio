package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudConnectionRoutesRedactSecrets(t *testing.T) {
	root := t.TempDir()
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{
		"name":"prod-admin",
		"provider":"aws",
		"auth_method":"aws_static",
		"region":"us-east-1",
		"metadata":{"access_key_id":"AKIAEXAMPLE","account_id":"123456789012"},
		"secrets":{"secret_access_key":"super-secret"}
	}`
	resp, err := http.Post(srv.URL+"/api/cloud/connections", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST connection: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create should 201, got %d", resp.StatusCode)
	}

	var created struct {
		ID           string            `json:"id"`
		Metadata     map[string]string `json:"metadata"`
		SecretFields []string          `json:"secret_fields"`
		SecretStore  string            `json:"secret_store"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created connection should include id")
	}
	if got := created.Metadata["access_key_id"]; got != "AKIAEXAMPLE" {
		t.Fatalf("public metadata should include access key id, got %q", got)
	}
	if got := created.SecretStore; got != "local_encrypted" {
		t.Fatalf("public response should include local encrypted secret store, got %q", got)
	}

	resp, err = http.Get(srv.URL + "/api/cloud/connections")
	if err != nil {
		t.Fatalf("GET connections: %v", err)
	}
	defer closeBody(resp.Body)
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read list: %v", err)
	}
	if strings.Contains(raw.String(), "super-secret") {
		t.Fatalf("secret leaked in list response: %s", raw.String())
	}
	if !strings.Contains(raw.String(), "secret_access_key") {
		t.Fatalf("secret field presence should be returned: %s", raw.String())
	}
}

func TestCloudConnectionTestReportsMissingFields(t *testing.T) {
	root := t.TempDir()
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"name":"sp","provider":"azure","auth_method":"azure_service_principal","metadata":{"tenant_id":"tenant-1"}}`
	resp, err := http.Post(srv.URL+"/api/cloud/connections", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST connection: %v", err)
	}
	defer closeBody(resp.Body)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	resp, err = http.Post(srv.URL+"/api/cloud/connections/"+created.ID+"/test", "application/json", nil)
	if err != nil {
		t.Fatalf("POST test: %v", err)
	}
	defer closeBody(resp.Body)
	var result struct {
		OK     bool `json:"ok"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode test: %v", err)
	}
	if result.OK {
		t.Fatal("incomplete service principal should not test ok")
	}
	if !routeHasCheck(result.Checks, "client_secret", "error") {
		t.Fatalf("client_secret missing check not found: %#v", result.Checks)
	}
}

func TestCloudConnectionUpdateMissingConnectionReturns404(t *testing.T) {
	root := t.TempDir()
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"name":"prod-admin","provider":"aws","auth_method":"aws_profile","metadata":{"profile":"default"}}`
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/cloud/connections/missing", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT missing connection: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing update should 404, got %d", resp.StatusCode)
	}
}

func TestRunWithMissingCloudConnectionReturns404(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{"tool":"terraform","command":"plan","connection_id":"missing"}`
	resp, err := http.Post(srv.URL+"/api/projects/demo/run", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("run with missing connection should 404, got %d", resp.StatusCode)
	}
}

func TestRunWithIncompleteCloudConnectionReturns400(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	createBody := `{"name":"sp","provider":"azure","auth_method":"azure_service_principal","metadata":{"tenant_id":"tenant-1"}}`
	resp, err := http.Post(srv.URL+"/api/cloud/connections", "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatalf("POST connection: %v", err)
	}
	defer closeBody(resp.Body)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	runBody := `{"tool":"terraform","command":"plan","connection_id":"` + created.ID + `"}`
	resp, err = http.Post(srv.URL+"/api/projects/demo/run", "application/json", strings.NewReader(runBody))
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("run with incomplete connection should 400, got %d", resp.StatusCode)
	}
	assertResponseBodyContains(t, resp, "connection_not_ready", "client_secret")
}

func routeHasCheck(checks []struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}, name string, status string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}

func closeBody(body io.Closer) {
	_ = body.Close()
}
