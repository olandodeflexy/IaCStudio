package cloudconnections

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	ProviderAWS   = "aws"
	ProviderAzure = "azure"
	ProviderGCP   = "gcp"

	SecretStoreLocalEncrypted = "local_encrypted"

	connectionsFileName       = ".iac-studio-connections.json"
	connectionsKeyFileName    = ".iac-studio-connections.key"
	connectionsKeyEnv         = "IAC_STUDIO_CONNECTIONS_KEY"
	encryptedSecretPrefix     = "iacstudio:v1:"
	encryptedSecretNonceSize  = 12
	generatedSecretKeyByteLen = 32
)

var providerAuthMethods = map[string][]string{
	ProviderAWS: {
		"aws_profile",
		"aws_sso",
		"aws_static",
	},
	ProviderAzure: {
		"azure_cli",
		"azure_service_principal",
	},
	ProviderGCP: {
		"gcp_gcloud",
		"gcp_service_account",
	},
}

var secretFieldsByMethod = map[string][]string{
	"aws_static":              {"secret_access_key", "session_token"},
	"azure_service_principal": {"client_secret"},
	"gcp_service_account":     {"service_account_json"},
}

var requiredFieldsByMethod = map[string][]string{
	"aws_profile":             {"profile"},
	"aws_sso":                 {"profile"},
	"aws_static":              {"access_key_id", "secret_access_key"},
	"azure_service_principal": {"tenant_id", "client_id", "client_secret"},
	"gcp_service_account":     {"project_id", "service_account_json"},
}

// Connection is the persisted form. Secrets are intentionally isolated from
// public metadata so route handlers can return redacted PublicConnection views.
type Connection struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Provider    string            `json:"provider"`
	AuthMethod  string            `json:"auth_method"`
	Region      string            `json:"region,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Secrets     map[string]string `json:"secrets,omitempty"`
	SecretStore string            `json:"secret_store,omitempty"`
	SecretRefs  map[string]string `json:"secret_refs,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type PublicConnection struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Provider     string            `json:"provider"`
	AuthMethod   string            `json:"auth_method"`
	Region       string            `json:"region,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	SecretFields []string          `json:"secret_fields,omitempty"`
	SecretStore  string            `json:"secret_store,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type TestResult struct {
	OK         bool             `json:"ok"`
	Summary    string           `json:"summary"`
	Connection PublicConnection `json:"connection"`
	Checks     []Check          `json:"checks"`
}

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Manager struct {
	path         string
	keyPath      string
	secretStores map[string]SecretStore
	mu           sync.RWMutex
}

func NewManager(projectsDir string, opts ...Option) *Manager {
	manager := &Manager{
		path:         filepath.Join(projectsDir, connectionsFileName),
		keyPath:      filepath.Join(projectsDir, connectionsKeyFileName),
		secretStores: map[string]SecretStore{},
	}
	manager.registerSecretStore(newLocalEncryptedSecretStore(manager.encryptionKeyForEncrypt, manager.encryptionKeyForDecrypt))
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	return manager
}

func SupportedAuthMethods(provider string) []string {
	methods := providerAuthMethods[provider]
	return slices.Clone(methods)
}

// CommandEnvironment translates a saved connection into process environment
// variables understood by Terraform/OpenTofu, Pulumi, and cloud CLIs. It never
// returns display text, so callers can attach it to command execution without
// echoing secrets back to the terminal or websocket payloads.
func CommandEnvironment(connection Connection) map[string]string {
	env := map[string]string{}
	set := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			env[key] = value
		}
	}

	switch connection.AuthMethod {
	case "aws_profile", "aws_sso":
		set("AWS_PROFILE", connection.Metadata["profile"])
		set("AWS_SDK_LOAD_CONFIG", "1")
	case "aws_static":
		set("AWS_ACCESS_KEY_ID", connection.Metadata["access_key_id"])
		set("AWS_SECRET_ACCESS_KEY", connection.Secrets["secret_access_key"])
		set("AWS_SESSION_TOKEN", connection.Secrets["session_token"])
	case "azure_cli":
		set("ARM_SUBSCRIPTION_ID", connection.Metadata["subscription_id"])
		set("ARM_TENANT_ID", connection.Metadata["tenant_id"])
	case "azure_service_principal":
		set("ARM_SUBSCRIPTION_ID", connection.Metadata["subscription_id"])
		set("ARM_TENANT_ID", connection.Metadata["tenant_id"])
		set("ARM_CLIENT_ID", connection.Metadata["client_id"])
		set("ARM_CLIENT_SECRET", connection.Secrets["client_secret"])
	case "gcp_gcloud":
		setGCPProject(env, connection.Metadata["project_id"])
	case "gcp_service_account":
		setGCPProject(env, connection.Metadata["project_id"])
		set("GOOGLE_CREDENTIALS", connection.Secrets["service_account_json"])
	}

	switch connection.Provider {
	case ProviderAWS:
		set("AWS_REGION", connection.Region)
		set("AWS_DEFAULT_REGION", connection.Region)
	case ProviderGCP:
		set("CLOUDSDK_COMPUTE_REGION", connection.Region)
		set("GOOGLE_REGION", connection.Region)
	}

	if len(env) == 0 {
		return nil
	}
	return env
}

func setGCPProject(env map[string]string, projectID string) {
	if projectID = strings.TrimSpace(projectID); projectID == "" {
		return
	}
	env["GOOGLE_PROJECT"] = projectID
	env["GOOGLE_CLOUD_PROJECT"] = projectID
	env["CLOUDSDK_CORE_PROJECT"] = projectID
}

func (m *Manager) List() ([]PublicConnection, error) {
	connections, err := m.load()
	if err != nil {
		return nil, err
	}
	out := make([]PublicConnection, 0, len(connections))
	for _, connection := range connections {
		out = append(out, publicConnection(connection))
	}
	return out, nil
}

func (m *Manager) Get(id string) (*Connection, error) {
	connections, err := m.loadForUse()
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		if connection.ID == id {
			connectionCopy := connection
			connectionCopy.Metadata = cloneMap(connection.Metadata)
			connectionCopy.Secrets = cloneMap(connection.Secrets)
			connectionCopy.SecretRefs = cloneMap(connection.SecretRefs)
			return &connectionCopy, nil
		}
	}
	return nil, os.ErrNotExist
}

func (m *Manager) Save(input Connection) (PublicConnection, error) {
	if err := m.normalizeAndValidate(&input); err != nil {
		return PublicConnection{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	connections, err := m.loadUnlocked()
	if err != nil {
		return PublicConnection{}, err
	}

	now := time.Now().UTC()
	if input.ID == "" {
		input.ID = newID()
		input.CreatedAt = now
	} else {
		input.CreatedAt = now
		for _, existing := range connections {
			if existing.ID == input.ID {
				input.CreatedAt = existing.CreatedAt
				input.Secrets = mergeSecrets(existing.Secrets, input.Secrets)
				preserveExistingExternalSecretRefs(&input, existing)
				break
			}
		}
	}
	input.UpdatedAt = now
	input.Metadata = publicMetadata(input.AuthMethod, input.Metadata)
	input.Secrets = secretMetadata(input.AuthMethod, input.Secrets)
	applySecretReferenceDefaults(&input)

	replaced := false
	for index, existing := range connections {
		if existing.ID == input.ID {
			connections[index] = input
			replaced = true
			break
		}
	}
	if !replaced {
		connections = append(connections, input)
	}

	if err := m.saveUnlocked(connections); err != nil {
		return PublicConnection{}, err
	}
	return publicConnection(input), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	connections, err := m.loadUnlocked()
	if err != nil {
		return err
	}
	next := connections[:0]
	found := false
	for _, connection := range connections {
		if connection.ID == id {
			found = true
			continue
		}
		next = append(next, connection)
	}
	if !found {
		return os.ErrNotExist
	}
	return m.saveUnlocked(next)
}

func (m *Manager) Test(id string) (TestResult, error) {
	connection, err := m.Get(id)
	if err != nil {
		return TestResult{}, err
	}
	checks := validateReadiness(*connection)
	ok := true
	for _, check := range checks {
		if check.Status == "error" {
			ok = false
			break
		}
	}
	summary := "Connection is ready for local IaC workflows."
	if !ok {
		summary = "Connection is missing required fields before it can be used."
	}
	return TestResult{
		OK:         ok,
		Summary:    summary,
		Connection: publicConnection(*connection),
		Checks:     checks,
	}, nil
}

func (m *Manager) load() ([]Connection, error) {
	m.mu.RLock()
	connections, needsMigration, err := m.loadUnlockedWithMigrationFlag(false)
	m.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if !needsMigration {
		return connections, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	connections, needsMigration, err = m.loadUnlockedWithMigrationFlag(false)
	if err != nil {
		return nil, err
	}
	if !needsMigration {
		return connections, nil
	}
	if err := m.saveUnlocked(connections); err != nil {
		return nil, err
	}
	return connections, nil
}

func (m *Manager) loadForUse() ([]Connection, error) {
	m.mu.RLock()
	connections, needsMigration, err := m.loadUnlockedWithMigrationFlag(true)
	m.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if !needsMigration {
		return connections, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	connections, needsMigration, err = m.loadUnlockedWithMigrationFlag(true)
	if err != nil {
		return nil, err
	}
	if !needsMigration {
		return connections, nil
	}
	if err := m.saveUnlocked(connections); err != nil {
		return nil, err
	}
	connections, _, err = m.loadUnlockedWithMigrationFlag(true)
	return connections, err
}

func (m *Manager) loadUnlocked() ([]Connection, error) {
	connections, _, err := m.loadUnlockedWithMigrationFlag(false)
	return connections, err
}

func (m *Manager) loadUnlockedWithMigrationFlag(resolveSecretRefs bool) ([]Connection, bool, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Connection{}, false, nil
		}
		return nil, false, fmt.Errorf("read cloud connections: %w", err)
	}
	var connections []Connection
	if err := json.Unmarshal(data, &connections); err != nil {
		return nil, false, fmt.Errorf("parse cloud connections: %w", err)
	}
	needsMigration, err := m.loadConnectionSecrets(connections, resolveSecretRefs)
	if err != nil {
		return nil, false, err
	}
	if applySecretReferenceDefaultsToAll(connections) {
		needsMigration = true
	}
	return connections, needsMigration, nil
}

func (m *Manager) saveUnlocked(connections []Connection) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("create cloud connections directory: %w", err)
	}
	persisted, err := m.storeConnectionSecrets(connections)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cloud connections: %w", err)
	}
	if err := writeFileAtomic(m.path, data, 0o600); err != nil {
		return fmt.Errorf("write cloud connections: %w", err)
	}
	return nil
}

func (m *Manager) storeConnectionSecrets(connections []Connection) ([]Connection, error) {
	out := make([]Connection, 0, len(connections))
	for _, connection := range connections {
		next := connection
		next.Metadata = cloneMap(connection.Metadata)
		next.Secrets = cloneMap(connection.Secrets)
		next.SecretRefs = cloneMap(connection.SecretRefs)
		next.SecretStore = strings.TrimSpace(next.SecretStore)
		store, ok := m.secretStoreFor(next.SecretStore)
		if !ok {
			if hasNonEmptySecrets(next.Secrets) {
				return nil, fmt.Errorf("connection %s uses unsupported secret store %q with local secret values", connectionLabel(next), next.SecretStore)
			}
			out = append(out, next)
			continue
		}
		if len(next.Secrets) == 0 {
			out = append(out, next)
			continue
		}
		stored, err := store.Save(context.Background(), secretScope(next), next.Secrets)
		if err != nil {
			return nil, err
		}
		next.Secrets = stored.Values
		next.SecretRefs = stored.Refs
		if len(next.Secrets) > 0 || len(next.SecretRefs) > 0 {
			next.SecretStore = store.Kind()
		}
		out = append(out, next)
	}
	return out, nil
}

func (m *Manager) loadConnectionSecrets(connections []Connection, resolveSecretRefs bool) (bool, error) {
	needsMigration := false
	for index := range connections {
		connections[index].SecretStore = strings.TrimSpace(connections[index].SecretStore)
		store, ok := m.secretStoreFor(connections[index].SecretStore)
		if !ok {
			if hasNonEmptySecrets(connections[index].Secrets) {
				return false, fmt.Errorf("connection %s uses unsupported secret store %q with local secret values", connectionLabel(connections[index]), connections[index].SecretStore)
			}
			continue
		}
		hasStoredValues := len(connections[index].Secrets) > 0
		hasResolvableRefs := resolveSecretRefs && store.Kind() != SecretStoreLocalEncrypted && len(connections[index].SecretRefs) > 0
		if !hasStoredValues && !hasResolvableRefs {
			continue
		}
		loaded, err := store.Load(context.Background(), secretScope(connections[index]), StoredSecrets{
			Values: connections[index].Secrets,
			Refs:   connections[index].SecretRefs,
		})
		if err != nil {
			return false, err
		}
		connections[index].Secrets = loaded.Values
		if loaded.NeedsMigration {
			needsMigration = true
		}
	}
	return needsMigration, nil
}

func secretScope(connection Connection) SecretScope {
	return SecretScope{
		ConnectionID: connection.ID,
		Provider:     connection.Provider,
		AuthMethod:   connection.AuthMethod,
	}
}

func connectionLabel(connection Connection) string {
	id := strings.TrimSpace(connection.ID)
	name := strings.TrimSpace(connection.Name)
	if id == "" && name == "" {
		return "<unknown>"
	}
	if id == "" {
		return fmt.Sprintf("%q", name)
	}
	if name == "" || name == id {
		return fmt.Sprintf("%q", id)
	}
	return fmt.Sprintf("%q (%s)", id, name)
}

func (m *Manager) encryptionKeyForEncrypt() ([]byte, error) {
	if key, ok := environmentEncryptionKey(); ok {
		return key, nil
	}
	return loadOrCreateLocalKey(m.keyPath)
}

func (m *Manager) encryptionKeyForDecrypt() ([]byte, error) {
	if key, ok := environmentEncryptionKey(); ok {
		return key, nil
	}
	return loadExistingLocalKey(m.keyPath)
}

func environmentEncryptionKey() ([]byte, bool) {
	passphrase := strings.TrimSpace(os.Getenv(connectionsKeyEnv))
	if passphrase == "" {
		return nil, false
	}
	sum := sha256.Sum256([]byte(passphrase))
	key := make([]byte, len(sum))
	copy(key, sum[:])
	return key, true
}

func loadOrCreateLocalKey(path string) ([]byte, error) {
	err := ensureLocalKeyFileMode(path)
	if err == nil {
		return readLocalKey(path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key := make([]byte, generatedSecretKeyByteLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate cloud connections key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create cloud connections key directory: %w", err)
	}
	if err := writeNewLocalKey(path, key); err != nil {
		if errors.Is(err, os.ErrExist) {
			if err := ensureLocalKeyFileMode(path); err != nil {
				return nil, err
			}
			return readLocalKey(path)
		}
		return nil, fmt.Errorf("write cloud connections key: %w", err)
	}
	return key, nil
}

func writeNewLocalKey(path string, key []byte) (err error) {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	cleanup := true
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err = file.Chmod(0o600); err != nil {
		return err
	}
	if _, err = file.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = os.Link(tmpPath, path); err != nil {
		return err
	}
	syncDirBestEffort(filepath.Dir(path))
	return nil
}

func loadExistingLocalKey(path string) ([]byte, error) {
	err := ensureLocalKeyFileMode(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read cloud connections key: missing key material; set %s or restore %s before reading encrypted cloud connections", connectionsKeyEnv, connectionsKeyFileName)
		}
		return nil, err
	}
	return readLocalKey(path)
}

func readLocalKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cloud connections key: %w", err)
	}
	decoded, decodeErr := hex.DecodeString(strings.TrimSpace(string(data)))
	if decodeErr != nil || len(decoded) != generatedSecretKeyByteLen {
		return nil, fmt.Errorf("read cloud connections key: invalid key material")
	}
	return decoded, nil
}

func ensureLocalKeyFileMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat cloud connections key: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cloud connections key must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cloud connections key must be a regular file")
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("cloud connections key permissions are too broad (%o); tighten %s permissions to 0600: %w", info.Mode().Perm(), connectionsKeyFileName, err)
	}
	return nil
}

func isEncryptedSecret(value string) bool {
	if !strings.HasPrefix(value, encryptedSecretPrefix) {
		return false
	}
	encoded := strings.TrimPrefix(value, encryptedSecretPrefix)
	nonceText, ciphertextText, ok := strings.Cut(encoded, ":")
	if !ok || nonceText == "" || ciphertextText == "" {
		return false
	}
	nonce, err := base64.RawURLEncoding.DecodeString(nonceText)
	if err != nil || len(nonce) != encryptedSecretNonceSize {
		return false
	}
	if _, err := base64.RawURLEncoding.DecodeString(ciphertextText); err != nil {
		return false
	}
	return true
}

func encryptSecretValue(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, encryptedSecretNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return encryptedSecretPrefix +
		base64.RawURLEncoding.EncodeToString(nonce) + ":" +
		base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func decryptSecretValue(key []byte, value string) (string, error) {
	encoded := strings.TrimPrefix(value, encryptedSecretPrefix)
	nonceText, ciphertextText, ok := strings.Cut(encoded, ":")
	if !ok {
		return "", errors.New("invalid encrypted secret envelope")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(nonceText)
	if err != nil {
		return "", fmt.Errorf("decode nonce: %w", err)
	}
	if len(nonce) != encryptedSecretNonceSize {
		return "", errors.New("invalid nonce length")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(ciphertextText)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("encrypted secret authentication failed; verify " + connectionsKeyEnv + " or " + connectionsKeyFileName + " matches the key used to encrypt this connections file")
	}
	return string(plaintext), nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	syncDirBestEffort(dir)
	return nil
}

func syncDirBestEffort(dir string) {
	handle, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = handle.Close() }()
	_ = handle.Sync()
}

func normalizeConnectionFields(connection *Connection) error {
	connection.ID = strings.TrimSpace(connection.ID)
	connection.Name = strings.TrimSpace(connection.Name)
	connection.Provider = strings.TrimSpace(connection.Provider)
	connection.AuthMethod = strings.TrimSpace(connection.AuthMethod)
	connection.Region = strings.TrimSpace(connection.Region)
	connection.SecretStore = strings.TrimSpace(connection.SecretStore)

	if connection.Name == "" {
		return errors.New("connection name is required")
	}
	methods := providerAuthMethods[connection.Provider]
	if len(methods) == 0 {
		return fmt.Errorf("unsupported cloud provider: %s", connection.Provider)
	}
	if !slices.Contains(methods, connection.AuthMethod) {
		return fmt.Errorf("unsupported auth method %q for provider %q", connection.AuthMethod, connection.Provider)
	}
	connection.Metadata = trimMap(connection.Metadata)
	connection.Secrets = trimMap(connection.Secrets)
	connection.SecretRefs = trimMap(connection.SecretRefs)
	return nil
}

func publicConnection(connection Connection) PublicConnection {
	secretStore := strings.TrimSpace(connection.SecretStore)
	if secretStore == "" && len(connection.Secrets) > 0 {
		secretStore = SecretStoreLocalEncrypted
	}
	return PublicConnection{
		ID:           connection.ID,
		Name:         connection.Name,
		Provider:     connection.Provider,
		AuthMethod:   connection.AuthMethod,
		Region:       connection.Region,
		Metadata:     publicMetadata(connection.AuthMethod, connection.Metadata),
		SecretFields: presentSecretFields(connection.AuthMethod, connection.Secrets, connection.SecretRefs),
		SecretStore:  secretStore,
		CreatedAt:    connection.CreatedAt,
		UpdatedAt:    connection.UpdatedAt,
	}
}

func applySecretReferenceDefaultsToAll(connections []Connection) bool {
	changed := false
	for index := range connections {
		if applySecretReferenceDefaults(&connections[index]) {
			changed = true
		}
	}
	return changed
}

func preserveExistingExternalSecretRefs(input *Connection, existing Connection) {
	secretStore := strings.TrimSpace(existing.SecretStore)
	if secretStore == "" || secretStore == SecretStoreLocalEncrypted {
		return
	}
	if input.SecretStore != "" || len(input.SecretRefs) != 0 || len(input.Secrets) != 0 {
		return
	}
	secretRefs := trimMap(existing.SecretRefs)
	if len(secretRefs) == 0 {
		return
	}
	input.SecretStore = secretStore
	input.SecretRefs = secretRefs
}

func applySecretReferenceDefaults(connection *Connection) bool {
	if connection.SecretStore != "" && connection.SecretStore != SecretStoreLocalEncrypted {
		return false
	}

	refs := localEncryptedSecretRefs(connection.ID, connection.AuthMethod, connection.Secrets)
	if len(refs) == 0 {
		changed := connection.SecretStore != "" || len(connection.SecretRefs) != 0
		connection.SecretStore = ""
		connection.SecretRefs = nil
		return changed
	}

	changed := false
	if connection.SecretStore == "" {
		connection.SecretStore = SecretStoreLocalEncrypted
		changed = true
	}
	if connection.SecretStore != SecretStoreLocalEncrypted {
		return changed
	}
	if !maps.Equal(connection.SecretRefs, refs) {
		connection.SecretRefs = refs
		changed = true
	}
	return changed
}

func localEncryptedSecretRefs(connectionID, authMethod string, secrets map[string]string) map[string]string {
	if strings.TrimSpace(connectionID) == "" {
		return nil
	}
	out := map[string]string{}
	for _, key := range secretFieldsByMethod[authMethod] {
		if strings.TrimSpace(secrets[key]) != "" {
			out[key] = "local://connections/" + connectionID + "/" + key
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func publicMetadata(authMethod string, metadata map[string]string) map[string]string {
	allowed := map[string]bool{
		"profile":         true,
		"account_id":      true,
		"role_arn":        true,
		"access_key_id":   true,
		"subscription_id": true,
		"tenant_id":       true,
		"client_id":       true,
		"project_id":      true,
	}
	out := map[string]string{}
	for key, value := range trimMap(metadata) {
		if allowed[key] && !slices.Contains(secretFieldsByMethod[authMethod], key) {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func secretMetadata(authMethod string, secrets map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range secretFieldsByMethod[authMethod] {
		if value := strings.TrimSpace(secrets[key]); value != "" && !isMasked(value) {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func presentSecretFields(authMethod string, secrets, secretRefs map[string]string) []string {
	out := []string{}
	for _, key := range secretFieldsByMethod[authMethod] {
		if strings.TrimSpace(secrets[key]) != "" || strings.TrimSpace(secretRefs[key]) != "" {
			out = append(out, key)
		}
	}
	return out
}

func mergeSecrets(existing, submitted map[string]string) map[string]string {
	out := cloneMap(existing)
	for key, value := range submitted {
		if value = strings.TrimSpace(value); value != "" && !isMasked(value) {
			if out == nil {
				out = map[string]string{}
			}
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasNonEmptySecrets(secrets map[string]string) bool {
	for _, value := range secrets {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func validateReadiness(connection Connection) []Check {
	required := requiredFieldsByMethod[connection.AuthMethod]
	checks := []Check{{
		Name:    "auth_method",
		Status:  "pass",
		Message: fmt.Sprintf("%s is configured for %s", connection.AuthMethod, connection.Provider),
	}}
	for _, field := range required {
		value := connection.Metadata[field]
		if slices.Contains(secretFieldsByMethod[connection.AuthMethod], field) {
			value = connection.Secrets[field]
		}
		if strings.TrimSpace(value) == "" {
			checks = append(checks, Check{
				Name:    field,
				Status:  "error",
				Message: fmt.Sprintf("%s is required for %s", field, connection.AuthMethod),
			})
		} else {
			checks = append(checks, Check{
				Name:    field,
				Status:  "pass",
				Message: fmt.Sprintf("%s is present", field),
			})
		}
	}
	if len(required) == 0 {
		checks = append(checks, Check{
			Name:    "local_auth",
			Status:  "pass",
			Message: "Uses locally managed cloud CLI credentials.",
		})
	}
	return checks
}

func trimMap(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		if key = strings.TrimSpace(key); key != "" {
			if value = strings.TrimSpace(value); value != "" {
				out[key] = value
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func isMasked(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r != '*' && r != '\u2022' {
			return false
		}
	}
	return true
}

func newID() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("conn_%d", time.Now().UnixNano())
	}
	return "conn_" + hex.EncodeToString(bytes[:])
}
