package agentproviders

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
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

func TestDefaultConnectionProviderOrderAndSecurityMetadata(t *testing.T) {
	definitions := DefaultConnectionProviders()
	got := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		got = append(got, definition.ID)
		if definition.Name == "" || definition.Family == "" || definition.Category == "" {
			t.Fatalf("provider %q missing identity metadata: %+v", definition.ID, definition)
		}
		if definition.BillingHint == "" || definition.DataHandlingHint == "" || definition.SecretStorageHint == "" || definition.SetupHint == "" {
			t.Fatalf("provider %q missing user-facing hints: %+v", definition.ID, definition)
		}
		if len(definition.Capabilities) == 0 || len(definition.CostControls) == 0 {
			t.Fatalf("provider %q missing capabilities or cost controls: %+v", definition.ID, definition)
		}
	}
	want := []string{"openai-api", "anthropic-api", "azure-openai", "aws-bedrock", "vertex-ai", "enterprise-gateway"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("connection provider order = %#v, want %#v", got, want)
	}
}

func TestDefaultConnectionProvidersDoNotContainSecretValues(t *testing.T) {
	data, err := json.Marshal(DefaultConnectionProviders())
	if err != nil {
		t.Fatalf("marshal connection providers: %v", err)
	}
	got := string(data)
	for _, leaked := range []string{"sk-", "AKIA", "secret_value", "access_token", "refresh_token"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("connection provider metadata leaked secret-like value %q in %s", leaked, got)
		}
	}
}

func TestDefaultOpenAICompatibleLocalEndpointCandidates(t *testing.T) {
	definitions := DefaultLocalProviders()
	var endpoints []EndpointCandidate
	for _, definition := range definitions {
		if definition.ID == "openai-compatible-local" {
			endpoints = definition.Endpoints
			break
		}
	}
	want := []EndpointCandidate{
		{Entrypoint: "http://127.0.0.1:1234/v1", ProbeURL: "http://127.0.0.1:1234/v1/models"},
		{Entrypoint: "http://[::1]:1234/v1", ProbeURL: "http://[::1]:1234/v1/models"},
		{Entrypoint: "http://127.0.0.1:8000/v1", ProbeURL: "http://127.0.0.1:8000/v1/models"},
		{Entrypoint: "http://[::1]:8000/v1", ProbeURL: "http://[::1]:8000/v1/models"},
	}
	if !reflect.DeepEqual(endpoints, want) {
		t.Fatalf("openai-compatible-local endpoints = %#v, want %#v", endpoints, want)
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
	var sawCookie bool
	var sawAcceptEncoding bool
	var bodySize int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("probe method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("probe path = %s, want /v1/models", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Fatalf("probe query = %q, want empty", r.URL.RawQuery)
		}
		sawAuthorization = r.Header.Get("Authorization") != ""
		sawCookie = r.Header.Get("Cookie") != ""
		sawAcceptEncoding = r.Header.Get("Accept-Encoding") != ""
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read probe body: %v", err)
		}
		bodySize = len(payload)
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
	if sawCookie {
		t.Fatal("probe should not send cookies")
	}
	if sawAcceptEncoding {
		t.Fatal("probe should not request compressed responses")
	}
	if bodySize != 0 {
		t.Fatalf("probe should not send a request body, got %d bytes", bodySize)
	}
	if defaultEndpointProbe("https://example.com/v1/models") {
		t.Fatal("non-loopback endpoint should not be probed")
	}
}

func TestDefaultEndpointProbeRejectsCredentialAndModelSpecificURLs(t *testing.T) {
	for _, probeURL := range []string{
		"http://" + "user:pass@" + "127.0.0.1:1234/v1/models",
		"http://127.0.0.1:1234/v1/models?model=secret",
		"http://127.0.0.1:1234/v1/models#fragment",
		"http://127.0.0.1:1234/v1/chat/completions",
	} {
		if defaultEndpointProbe(probeURL) {
			t.Fatalf("probe should reject URL %q", probeURL)
		}
	}
}

func TestIsLoopbackHostResolvesLocalhost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), localEndpointProbeTimeout)
	defer cancel()
	if !isLoopbackHost(ctx, "127.0.0.1") {
		t.Fatal("127.0.0.1 should be loopback")
	}
	if !isLoopbackHost(ctx, "::1") {
		t.Fatal("::1 should be loopback")
	}
	if isLoopbackHost(ctx, "192.0.2.1") {
		t.Fatal("192.0.2.1 should not be treated as loopback")
	}
	loopbackLookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if ctx == nil {
			return nil, errors.New("missing context")
		}
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("missing context deadline")
		}
		if host != "localhost" {
			return nil, errors.New("unexpected host")
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("::1")}}, nil
	}
	if !isLoopbackHostWithLookup(ctx, "localhost", loopbackLookup) {
		t.Fatal("localhost should be accepted when all resolved addresses are loopback")
	}
	mixedLookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if ctx == nil {
			return nil, errors.New("missing context")
		}
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("missing context deadline")
		}
		if host != "localhost" {
			return nil, errors.New("unexpected host")
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("192.0.2.1")}}, nil
	}
	if isLoopbackHostWithLookup(ctx, "localhost", mixedLookup) {
		t.Fatal("localhost should be rejected when any resolved address is not loopback")
	}
}

func TestDefaultEndpointProbeDoesNotFollowRedirects(t *testing.T) {
	var redirectTargetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTargetHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(target.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/models", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	if defaultEndpointProbe(redirector.URL + "/v1/models") {
		t.Fatal("redirect responses should not be treated as available")
	}
	if redirectTargetHits.Load() != 0 {
		t.Fatalf("probe followed redirect %d times", redirectTargetHits.Load())
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
