package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iac-studio/iac-studio/internal/agentproviders"
	"github.com/iac-studio/iac-studio/internal/ai"
	"github.com/iac-studio/iac-studio/internal/runner"
	"github.com/iac-studio/iac-studio/internal/watcher"
)

func TestAgentHubLocalProvidersRouteUsesConfiguredDiscovery(t *testing.T) {
	root := t.TempDir()
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
		RouterOptions{LocalAgentProviders: func() []agentproviders.LocalProviderStatus {
			return []agentproviders.LocalProviderStatus{{
				ID:             "gemini",
				Name:           "Gemini CLI",
				Category:       "local_agent",
				State:          agentproviders.StateAvailable,
				Installed:      true,
				Command:        "gemini",
				Entrypoint:     "gemini",
				Candidates:     []string{"gemini"},
				Version:        agentproviders.VersionUnknown,
				Capabilities:   []string{"chat", "local_cli"},
				CredentialMode: agentproviders.CredentialExternalLogin,
				AuthHint:       "Use the local Gemini session.",
			}}
		}},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/agent-hub/providers/local", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	var body struct {
		Providers []agentproviders.LocalProviderStatus `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Providers) != 1 {
		t.Fatalf("providers = %+v", body.Providers)
	}
	got := body.Providers[0]
	if got.ID != "gemini" || !got.Installed || got.Command != "gemini" || got.Version != agentproviders.VersionUnknown || len(got.Capabilities) != 2 || got.CredentialMode != agentproviders.CredentialExternalLogin {
		t.Fatalf("unexpected provider payload: %+v", got)
	}
}

func TestAgentHubProviderConnectionsRouteUsesConfiguredCatalog(t *testing.T) {
	root := t.TempDir()
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
		RouterOptions{AgentProviderConnections: func() []agentproviders.ConnectionProviderDefinition {
			return []agentproviders.ConnectionProviderDefinition{{
				ID:                "openai-api",
				Name:              "OpenAI API",
				Family:            "openai",
				Category:          agentproviders.ConnectionCategoryAPI,
				CredentialMode:    agentproviders.ConnectionCredentialSecretStore,
				RequiredFields:    []string{"model"},
				SecretFields:      []string{"api_key"},
				Capabilities:      []string{"chat", "tool_calling"},
				CostControls:      []string{"monthly_budget"},
				BillingHint:       "Billed through OpenAI Platform API usage.",
				DataHandlingHint:  "Prompts are sent to the configured endpoint.",
				SecretStorageHint: "Keys are stored through secret stores and never returned.",
				SetupHint:         "Use for automation.",
			}}
		}},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/agent-hub/providers/connections", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	var body struct {
		Providers []agentproviders.ConnectionProviderDefinition `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Providers) != 1 {
		t.Fatalf("providers = %+v", body.Providers)
	}
	got := body.Providers[0]
	if got.ID != "openai-api" || got.CredentialMode != agentproviders.ConnectionCredentialSecretStore || len(got.SecretFields) != 1 || got.SecretFields[0] != "api_key" {
		t.Fatalf("unexpected provider connection payload: %+v", got)
	}
}
