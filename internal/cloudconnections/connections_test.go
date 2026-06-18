package cloudconnections

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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

	data, err := os.ReadFile(filepath.Join(filepath.Dir(manager.path), ".iac-studio-connections.json"))
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	if contains(string(data), "super-secret") {
		t.Fatal("secret should be encrypted at rest, but plaintext was found")
	}
	if !contains(string(data), encryptedSecretPrefix) {
		t.Fatalf("expected encrypted secret envelope in persisted file: %s", string(data))
	}

	stored, err := manager.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := stored.Secrets["secret_access_key"]; got != "super-secret" {
		t.Fatalf("stored secret should decrypt for runner use, got %q", got)
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
