package agentproviders

import (
	"encoding/json"
	"errors"
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
	want := []string{"codex", "claude", "gemini", "copilot", "ollama"}
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
	}))

	statuses := discoverer.DiscoverLocal()
	byID := map[string]LocalProviderStatus{}
	for _, status := range statuses {
		byID[status.ID] = status
	}

	if status := byID["codex"]; !status.Installed || status.Command != "codex" || status.Entrypoint != "codex" {
		t.Fatalf("unexpected codex status: %+v", status)
	}
	if status := byID["claude"]; status.Installed || status.State != StateNotInstalled || status.Command != "" {
		t.Fatalf("unexpected claude status: %+v", status)
	}
	if status := byID["copilot"]; !status.Installed || status.Command != "gh-copilot" || status.Entrypoint != "gh copilot" {
		t.Fatalf("unexpected copilot status: %+v", status)
	}
	if status := byID["ollama"]; !status.Installed || status.Category != "local_model" {
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

func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if len(needle) > 0 && strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
