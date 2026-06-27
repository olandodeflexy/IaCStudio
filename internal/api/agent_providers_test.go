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
