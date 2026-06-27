package cloudconnections

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSecretStore struct {
	kind   string
	values map[string]string
	saves  []StoredSecrets
	loads  []StoredSecrets
}

func (s *fakeSecretStore) Kind() string {
	return s.kind
}

func (s *fakeSecretStore) Save(_ context.Context, scope SecretScope, secrets map[string]string) (StoredSecrets, error) {
	refs := map[string]string{}
	for key, value := range secrets {
		if strings.TrimSpace(value) == "" {
			continue
		}
		ref := s.kind + "://connections/" + scope.ConnectionID + "/" + key
		refs[key] = ref
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[ref] = value
	}
	stored := StoredSecrets{Refs: refs}
	s.saves = append(s.saves, stored)
	return stored, nil
}

func (s *fakeSecretStore) Load(_ context.Context, _ SecretScope, stored StoredSecrets) (LoadedSecrets, error) {
	s.loads = append(s.loads, stored)
	values := map[string]string{}
	needsMigration := false
	for key, ref := range stored.Refs {
		if strings.Contains(ref, "://legacy/") {
			needsMigration = true
		}
		if value := s.values[ref]; strings.TrimSpace(value) != "" {
			values[key] = value
		}
	}
	if len(values) == 0 {
		return LoadedSecrets{NeedsMigration: needsMigration}, nil
	}
	return LoadedSecrets{Values: values, NeedsMigration: needsMigration}, nil
}

func (s *fakeSecretStore) Delete(context.Context, SecretScope, StoredSecrets) error {
	return nil
}

func TestManagerRedactsStaticSecrets(t *testing.T) {
	manager := NewManager(t.TempDir())

	created, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Region:     "us-east-1",
		Metadata: map[string]string{
			"access_key_id": "AKIAEXAMPLE",
			"account_id":    "123456789012",
		},
		Secrets: map[string]string{
			"secret_access_key": "super-secret",
		},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := created.Metadata["access_key_id"]; got != "AKIAEXAMPLE" {
		t.Fatalf("access key id should be public metadata, got %q", got)
	}
	if slices.Contains(created.SecretFields, "secret_access_key") == false {
		t.Fatalf("secret field presence should be exposed without value: %#v", created.SecretFields)
	}
	if created.SecretStore != SecretStoreLocalEncrypted {
		t.Fatalf("secret store should report local encrypted storage, got %q", created.SecretStore)
	}

	listed, err := manager.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one connection, got %d", len(listed))
	}
	if _, leaked := listed[0].Metadata["secret_access_key"]; leaked {
		t.Fatal("secret_access_key leaked into public metadata")
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	if contains(string(data), "super-secret") {
		t.Fatal("secret should be encrypted at rest, but plaintext was found")
	}
	if !contains(string(data), encryptedSecretPrefix) {
		t.Fatalf("expected encrypted secret envelope in persisted file: %s", string(data))
	}
	if !contains(string(data), `"secret_store": "local_encrypted"`) {
		t.Fatalf("expected persisted secret store metadata: %s", string(data))
	}
	if !contains(string(data), `"secret_refs"`) || !contains(string(data), `local://connections/`) {
		t.Fatalf("expected persisted local secret refs: %s", string(data))
	}

	stored, err := manager.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "super-secret" {
		t.Fatalf("stored secret should decrypt for runner use, got %q", got)
	}
	if got := stored.SecretRefs["secret_access_key"]; got != "local://connections/"+created.ID+"/secret_access_key" {
		t.Fatalf("stored secret ref should point at local encrypted store, got %q", got)
	}
}

func TestManagerRegistersLocalEncryptedStoreByDefault(t *testing.T) {
	manager := NewManager(t.TempDir())

	stores := manager.SupportedSecretStores()
	if len(stores) != 1 || stores[0] != SecretStoreLocalEncrypted {
		t.Fatalf("default manager should only register local encrypted store, got %#v", stores)
	}
}

func TestManagerSavesSecretsWithRegisteredStore(t *testing.T) {
	store := &fakeSecretStore{kind: "vault"}
	manager := NewManager(t.TempDir(), WithSecretStore(store))

	created, err := manager.Save(Connection{
		Name:        "external",
		Provider:    ProviderAWS,
		AuthMethod:  "aws_static",
		Metadata:    map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:     map[string]string{"secret_access_key": "super-secret"},
		SecretStore: "vault",
	})
	if err != nil {
		t.Fatalf("Save with registered secret store: %v", err)
	}
	if got := created.SecretStore; got != "vault" {
		t.Fatalf("public connection should report registered secret store, got %q", got)
	}
	if !slices.Contains(created.SecretFields, "secret_access_key") {
		t.Fatalf("public connection should expose referenced secret field: %#v", created.SecretFields)
	}
	if len(store.saves) != 1 {
		t.Fatalf("registered store should save secrets once, got %d calls", len(store.saves))
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read persisted external record: %v", err)
	}
	if contains(string(data), "super-secret") || contains(string(data), "local://connections/") {
		t.Fatalf("registered external store should persist refs without local plaintext or local refs: %s", string(data))
	}
	if !contains(string(data), `"secret_store": "vault"`) || !contains(string(data), `vault://connections/`) {
		t.Fatalf("registered external store should persist external refs: %s", string(data))
	}

	stored, err := manager.Get(created.ID)
	if err != nil {
		t.Fatalf("Get registered store connection: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "super-secret" {
		t.Fatalf("registered store should resolve secret for runner use, got %q", got)
	}
	if len(store.loads) == 0 {
		t.Fatal("registered store should load referenced secrets")
	}
}

func TestManagerLoadsSecretRefsWithRegisteredStore(t *testing.T) {
	dir := t.TempDir()
	ref := "vault://connections/conn_external/secret_access_key"
	store := &fakeSecretStore{
		kind:   "vault",
		values: map[string]string{ref: "super-secret"},
	}
	manager := NewManager(dir, WithSecretStore(store))
	record := `[{
		"id":"conn_external",
		"name":"external",
		"provider":"aws",
		"auth_method":"aws_static",
		"metadata":{"access_key_id":"AKIAEXAMPLE"},
		"secret_store":"vault",
		"secret_refs":{"secret_access_key":"` + ref + `"},
		"created_at":"2026-06-18T00:00:00Z",
		"updated_at":"2026-06-18T00:00:00Z"
	}]`
	if err := os.WriteFile(manager.path, []byte(record), 0o600); err != nil {
		t.Fatalf("write registered store record: %v", err)
	}

	listed, err := manager.List()
	if err != nil {
		t.Fatalf("List registered store record: %v", err)
	}
	if len(listed) != 1 || !slices.Contains(listed[0].SecretFields, "secret_access_key") {
		t.Fatalf("List should expose referenced secret field without resolving the value: %#v", listed)
	}
	if len(store.loads) != 0 {
		t.Fatalf("List should not resolve external secret refs, got %d loads", len(store.loads))
	}

	stored, err := manager.Get("conn_external")
	if err != nil {
		t.Fatalf("Get registered store record: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "super-secret" {
		t.Fatalf("registered store should resolve secret refs, got %q", got)
	}
	if len(store.loads) != 1 {
		t.Fatalf("registered store should load refs once, got %d calls", len(store.loads))
	}
	if got := store.loads[0].Refs["secret_access_key"]; got != ref {
		t.Fatalf("registered store should receive persisted ref, got %q", got)
	}
}

func TestManagerMigratesRegisteredStoreRefsForUse(t *testing.T) {
	dir := t.TempDir()
	oldRef := "vault://legacy/conn_external/secret_access_key"
	newRef := "vault://connections/conn_external/secret_access_key"
	store := &fakeSecretStore{
		kind:   "vault",
		values: map[string]string{oldRef: "super-secret"},
	}
	manager := NewManager(dir, WithSecretStore(store))
	record := `[{
		"id":"conn_external",
		"name":"external",
		"provider":"aws",
		"auth_method":"aws_static",
		"metadata":{"access_key_id":"AKIAEXAMPLE"},
		"secret_store":"vault",
		"secret_refs":{"secret_access_key":"` + oldRef + `"},
		"created_at":"2026-06-18T00:00:00Z",
		"updated_at":"2026-06-18T00:00:00Z"
	}]`
	if err := os.WriteFile(manager.path, []byte(record), 0o600); err != nil {
		t.Fatalf("write registered store record: %v", err)
	}

	stored, err := manager.Get("conn_external")
	if err != nil {
		t.Fatalf("Get registered store record: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "super-secret" {
		t.Fatalf("registered store should resolve migrated secret refs, got %q", got)
	}
	if got := stored.SecretRefs["secret_access_key"]; got != newRef {
		t.Fatalf("registered store should return migrated ref, got %q", got)
	}
	if len(store.saves) != 1 {
		t.Fatalf("registered store should save migrated refs once, got %d saves", len(store.saves))
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read migrated registered store record: %v", err)
	}
	if contains(string(data), "super-secret") {
		t.Fatalf("registered store migration should not persist plaintext: %s", string(data))
	}
	if contains(string(data), oldRef) || !contains(string(data), newRef) {
		t.Fatalf("registered store migration should replace old ref with new ref: %s", string(data))
	}
}

func TestManagerRejectsUnsupportedSecretStore(t *testing.T) {
	manager := NewManager(t.TempDir())

	_, err := manager.Save(Connection{
		Name:        "prod-admin",
		Provider:    ProviderAWS,
		AuthMethod:  "aws_static",
		Metadata:    map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:     map[string]string{"secret_access_key": "super-secret"},
		SecretStore: "vault",
	})
	if err == nil {
		t.Fatal("Save should reject unsupported secret stores until an adapter is registered")
	}
	if !strings.Contains(err.Error(), "unsupported secret store") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManagerRejectsExternalStoreWithLocalSecretValuesDuringPersistence(t *testing.T) {
	manager := NewManager(t.TempDir())

	err := manager.saveUnlocked([]Connection{{
		ID:          "conn_external",
		Name:        "external",
		Provider:    ProviderAWS,
		AuthMethod:  "aws_static",
		SecretStore: "vault",
		SecretRefs:  map[string]string{"secret_access_key": "vault://secret/data/iac/prod#secret_access_key"},
		Secrets:     map[string]string{"secret_access_key": "must-not-persist"},
	}})
	if err == nil {
		t.Fatal("external stores should fail closed when local secret values are present")
	}
	if !strings.Contains(err.Error(), "conn_external") ||
		!strings.Contains(err.Error(), "external") ||
		!strings.Contains(err.Error(), `secret store "vault"`) ||
		!strings.Contains(err.Error(), "local secret values") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManagerRejectsExternalStoreWithLocalSecretValuesOnLoad(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)
	record := `[{
		"id":"conn_external",
		"name":"external",
		"provider":"aws",
		"auth_method":"aws_static",
		"metadata":{"access_key_id":"AKIAEXAMPLE"},
		"secret_store":"vault",
		"secret_refs":{"secret_access_key":"vault://secret/data/iac/prod#secret_access_key"},
		"secrets":{"secret_access_key":"must-not-load"},
		"created_at":"2026-06-18T00:00:00Z",
		"updated_at":"2026-06-18T00:00:00Z"
	}]`
	if err := os.WriteFile(manager.path, []byte(record), 0o600); err != nil {
		t.Fatalf("write external store record: %v", err)
	}

	if _, err := manager.Get("conn_external"); err == nil {
		t.Fatal("Get should reject unsupported external stores that persisted local secret values")
	} else if !strings.Contains(err.Error(), "conn_external") ||
		!strings.Contains(err.Error(), "external") ||
		!strings.Contains(err.Error(), `secret store "vault"`) ||
		!strings.Contains(err.Error(), "local secret values") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManagerNormalizesLocalSecretStoreDuringPersistence(t *testing.T) {
	manager := NewManager(t.TempDir())

	err := manager.saveUnlocked([]Connection{{
		ID:          "conn_local",
		Name:        "local",
		Provider:    ProviderAWS,
		AuthMethod:  "aws_static",
		SecretStore: " local_encrypted ",
		Metadata:    map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:     map[string]string{"secret_access_key": "super-secret"},
	}})
	if err != nil {
		t.Fatalf("save local encrypted record with whitespace store: %v", err)
	}

	stored, err := manager.Get("conn_local")
	if err != nil {
		t.Fatalf("Get normalized local encrypted record: %v", err)
	}
	if got := stored.SecretStore; got != SecretStoreLocalEncrypted {
		t.Fatalf("local encrypted secret store should be normalized, got %q", got)
	}
	if got := stored.Secrets["secret_access_key"]; got != "super-secret" {
		t.Fatalf("local encrypted secret should decrypt after normalized persistence, got %q", got)
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read persisted local encrypted record: %v", err)
	}
	if contains(string(data), `"secret_store": " local_encrypted "`) {
		t.Fatalf("persisted local secret store should be normalized: %s", string(data))
	}
}

func TestManagerNormalizesLocalSecretStoreDuringLoad(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)

	if _, err := manager.Save(Connection{
		Name:       "local",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "super-secret"},
	}); err != nil {
		t.Fatalf("Save local encrypted record: %v", err)
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read local encrypted record: %v", err)
	}
	data = []byte(strings.Replace(string(data), `"secret_store": "local_encrypted"`, `"secret_store": " local_encrypted "`, 1))
	if err := os.WriteFile(manager.path, data, 0o600); err != nil {
		t.Fatalf("write local encrypted record with whitespace store: %v", err)
	}

	listed, err := manager.List()
	if err != nil {
		t.Fatalf("List normalized local encrypted record: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one local encrypted record, got %d", len(listed))
	}
	if got := listed[0].SecretStore; got != SecretStoreLocalEncrypted {
		t.Fatalf("public local encrypted secret store should be normalized, got %q", got)
	}

	stored, err := manager.Get(listed[0].ID)
	if err != nil {
		t.Fatalf("Get normalized local encrypted record: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "super-secret" {
		t.Fatalf("local encrypted secret should decrypt after normalized load, got %q", got)
	}
}

func TestManagerPreservesExternalSecretRefsOnLoad(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)
	ref := "vault://secret/data/iac/prod#secret_access_key"
	record := `[{
		"id":"conn_external",
		"name":"external",
		"provider":"aws",
		"auth_method":"aws_static",
		"metadata":{"access_key_id":"AKIAEXAMPLE"},
		"secret_store":" vault ",
		"secret_refs":{"secret_access_key":" ` + ref + ` ","session_token":"   "},
		"created_at":"2026-06-18T00:00:00Z",
		"updated_at":"2026-06-18T00:00:00Z"
	}]`
	if err := os.WriteFile(manager.path, []byte(record), 0o600); err != nil {
		t.Fatalf("write external store record: %v", err)
	}

	stored, err := manager.Get("conn_external")
	if err != nil {
		t.Fatalf("Get external store record: %v", err)
	}
	if got := strings.TrimSpace(stored.SecretStore); got != "vault" {
		t.Fatalf("external secret store should be preserved, got %q", got)
	}
	if got := strings.TrimSpace(stored.SecretRefs["secret_access_key"]); got != ref {
		t.Fatalf("external secret ref should be preserved, got %q", got)
	}

	listed, err := manager.List()
	if err != nil {
		t.Fatalf("List external store record: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one external store record, got %d", len(listed))
	}
	if !slices.Contains(listed[0].SecretFields, "secret_access_key") {
		t.Fatalf("public secret fields should include referenced secret field: %#v", listed[0].SecretFields)
	}
	if got := listed[0].SecretStore; got != "vault" {
		t.Fatalf("public secret store should be normalized, got %q", got)
	}

	if _, err := manager.Save(Connection{
		ID:         "conn_external",
		Name:       "external-renamed",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Region:     "us-west-2",
		Metadata:   map[string]string{"access_key_id": "AKIARENAMED"},
	}); err != nil {
		t.Fatalf("Save external store metadata update: %v", err)
	}
	updated, err := manager.Get("conn_external")
	if err != nil {
		t.Fatalf("Get updated external store record: %v", err)
	}
	if got := updated.SecretStore; got != "vault" {
		t.Fatalf("external secret store should survive metadata update, got %q", got)
	}
	if got := updated.SecretRefs["secret_access_key"]; got != ref {
		t.Fatalf("external secret ref should survive metadata update, got %q", got)
	}
	if _, ok := updated.SecretRefs["session_token"]; ok {
		t.Fatalf("empty external secret ref should be dropped: %#v", updated.SecretRefs)
	}
	if got := updated.Region; got != "us-west-2" {
		t.Fatalf("metadata update should still apply, got region %q", got)
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read external store record: %v", err)
	}
	if !contains(string(data), ref) {
		t.Fatalf("external secret ref should not be overwritten: %s", string(data))
	}
	if contains(string(data), `"secret_store": " vault "`) || contains(string(data), `"session_token": "   "`) {
		t.Fatalf("external secret refs should be normalized on update: %s", string(data))
	}
	if contains(string(data), "local://connections/") {
		t.Fatalf("external secret store should not receive local refs: %s", string(data))
	}
}

func TestManagerEncryptsUserSecretWithEnvelopePrefix(t *testing.T) {
	manager := NewManager(t.TempDir())
	plaintext := encryptedSecretPrefix + "user-supplied-secret"

	created, err := manager.Save(Connection{
		Name:       "prefixed-secret",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": plaintext},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	if contains(string(data), plaintext) {
		t.Fatal("secret with envelope prefix should still be encrypted at rest")
	}

	stored, err := manager.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != plaintext {
		t.Fatalf("prefixed secret should decrypt to original value, got %q", got)
	}
}

func TestManagerReadsLegacyPlaintextSecrets(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)
	legacy := `[{
		"id":"conn_legacy",
		"name":"legacy",
		"provider":"aws",
		"auth_method":"aws_static",
		"metadata":{"access_key_id":"AKIAEXAMPLE"},
		"secrets":{"secret_access_key":"legacy-secret"},
		"created_at":"2026-06-18T00:00:00Z",
		"updated_at":"2026-06-18T00:00:00Z"
	}]`
	if err := os.WriteFile(manager.path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	stored, err := manager.Get("conn_legacy")
	if err != nil {
		t.Fatalf("Get legacy: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "legacy-secret" {
		t.Fatalf("legacy plaintext secret should remain readable, got %q", got)
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read migrated file: %v", err)
	}
	if contains(string(data), "legacy-secret") {
		t.Fatal("legacy plaintext secret should be migrated to encrypted storage after read")
	}
	if !contains(string(data), encryptedSecretPrefix) {
		t.Fatalf("migrated file should contain encrypted envelope: %s", string(data))
	}
}

func TestManagerMigratesLegacyPlaintextSecretWithEnvelopePrefix(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)
	plaintext := encryptedSecretPrefix + "legacy-prefix-collision"
	legacy := `[{
		"id":"conn_legacy",
		"name":"legacy",
		"provider":"aws",
		"auth_method":"aws_static",
		"metadata":{"access_key_id":"AKIAEXAMPLE"},
		"secrets":{"secret_access_key":"` + plaintext + `"},
		"created_at":"2026-06-18T00:00:00Z",
		"updated_at":"2026-06-18T00:00:00Z"
	}]`
	if err := os.WriteFile(manager.path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	stored, err := manager.Get("conn_legacy")
	if err != nil {
		t.Fatalf("Get legacy: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != plaintext {
		t.Fatalf("legacy plaintext secret with envelope prefix should remain readable, got %q", got)
	}

	data, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read migrated file: %v", err)
	}
	if contains(string(data), plaintext) {
		t.Fatal("legacy plaintext secret with envelope prefix should be migrated to encrypted storage")
	}
	if !contains(string(data), encryptedSecretPrefix) {
		t.Fatalf("migrated file should contain encrypted envelope: %s", string(data))
	}
}

func TestManagerConnectionKeyFileMode(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)

	if _, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "super-secret"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(manager.keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("connection key file mode should be 0600, got %o", got)
	}
	keyData, err := os.ReadFile(manager.keyPath)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if len(strings.TrimSpace(string(keyData))) != generatedSecretKeyByteLen*2 {
		t.Fatalf("key file should contain hex encoded 256-bit key, got %q", string(keyData))
	}
}

func TestManagerConnectionKeyCreationCleansTempFiles(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)

	if _, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "super-secret"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tmpFiles, err := filepath.Glob(filepath.Join(dir, connectionsKeyFileName+".*.tmp"))
	if err != nil {
		t.Fatalf("glob key temp files: %v", err)
	}
	if len(tmpFiles) != 0 {
		t.Fatalf("key temp files should be cleaned up: %#v", tmpFiles)
	}
}

func TestManagerListUsesReadLockWhenNoMigrationNeeded(t *testing.T) {
	manager := NewManager(t.TempDir())
	if _, err := manager.Save(Connection{
		Name:       "profile-only",
		Provider:   ProviderAWS,
		AuthMethod: "aws_profile",
		Metadata:   map[string]string{"profile": "default"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	manager.mu.RLock()
	defer manager.mu.RUnlock()

	errCh := make(chan error, 1)
	go func() {
		_, err := manager.List()
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("List: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("List blocked behind an existing read lock")
	}
}

func TestManagerTightensExistingConnectionKeyFileMode(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)
	if err := os.MkdirAll(filepath.Dir(manager.keyPath), 0o755); err != nil {
		t.Fatalf("mkdir key dir: %v", err)
	}
	if err := os.WriteFile(manager.keyPath, []byte(strings.Repeat("ab", generatedSecretKeyByteLen)+"\n"), 0o644); err != nil {
		t.Fatalf("write loose key file: %v", err)
	}

	key, err := loadOrCreateLocalKey(manager.keyPath)
	if err != nil {
		t.Fatalf("load existing key: %v", err)
	}
	if len(key) != generatedSecretKeyByteLen {
		t.Fatalf("expected %d key bytes, got %d", generatedSecretKeyByteLen, len(key))
	}
	info, err := os.Stat(manager.keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("connection key file mode should be tightened to 0600, got %o", got)
	}
}

func TestManagerRejectsSymlinkedConnectionKey(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)
	targetPath := filepath.Join(dir, "target-key")
	if err := os.WriteFile(targetPath, []byte(strings.Repeat("ab", generatedSecretKeyByteLen)+"\n"), 0o600); err != nil {
		t.Fatalf("write target key: %v", err)
	}
	if err := os.Chmod(targetPath, 0o644); err != nil {
		t.Fatalf("chmod target key: %v", err)
	}
	if err := os.Symlink(targetPath, manager.keyPath); err != nil {
		t.Skipf("symlinks are not available on this platform: %v", err)
	}

	_, err := loadOrCreateLocalKey(manager.keyPath)
	if err == nil {
		t.Fatal("expected symlinked key path to be rejected")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %q", err.Error())
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("stat target key: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("symlink target should not be chmodded, got %o", got)
	}
}

func TestLoadOrCreateLocalKeyReturnsSingleKeyAcrossConcurrentCreators(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		dir := t.TempDir()
		keyPath := filepath.Join(dir, connectionsKeyFileName)
		const workers = 8
		start := make(chan struct{})
		results := make(chan string, workers)
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				<-start
				key, err := loadOrCreateLocalKey(keyPath)
				if err != nil {
					errs <- err
					return
				}
				results <- string(key)
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		close(errs)

		for err := range errs {
			t.Fatalf("loadOrCreateLocalKey: %v", err)
		}

		var first string
		for key := range results {
			if first == "" {
				first = key
				continue
			}
			if key != first {
				t.Fatal("concurrent key creators should all return the same key material")
			}
		}
	}
}

func TestManagerCanUseEnvironmentEncryptionKey(t *testing.T) {
	t.Setenv(connectionsKeyEnv, "stable-deployment-key")
	dir := t.TempDir()
	manager := NewManager(dir)

	created, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "super-secret"},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(manager.keyPath); !os.IsNotExist(err) {
		t.Fatalf("env-key mode should not create local key file, err=%v", err)
	}

	reloaded := NewManager(dir)
	stored, err := reloaded.Get(created.ID)
	if err != nil {
		t.Fatalf("Get with env key: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "super-secret" {
		t.Fatalf("env key should decrypt stored secret, got %q", got)
	}
}

func TestManagerWrongEncryptionKeyReturnsRemediation(t *testing.T) {
	t.Setenv(connectionsKeyEnv, "original-deployment-key")
	dir := t.TempDir()
	manager := NewManager(dir)

	created, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "super-secret"},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	t.Setenv(connectionsKeyEnv, "rotated-deployment-key")
	reloaded := NewManager(dir)
	_, err = reloaded.Get(created.ID)
	if err == nil {
		t.Fatal("expected wrong encryption key to fail closed")
	}
	message := err.Error()
	if !strings.Contains(message, connectionsKeyEnv) || !strings.Contains(message, connectionsKeyFileName) {
		t.Fatalf("expected key remediation in decrypt error, got %q", message)
	}
	if strings.Contains(message, "super-secret") {
		t.Fatalf("decrypt error leaked plaintext secret: %q", message)
	}
}

func TestManagerMissingLocalKeyDoesNotCreateReplacementOnDecrypt(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)

	created, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "super-secret"},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Remove(manager.keyPath); err != nil {
		t.Fatalf("remove local key file: %v", err)
	}

	reloaded := NewManager(dir)
	_, err = reloaded.Get(created.ID)
	if err == nil {
		t.Fatal("expected missing local encryption key to fail closed")
	}
	message := err.Error()
	if !strings.Contains(message, connectionsKeyEnv) || !strings.Contains(message, connectionsKeyFileName) {
		t.Fatalf("expected missing key remediation in decrypt error, got %q", message)
	}
	if strings.Contains(message, "super-secret") {
		t.Fatalf("decrypt error leaked plaintext secret: %q", message)
	}
	if _, err := os.Stat(reloaded.keyPath); !os.IsNotExist(err) {
		t.Fatalf("decrypt should not create a replacement local key file, stat err=%v", err)
	}
}

func TestManagerKeepsExistingSecretOnMaskedUpdate(t *testing.T) {
	manager := NewManager(t.TempDir())

	created, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "old-secret"},
	})
	if err != nil {
		t.Fatalf("Save create: %v", err)
	}

	if _, err := manager.Save(Connection{
		ID:         created.ID,
		Name:       "prod-admin-renamed",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "********"},
	}); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	stored, err := manager.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "old-secret" {
		t.Fatalf("masked update should keep old secret, got %q", got)
	}
}

func TestManagerPersistsSecretContainingMaskCharacters(t *testing.T) {
	manager := NewManager(t.TempDir())

	created, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": "old-secret"},
	})
	if err != nil {
		t.Fatalf("Save create: %v", err)
	}

	want := "{\"private_key\":\"abc*def\u2022ghi\"}"
	if _, err := manager.Save(Connection{
		ID:         created.ID,
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets:    map[string]string{"secret_access_key": want},
	}); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	stored, err := manager.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != want {
		t.Fatalf("secret containing mask characters should persist, got %q", got)
	}
}

func TestManagerSaveUsesAtomicFileModeAndCleansTempFiles(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)

	if _, err := manager.Save(Connection{
		Name:       "prod-admin",
		Provider:   ProviderAWS,
		AuthMethod: "aws_profile",
		Metadata:   map[string]string{"profile": "default"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(manager.path)
	if err != nil {
		t.Fatalf("stat persisted file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("connection file mode should be 0600, got %o", got)
	}

	tmpFiles, err := filepath.Glob(filepath.Join(dir, ".iac-studio-connections.json.*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(tmpFiles) != 0 {
		t.Fatalf("atomic temp files should be cleaned up: %#v", tmpFiles)
	}
}

func TestManagerTestReportsMissingRequiredFields(t *testing.T) {
	manager := NewManager(t.TempDir())

	created, err := manager.Save(Connection{
		Name:       "incomplete-sp",
		Provider:   ProviderAzure,
		AuthMethod: "azure_service_principal",
		Metadata:   map[string]string{"tenant_id": "tenant-1"},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	result, err := manager.Test(created.ID)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if result.OK {
		t.Fatal("incomplete service principal should not be ready")
	}
	if !hasCheck(result.Checks, "client_id", "error") || !hasCheck(result.Checks, "client_secret", "error") {
		t.Fatalf("missing required fields were not reported: %#v", result.Checks)
	}
}

func TestManagerRejectsUnsupportedAuthMethod(t *testing.T) {
	manager := NewManager(t.TempDir())

	_, err := manager.Save(Connection{
		Name:       "bad",
		Provider:   ProviderAWS,
		AuthMethod: "azure_cli",
	})
	if err == nil {
		t.Fatal("expected unsupported auth method error")
	}
}

func TestCommandEnvironmentAWSProfile(t *testing.T) {
	env := CommandEnvironment(Connection{
		Provider:   ProviderAWS,
		AuthMethod: "aws_profile",
		Region:     "us-east-1",
		Metadata:   map[string]string{"profile": "prod-admin"},
	})

	if env["AWS_PROFILE"] != "prod-admin" {
		t.Fatalf("AWS_PROFILE not set from profile: %#v", env)
	}
	if env["AWS_SDK_LOAD_CONFIG"] != "1" {
		t.Fatalf("AWS_SDK_LOAD_CONFIG should be enabled for profile auth: %#v", env)
	}
	if env["AWS_REGION"] != "us-east-1" || env["AWS_DEFAULT_REGION"] != "us-east-1" {
		t.Fatalf("AWS region env not set: %#v", env)
	}
}

func TestCommandEnvironmentAWSStatic(t *testing.T) {
	env := CommandEnvironment(Connection{
		Provider:   ProviderAWS,
		AuthMethod: "aws_static",
		Region:     "us-west-2",
		Metadata:   map[string]string{"access_key_id": "AKIAEXAMPLE"},
		Secrets: map[string]string{
			"secret_access_key": "secret-value",
			"session_token":     "session-value",
		},
	})

	if env["AWS_ACCESS_KEY_ID"] != "AKIAEXAMPLE" ||
		env["AWS_SECRET_ACCESS_KEY"] != "secret-value" ||
		env["AWS_SESSION_TOKEN"] != "session-value" {
		t.Fatalf("AWS static credential env not set: %#v", env)
	}
}

func TestCommandEnvironmentAzureServicePrincipal(t *testing.T) {
	env := CommandEnvironment(Connection{
		Provider:   ProviderAzure,
		AuthMethod: "azure_service_principal",
		Metadata: map[string]string{
			"subscription_id": "sub-1",
			"tenant_id":       "tenant-1",
			"client_id":       "client-1",
		},
		Secrets: map[string]string{"client_secret": "secret-value"},
	})

	if env["ARM_SUBSCRIPTION_ID"] != "sub-1" ||
		env["ARM_TENANT_ID"] != "tenant-1" ||
		env["ARM_CLIENT_ID"] != "client-1" ||
		env["ARM_CLIENT_SECRET"] != "secret-value" {
		t.Fatalf("Azure service principal env not set: %#v", env)
	}
}

func TestCommandEnvironmentGCPServiceAccount(t *testing.T) {
	env := CommandEnvironment(Connection{
		Provider:   ProviderGCP,
		AuthMethod: "gcp_service_account",
		Region:     "us-central1",
		Metadata:   map[string]string{"project_id": "prod-project"},
		Secrets:    map[string]string{"service_account_json": `{"type":"service_account"}`},
	})

	if env["GOOGLE_PROJECT"] != "prod-project" ||
		env["GOOGLE_CLOUD_PROJECT"] != "prod-project" ||
		env["CLOUDSDK_CORE_PROJECT"] != "prod-project" {
		t.Fatalf("GCP project env not set: %#v", env)
	}
	if env["GOOGLE_CREDENTIALS"] != `{"type":"service_account"}` {
		t.Fatalf("GCP credentials env not set: %#v", env)
	}
	if env["GOOGLE_REGION"] != "us-central1" || env["CLOUDSDK_COMPUTE_REGION"] != "us-central1" {
		t.Fatalf("GCP region env not set: %#v", env)
	}
}

func TestIsMaskedRequiresOnlyMaskCharacters(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "empty", value: "", want: false},
		{name: "stars", value: "********", want: true},
		{name: "bullets", value: "\u2022\u2022\u2022\u2022", want: true},
		{name: "mixed masks", value: "***\u2022\u2022", want: true},
		{name: "trimmed masks", value: "  ********  ", want: true},
		{name: "embedded star", value: "abc*def", want: false},
		{name: "embedded bullet", value: "abc\u2022def", want: false},
		{name: "json credential", value: `{"private_key":"abc*def"}`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMasked(tt.value); got != tt.want {
				t.Fatalf("isMasked(%q) = %t, want %t", tt.value, got, tt.want)
			}
		})
	}
}

func contains(text, want string) bool {
	return strings.Contains(text, want)
}

func hasCheck(checks []Check, name string, status string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
