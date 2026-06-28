package agentproviders

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultLocalProviderOrder(t *testing.T) {
	definitions := DefaultLocalProviders()
	got := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		got = append(got, definition.ID)
	}
	want := []string{"codex", "claude", "gemini", "copilot", "ollama", "openai-compatible-local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider order = %#v, want %#v", got, want)
	}
}

func TestDiscoverLocalUsesLookupWithoutLeakingPaths(t *testing.T) {
	installed := map[string]string{
		"codex":      "/private/bin/codex",
		"gh-copilot": "/private/bin/gh-copilot",
		"ollama":     "/private/bin/ollama",
	}
	discoverer := NewDiscoverer(WithLookupFunc(func(file string) (string, error) {
		path, ok := installed[file]
		if !ok {
			return "", errors.New("not found")
		}
		return path, nil
	}), WithEndpointProbeFunc(func(string) bool { return false }))

	statuses := discoverer.DiscoverLocal()
	byID := map[string]LocalProviderStatus{}
	for _, status := range statuses {
		byID[status.ID] = status
	}

	if status := byID["codex"]; !status.Installed || status.Command != "codex" || status.Entrypoint != "codex" || status.CredentialMode != CredentialExternalLogin || status.Version != VersionUnknown || !hasCapability(status, "local_cli") {
		t.Fatalf("unexpected codex status: %+v", status)
	}
	if status := byID["claude"]; status.Installed || status.State != StateNotInstalled || status.Command != "" {
		t.Fatalf("unexpected claude status: %+v", status)
	}
	if status := byID["copilot"]; !status.Installed || status.Command != "gh-copilot" || status.Entrypoint != "gh-copilot" {
		t.Fatalf("unexpected copilot status: %+v", status)
	}
	if status := byID["ollama"]; !status.Installed || status.Category != "local_model" || status.CredentialMode != CredentialNone || !hasCapability(status, "offline_runtime") {
		t.Fatalf("unexpected ollama status: %+v", status)
	}

	data, err := json.Marshal(statuses)
	if err != nil {
		t.Fatalf("marshal statuses: %v", err)
	}
	if got := string(data); containsAny(got, []string{"/private/bin/codex", "/private/bin/gh-copilot", "/private/bin/ollama"}) {
		t.Fatalf("status JSON leaked executable path: %s", got)
	}
}

func TestDiscoverLocalDetectsOpenAICompatibleEndpoint(t *testing.T) {
	discoverer := NewDiscoverer(
		WithLookupFunc(func(string) (string, error) {
			return "", errors.New("not found")
		}),
		WithEndpointProbeFunc(func(probeURL string) bool {
			return probeURL == "http://127.0.0.1:1234/v1/models"
		}),
	)

	statuses := discoverer.DiscoverLocal()
	byID := map[string]LocalProviderStatus{}
	for _, status := range statuses {
		byID[status.ID] = status
	}

	status := byID["openai-compatible-local"]
	if !status.Installed || status.State != StateAvailable || status.Category != "local_model" || status.CredentialMode != CredentialNone {
		t.Fatalf("unexpected OpenAI-compatible status: %+v", status)
	}
	if status.Command != "" || status.Entrypoint != "http://127.0.0.1:1234/v1" || status.InstallHint != "" {
		t.Fatalf("unexpected OpenAI-compatible endpoint fields: %+v", status)
	}
	if !hasCapability(status, "openai_compatible") || !hasCapability(status, "local_model") {
		t.Fatalf("missing OpenAI-compatible capabilities: %+v", status.Capabilities)
	}
}

func TestDefaultEndpointProbeOnlyAllowsLoopbackModelLists(t *testing.T) {
	var sawAuthorization bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("probe path = %s, want /v1/models", r.URL.Path)
		}
		sawAuthorization = r.Header.Get("Authorization") != ""
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(server.Close)

	if !defaultEndpointProbe(server.URL + "/v1/models") {
		t.Fatal("expected loopback model-list probe to pass")
	}
	if sawAuthorization {
		t.Fatal("probe should not send authorization headers")
	}
	if defaultEndpointProbe("https://example.com/v1/models") {
		t.Fatal("non-loopback endpoint should not be probed")
	}
}

func TestStatusNormalizesListFieldsForJSONConsumers(t *testing.T) {
	discoverer := NewDiscoverer(WithLookupFunc(func(string) (string, error) {
		return "", errors.New("not found")
	}), WithEndpointProbeFunc(func(string) bool { return false }))

	status := discoverer.status(LocalProviderDefinition{
		ID:         "custom",
		Name:       "Custom Provider",
		Category:   "local_agent",
		Entrypoint: "custom",
	})

	if status.Candidates == nil {
		t.Fatalf("candidates should be an empty slice, got nil")
	}
	if status.Capabilities == nil {
		t.Fatalf("capabilities should be an empty slice, got nil")
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"candidates":[]`) || !strings.Contains(got, `"capabilities":[]`) {
		t.Fatalf("status JSON should expose empty arrays, got %s", got)
	}
}

func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if len(needle) > 0 && strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func hasCapability(status LocalProviderStatus, want string) bool {
	for _, capability := range status.Capabilities {
		if capability == want {
			return true
		}
	}
	return false
}
