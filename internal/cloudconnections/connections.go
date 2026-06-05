package cloudconnections

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Provider   string            `json:"provider"`
	AuthMethod string            `json:"auth_method"`
	Region     string            `json:"region,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Secrets    map[string]string `json:"secrets,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

type PublicConnection struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Provider     string            `json:"provider"`
	AuthMethod   string            `json:"auth_method"`
	Region       string            `json:"region,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	SecretFields []string          `json:"secret_fields,omitempty"`
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
	path string
	mu   sync.RWMutex
}

func NewManager(projectsDir string) *Manager {
	return &Manager{path: filepath.Join(projectsDir, ".iac-studio-connections.json")}
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
	connections, err := m.load()
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		if connection.ID == id {
			copy := connection
			copy.Metadata = cloneMap(connection.Metadata)
			copy.Secrets = cloneMap(connection.Secrets)
			return &copy, nil
		}
	}
	return nil, os.ErrNotExist
}

func (m *Manager) Save(input Connection) (PublicConnection, error) {
	if err := normalizeAndValidate(&input); err != nil {
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
				break
			}
		}
	}
	input.UpdatedAt = now
	input.Metadata = publicMetadata(input.AuthMethod, input.Metadata)
	input.Secrets = secretMetadata(input.AuthMethod, input.Secrets)

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
	defer m.mu.RUnlock()
	return m.loadUnlocked()
}

func (m *Manager) loadUnlocked() ([]Connection, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Connection{}, nil
		}
		return nil, fmt.Errorf("read cloud connections: %w", err)
	}
	var connections []Connection
	if err := json.Unmarshal(data, &connections); err != nil {
		return nil, fmt.Errorf("parse cloud connections: %w", err)
	}
	return connections, nil
}

func (m *Manager) saveUnlocked(connections []Connection) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("create cloud connections directory: %w", err)
	}
	data, err := json.MarshalIndent(connections, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cloud connections: %w", err)
	}
	if err := writeFileAtomic(m.path, data, 0o600); err != nil {
		return fmt.Errorf("write cloud connections: %w", err)
	}
	return nil
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

func normalizeAndValidate(connection *Connection) error {
	connection.ID = strings.TrimSpace(connection.ID)
	connection.Name = strings.TrimSpace(connection.Name)
	connection.Provider = strings.TrimSpace(connection.Provider)
	connection.AuthMethod = strings.TrimSpace(connection.AuthMethod)
	connection.Region = strings.TrimSpace(connection.Region)

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
	return nil
}

func publicConnection(connection Connection) PublicConnection {
	return PublicConnection{
		ID:           connection.ID,
		Name:         connection.Name,
		Provider:     connection.Provider,
		AuthMethod:   connection.AuthMethod,
		Region:       connection.Region,
		Metadata:     publicMetadata(connection.AuthMethod, connection.Metadata),
		SecretFields: presentSecretFields(connection.AuthMethod, connection.Secrets),
		CreatedAt:    connection.CreatedAt,
		UpdatedAt:    connection.UpdatedAt,
	}
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

func presentSecretFields(authMethod string, secrets map[string]string) []string {
	out := []string{}
	for _, key := range secretFieldsByMethod[authMethod] {
		if strings.TrimSpace(secrets[key]) != "" {
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
