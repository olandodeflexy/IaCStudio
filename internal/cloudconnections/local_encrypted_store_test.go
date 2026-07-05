package cloudconnections

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalEncryptedSecretStoreRoundTrip(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), connectionsKeyFileName)
	store := newLocalEncryptedSecretStore(
		func() ([]byte, error) { return loadOrCreateLocalKey(keyPath) },
		func() ([]byte, error) { return loadExistingLocalKey(keyPath) },
	)
	scope := SecretScope{
		ConnectionID: "conn_test",
		Provider:     ProviderAWS,
		AuthMethod:   "aws_static",
	}

	stored, err := store.Save(context.Background(), scope, map[string]string{
		"secret_access_key": "super-secret",
		"session_token":     "  session-secret  ",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := stored.Values["secret_access_key"]; got == "" || got == "super-secret" || !isEncryptedSecret(got) {
		t.Fatalf("secret should be stored as encrypted envelope, got %q", got)
	}
	if got := stored.Refs["secret_access_key"]; got != "local://connections/conn_test/secret_access_key" {
		t.Fatalf("unexpected local secret ref: %q", got)
	}

	loaded, err := store.Load(context.Background(), scope, stored)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.NeedsMigration {
		t.Fatal("encrypted local secrets should not need migration")
	}
	if got := loaded.Values["secret_access_key"]; got != "super-secret" {
		t.Fatalf("secret_access_key should round trip, got %q", got)
	}
	if got := loaded.Values["session_token"]; got != "  session-secret  " {
		t.Fatalf("session_token should round trip, got %q", got)
	}
}

func TestLocalEncryptedSecretStoreMarksPlaintextForMigration(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), connectionsKeyFileName)
	store := newLocalEncryptedSecretStore(
		func() ([]byte, error) { return loadOrCreateLocalKey(keyPath) },
		func() ([]byte, error) { return loadExistingLocalKey(keyPath) },
	)
	scope := SecretScope{
		ConnectionID: "conn_legacy",
		Provider:     ProviderAWS,
		AuthMethod:   "aws_static",
	}

	loaded, err := store.Load(context.Background(), scope, StoredSecrets{
		Values: map[string]string{"secret_access_key": "  legacy-secret  "},
	})
	if err != nil {
		t.Fatalf("Load legacy plaintext: %v", err)
	}
	if !loaded.NeedsMigration {
		t.Fatal("plaintext local secret should be marked for migration")
	}
	if got := loaded.Values["secret_access_key"]; got != "  legacy-secret  " {
		t.Fatalf("plaintext local secret should remain usable before migration, got %q", got)
	}
}

func TestLocalEncryptedSecretStoreMemoizesKeysPerInstance(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	encryptLoads := 0
	decryptLoads := 0
	store := newLocalEncryptedSecretStore(
		func() ([]byte, error) {
			encryptLoads++
			return key, nil
		},
		func() ([]byte, error) {
			decryptLoads++
			return key, nil
		},
	)
	scope := SecretScope{
		ConnectionID: "conn_test",
		Provider:     ProviderAWS,
		AuthMethod:   "aws_static",
	}

	storedA, err := store.Save(context.Background(), scope, map[string]string{"secret_access_key": "secret-a"})
	if err != nil {
		t.Fatalf("Save A: %v", err)
	}
	storedB, err := store.Save(context.Background(), scope, map[string]string{"secret_access_key": "secret-b"})
	if err != nil {
		t.Fatalf("Save B: %v", err)
	}
	if encryptLoads != 1 {
		t.Fatalf("encryption key should load once per store instance, got %d loads", encryptLoads)
	}

	if _, err := store.Load(context.Background(), scope, storedA); err != nil {
		t.Fatalf("Load A: %v", err)
	}
	if _, err := store.Load(context.Background(), scope, storedB); err != nil {
		t.Fatalf("Load B: %v", err)
	}
	if decryptLoads != 1 {
		t.Fatalf("decryption key should load once per store instance, got %d loads", decryptLoads)
	}
}

func TestNewLocalEncryptedFileSecretStoreReportsKeyPathOnLoadError(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "agent-provider-connections.key")
	store := NewLocalEncryptedFileSecretStore(keyPath)

	_, err := store.Load(context.Background(), SecretScope{
		ConnectionID: "apc_test",
		Provider:     "openai-api",
		AuthMethod:   "secret_store",
	}, StoredSecrets{
		Values: map[string]string{
			"api_key": encryptedSecretPrefix + "AAAAAAAAAAAAAAAA:AA",
		},
	})
	if err == nil {
		t.Fatal("Load should fail when the file-backed key is missing")
	}
	if !strings.Contains(err.Error(), keyPath) {
		t.Fatalf("error should include concrete key path %q, got %q", keyPath, err.Error())
	}
}
