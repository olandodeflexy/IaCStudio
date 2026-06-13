package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPAirlockRoutesExposeTrustedReadOnlyServers(t *testing.T) {
	srv := httptest.NewServer(fullRouterForTest(t, t.TempDir()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/mcp-airlock/servers")
	if err != nil {
		t.Fatalf("GET servers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var statuses []struct {
		Server struct {
			ID              string `json:"id"`
			Trusted         bool   `json:"trusted"`
			ReadOnlyDefault bool   `json:"read_only_default"`
			CredentialMode  string `json:"credential_mode"`
		} `json:"server"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		t.Fatalf("decode servers: %v", err)
	}
	if len(statuses) < 2 {
		t.Fatalf("expected built-in Airlock servers, got %+v", statuses)
	}
	for _, status := range statuses {
		if !status.Server.Trusted || !status.Server.ReadOnlyDefault || status.Server.CredentialMode != "none" {
			t.Fatalf("server is not safe by default: %+v", status.Server)
		}
	}
}

func TestMCPAirlockHealthUnknownServerFailsClosed(t *testing.T) {
	srv := httptest.NewServer(fullRouterForTest(t, t.TempDir()))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/mcp-airlock/servers/unknown/health", "application/json", nil)
	if err != nil {
		t.Fatalf("POST health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
