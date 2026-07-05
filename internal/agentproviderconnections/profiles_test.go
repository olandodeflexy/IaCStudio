package agentproviderconnections

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/cloudconnections"
)

func TestSaveStoresLocalSecretsEncryptedAndReturnsRedactedProfile(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)

	profile, err := manager.Save(Profile{
		Name:           "OpenAI automation",
		ProviderID:     "openai-api",
		CredentialMode: "secret_store",
		Metadata: map[string]string{
			"model": "gpt-5",
		},
		CostControls: map[string]string{
			"monthly_budget": "100",
		},
		Secrets: map[string]string{
			"api_key": "sk-test-secret",
		},
	})
	if err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if profile.ID == "" {
		t.Fatal("profile should include generated id")
	}
	if profile.SecretStore != cloudconnections.SecretStoreLocalEncrypted {
		t.Fatalf("secret store = %q", profile.SecretStore)
	}
	if !slices.Equal(profile.SecretFields, []string{"api_key"}) {
		t.Fatalf("secret fields = %#v", profile.SecretFields)
	}

	data, err := os.ReadFile(filepath.Join(root, profilesFileName))
	if err != nil {
		t.Fatalf("read profiles file: %v", err)
	}
	if strings.Contains(string(data), "sk-test-secret") {
		t.Fatalf("plaintext secret leaked into profile store: %s", string(data))
	}
	if !strings.Contains(string(data), "iacstudio:v1:") {
		t.Fatalf("expected encrypted secret envelope in profile store: %s", string(data))
	}
}

func TestSavePreservesExternalSecretRefsOnPartialUpdate(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)

	created, err := manager.Save(Profile{
		Name:           "Anthropic team gateway",
		ProviderID:     "anthropic-api",
		CredentialMode: "secret_store",
		Metadata: map[string]string{
			"model": "claude-sonnet",
		},
		SecretStore: "vault",
		SecretRefs: map[string]string{
			"api_key": "vault://llm/anthropic/api_key",
		},
	})
	if err != nil {
		t.Fatalf("save external ref profile: %v", err)
	}

	updated, err := manager.Save(Profile{
		ID:             created.ID,
		Name:           "Anthropic team gateway",
		ProviderID:     "anthropic-api",
		CredentialMode: "secret_store",
		Metadata: map[string]string{
			"model": "claude-opus",
		},
		CostControls: map[string]string{
			"monthly_budget": "250",
		},
	})
	if err != nil {
		t.Fatalf("partial update profile: %v", err)
	}
	if updated.SecretStore != "vault" {
		t.Fatalf("secret store = %q", updated.SecretStore)
	}
	if !slices.Equal(updated.SecretFields, []string{"api_key"}) {
		t.Fatalf("secret fields = %#v", updated.SecretFields)
	}

	data, err := os.ReadFile(filepath.Join(root, profilesFileName))
	if err != nil {
		t.Fatalf("read profiles file: %v", err)
	}
	if !strings.Contains(string(data), "vault://llm/anthropic/api_key") {
		t.Fatalf("external secret ref was not preserved: %s", string(data))
	}
}

func TestSaveUsesRegisteredExternalSecretStore(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, WithSecretStore(fakeSecretStore{kind: "vault"}))

	profile, err := manager.Save(Profile{
		Name:           "OpenAI team key",
		ProviderID:     "openai-api",
		CredentialMode: "secret_store",
		SecretStore:    "vault",
		Secrets: map[string]string{
			"api_key": "sk-external-secret",
		},
	})
	if err != nil {
		t.Fatalf("save profile with external store: %v", err)
	}
	if profile.SecretStore != "vault" {
		t.Fatalf("secret store = %q", profile.SecretStore)
	}
	if !slices.Equal(profile.SecretFields, []string{"api_key"}) {
		t.Fatalf("secret fields = %#v", profile.SecretFields)
	}

	data, err := os.ReadFile(filepath.Join(root, profilesFileName))
	if err != nil {
		t.Fatalf("read profiles file: %v", err)
	}
	if strings.Contains(string(data), "sk-external-secret") {
		t.Fatalf("plaintext external secret leaked into profile store: %s", string(data))
	}
	if !strings.Contains(string(data), "vault://"+profile.ID+"/api_key") {
		t.Fatalf("external secret ref was not persisted: %s", string(data))
	}
}

type fakeSecretStore struct {
	kind string
}

func (s fakeSecretStore) Kind() string {
	return s.kind
}

func (s fakeSecretStore) Save(_ context.Context, scope cloudconnections.SecretScope, secrets map[string]string) (cloudconnections.StoredSecrets, error) {
	refs := map[string]string{}
	for key := range secrets {
		refs[key] = s.kind + "://" + scope.ConnectionID + "/" + key
	}
	return cloudconnections.StoredSecrets{Refs: refs}, nil
}

func (s fakeSecretStore) Load(_ context.Context, _ cloudconnections.SecretScope, _ cloudconnections.StoredSecrets) (cloudconnections.LoadedSecrets, error) {
	return cloudconnections.LoadedSecrets{}, nil
}

func (s fakeSecretStore) Delete(context.Context, cloudconnections.SecretScope, cloudconnections.StoredSecrets) error {
	return nil
}
