package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentProviderConnectionRoutesRedactSecrets(t *testing.T) {
	root := t.TempDir()
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	body := `{
		"name":"OpenAI automation",
		"provider_id":"openai-api",
		"credential_mode":"secret_store",
		"metadata":{"model":"gpt-5"},
		"cost_controls":{"monthly_budget":"100"},
		"secrets":{"api_key":"sk-agent-secret"}
	}`
	resp, err := http.Post(srv.URL+"/api/agent-hub/provider-connections", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST provider connection: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create should 201, got %d", resp.StatusCode)
	}

	var created struct {
		ID             string            `json:"id"`
		Name           string            `json:"name"`
		ProviderID     string            `json:"provider_id"`
		CredentialMode string            `json:"credential_mode"`
		Metadata       map[string]string `json:"metadata"`
		CostControls   map[string]string `json:"cost_controls"`
		SecretFields   []string          `json:"secret_fields"`
		SecretStore    string            `json:"secret_store"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created provider connection should include id")
	}
	if got := created.SecretStore; got != "local_encrypted" {
		t.Fatalf("secret store = %q", got)
	}
	if len(created.SecretFields) != 1 || created.SecretFields[0] != "api_key" {
		t.Fatalf("secret fields = %#v", created.SecretFields)
	}

	resp, err = http.Get(srv.URL + "/api/agent-hub/provider-connections")
	if err != nil {
		t.Fatalf("GET provider connections: %v", err)
	}
	defer closeBody(resp.Body)
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read list: %v", err)
	}
	bodyText := raw.String()
	if strings.Contains(bodyText, "sk-agent-secret") || strings.Contains(bodyText, "secret_refs") || strings.Contains(bodyText, `"secrets"`) {
		t.Fatalf("secret material leaked in list response: %s", bodyText)
	}
	if !strings.Contains(bodyText, "api_key") {
		t.Fatalf("secret field presence should be returned: %s", bodyText)
	}
}

func TestAgentProviderConnectionPartialUpdatePreservesSecretFields(t *testing.T) {
	root := t.TempDir()
	srv := httptest.NewServer(fullRouterForTest(t, root))
	defer srv.Close()

	createBody := `{
		"name":"Anthropic automation",
		"provider_id":"anthropic-api",
		"credential_mode":"secret_store",
		"metadata":{"model":"claude-sonnet"},
		"secrets":{"api_key":"sk-anthropic-secret"}
	}`
	resp, err := http.Post(srv.URL+"/api/agent-hub/provider-connections", "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatalf("POST provider connection: %v", err)
	}
	defer closeBody(resp.Body)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	updateBody := `{
		"name":"Anthropic automation",
		"provider_id":"anthropic-api",
		"credential_mode":"secret_store",
		"metadata":{"model":"claude-opus"},
		"cost_controls":{"monthly_budget":"250"}
	}`
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/agent-hub/provider-connections/"+created.ID, strings.NewReader(updateBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT provider connection: %v", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update should 200, got %d", resp.StatusCode)
	}
	var updated struct {
		SecretFields []string `json:"secret_fields"`
		SecretStore  string   `json:"secret_store"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.SecretStore != "local_encrypted" {
		t.Fatalf("secret store = %q", updated.SecretStore)
	}
	if len(updated.SecretFields) != 1 || updated.SecretFields[0] != "api_key" {
		t.Fatalf("secret fields were not preserved: %#v", updated.SecretFields)
	}

	resp, err = http.Get(srv.URL + "/api/agent-hub/provider-connections/" + created.ID)
	if err != nil {
		t.Fatalf("GET provider connection: %v", err)
	}
	defer closeBody(resp.Body)
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read get: %v", err)
	}
	bodyText := raw.String()
	if strings.Contains(bodyText, "sk-anthropic-secret") || strings.Contains(bodyText, "secret_refs") || strings.Contains(bodyText, `"secrets"`) {
		t.Fatalf("secret material leaked in get response: %s", bodyText)
	}
	if !strings.Contains(bodyText, "claude-opus") || !strings.Contains(bodyText, "api_key") {
		t.Fatalf("updated metadata and secret field should be returned: %s", bodyText)
	}
}
