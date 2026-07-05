package agentproviderconnections

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iac-studio/iac-studio/internal/cloudconnections"
)

const (
	profilesFileName    = ".iac-studio-agent-provider-connections.json"
	profilesKeyFileName = ".iac-studio-agent-provider-connections.key"
)

// Profile is the persisted form of an Agent Hub model-provider connection.
// Secrets are stored through a cloudconnections.SecretStore and must never be
// returned directly from API handlers.
type Profile struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	ProviderID     string            `json:"provider_id"`
	CredentialMode string            `json:"credential_mode"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	CostControls   map[string]string `json:"cost_controls,omitempty"`
	Secrets        map[string]string `json:"secrets,omitempty"`
	SecretStore    string            `json:"secret_store,omitempty"`
	SecretRefs     map[string]string `json:"secret_refs,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// PublicProfile is safe to return to browsers. It preserves connection shape
// and secret field presence without exposing secret values or external refs.
type PublicProfile struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	ProviderID     string            `json:"provider_id"`
	CredentialMode string            `json:"credential_mode"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	CostControls   map[string]string `json:"cost_controls,omitempty"`
	SecretFields   []string          `json:"secret_fields,omitempty"`
	SecretStore    string            `json:"secret_store,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// Option customizes an Agent Hub provider profile manager.
type Option func(*Manager)

// WithSecretStore registers an additional backend for profile secrets.
func WithSecretStore(store cloudconnections.SecretStore) Option {
	return func(m *Manager) {
		m.registerSecretStore(store)
	}
}

// Manager persists Agent Hub provider connection profiles.
type Manager struct {
	path           string
	secretStores   map[string]cloudconnections.SecretStore
	secretStoresMu sync.RWMutex
	mu             sync.Mutex
}

func NewManager(projectsDir string, opts ...Option) *Manager {
	manager := &Manager{
		path:         filepath.Join(projectsDir, profilesFileName),
		secretStores: map[string]cloudconnections.SecretStore{},
	}
	manager.registerSecretStore(cloudconnections.NewLocalEncryptedFileSecretStore(filepath.Join(projectsDir, profilesKeyFileName)))
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	return manager
}

func (m *Manager) List() ([]PublicProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	profiles, needsMigration, err := m.loadProfilesUnlocked(false)
	if err != nil {
		return nil, err
	}
	if needsMigration {
		if err := m.saveProfilesUnlocked(profiles); err != nil {
			return nil, err
		}
	}
	out := make([]PublicProfile, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, publicProfile(profile))
	}
	return out, nil
}

func (m *Manager) Get(id string) (PublicProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	profiles, needsMigration, err := m.loadProfilesUnlocked(false)
	if err != nil {
		return PublicProfile{}, err
	}
	if needsMigration {
		if err := m.saveProfilesUnlocked(profiles); err != nil {
			return PublicProfile{}, err
		}
	}
	for _, profile := range profiles {
		if profile.ID == strings.TrimSpace(id) {
			return publicProfile(profile), nil
		}
	}
	return PublicProfile{}, os.ErrNotExist
}

// GetForUse resolves registered external refs for future execution paths. It is
// not used by the current read-only API.
func (m *Manager) GetForUse(id string) (*Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	profiles, _, err := m.loadProfilesUnlocked(true)
	if err != nil {
		return nil, err
	}
	for index := range profiles {
		if profiles[index].ID == strings.TrimSpace(id) {
			return cloneProfile(profiles[index]), nil
		}
	}
	return nil, os.ErrNotExist
}

func (m *Manager) Save(input Profile) (PublicProfile, error) {
	if err := m.normalizeAndValidate(&input); err != nil {
		return PublicProfile{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	profiles, _, err := m.loadProfilesUnlocked(false)
	if err != nil {
		return PublicProfile{}, err
	}

	now := time.Now().UTC()
	replaced := false
	if input.ID == "" {
		input.ID = newID()
		input.CreatedAt = now
	} else {
		input.CreatedAt = now
		for index, existing := range profiles {
			if existing.ID != input.ID {
				continue
			}
			input.CreatedAt = existing.CreatedAt
			if input.SecretStore == "" {
				input.SecretStore = existing.SecretStore
			}
			input.Secrets = mergeSecrets(existing.Secrets, input.Secrets)
			if input.SecretStore == "" || input.SecretStore == existing.SecretStore {
				input.SecretRefs = mergeRefs(existing.SecretRefs, input.SecretRefs)
			}
			profiles[index] = input
			replaced = true
			break
		}
	}
	input.UpdatedAt = now
	applyLocalSecretReferenceDefaults(&input)

	if !replaced {
		profiles = append(profiles, input)
	}

	if err := m.saveProfilesUnlocked(profiles); err != nil {
		return PublicProfile{}, err
	}
	return publicProfile(input), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	profiles, _, err := m.loadProfilesUnlocked(false)
	if err != nil {
		return err
	}
	next := profiles[:0]
	found := false
	for _, profile := range profiles {
		if profile.ID == strings.TrimSpace(id) {
			found = true
			if err := m.deleteProfileSecrets(profile); err != nil {
				return err
			}
			continue
		}
		next = append(next, profile)
	}
	if !found {
		return os.ErrNotExist
	}
	return m.saveProfilesUnlocked(next)
}

func (m *Manager) loadProfilesUnlocked(resolveSecretRefs bool) ([]Profile, bool, error) {
	profiles, err := m.readProfilesUnlocked()
	if err != nil {
		return nil, false, err
	}
	needsMigration, err := m.loadProfileSecrets(profiles, resolveSecretRefs)
	if err != nil {
		return nil, false, err
	}
	return profiles, needsMigration, nil
}

func (m *Manager) readProfilesUnlocked() ([]Profile, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Profile{}, nil
		}
		return nil, fmt.Errorf("read agent provider connections: %w", err)
	}
	var profiles []Profile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, fmt.Errorf("parse agent provider connections: %w", err)
	}
	return profiles, nil
}

func (m *Manager) saveProfilesUnlocked(profiles []Profile) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("create agent provider connections directory: %w", err)
	}
	persisted, err := m.storeProfileSecrets(profiles)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent provider connections: %w", err)
	}
	if err := writeFileAtomic(m.path, data, 0o600); err != nil {
		return fmt.Errorf("write agent provider connections: %w", err)
	}
	return nil
}

func (m *Manager) storeProfileSecrets(profiles []Profile) ([]Profile, error) {
	out := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		next := profile
		next.Metadata = cloneMap(profile.Metadata)
		next.CostControls = cloneMap(profile.CostControls)
		next.Secrets = cloneMap(profile.Secrets)
		next.SecretRefs = cloneMap(profile.SecretRefs)
		next.SecretStore = strings.TrimSpace(next.SecretStore)
		store, ok := m.secretStoreFor(next.SecretStore)
		if !ok {
			if hasNonEmptySecrets(next.Secrets) {
				return nil, fmt.Errorf("agent provider connection %s uses unsupported secret store %q with local secret values", profileLabel(next), next.SecretStore)
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
		next.SecretRefs = mergeRefs(next.SecretRefs, stored.Refs)
		if len(next.Secrets) > 0 || len(next.SecretRefs) > 0 {
			next.SecretStore = store.Kind()
		}
		applyLocalSecretReferenceDefaults(&next)
		out = append(out, next)
	}
	return out, nil
}

func (m *Manager) loadProfileSecrets(profiles []Profile, resolveSecretRefs bool) (bool, error) {
	needsMigration := false
	for index := range profiles {
		profiles[index].SecretStore = strings.TrimSpace(profiles[index].SecretStore)
		store, ok := m.secretStoreFor(profiles[index].SecretStore)
		if !ok {
			if hasNonEmptySecrets(profiles[index].Secrets) {
				return false, fmt.Errorf("agent provider connection %s uses unsupported secret store %q with local secret values", profileLabel(profiles[index]), profiles[index].SecretStore)
			}
			continue
		}
		hasStoredValues := len(profiles[index].Secrets) > 0
		hasResolvableRefs := resolveSecretRefs && store.Kind() != cloudconnections.SecretStoreLocalEncrypted && len(profiles[index].SecretRefs) > 0
		if !hasStoredValues && !hasResolvableRefs {
			continue
		}
		loaded, err := store.Load(context.Background(), secretScope(profiles[index]), cloudconnections.StoredSecrets{
			Values: profiles[index].Secrets,
			Refs:   profiles[index].SecretRefs,
		})
		if err != nil {
			return false, err
		}
		profiles[index].Secrets = loaded.Values
		if loaded.NeedsMigration && hasNonEmptySecrets(loaded.Values) {
			needsMigration = true
		}
	}
	return needsMigration, nil
}

func (m *Manager) deleteProfileSecrets(profile Profile) error {
	profile.SecretStore = strings.TrimSpace(profile.SecretStore)
	store, ok := m.secretStoreFor(profile.SecretStore)
	if !ok {
		if hasNonEmptySecrets(profile.Secrets) {
			return fmt.Errorf("agent provider connection %s uses unsupported secret store %q with local secret values", profileLabel(profile), profile.SecretStore)
		}
		if hasNonEmptySecrets(profile.SecretRefs) {
			return fmt.Errorf("agent provider connection %s uses unsupported secret store %q with external secret refs; register the store before deleting", profileLabel(profile), profile.SecretStore)
		}
		return nil
	}
	if len(profile.Secrets) == 0 && len(profile.SecretRefs) == 0 {
		return nil
	}
	return store.Delete(context.Background(), secretScope(profile), cloudconnections.StoredSecrets{
		Values: cloneMap(profile.Secrets),
		Refs:   cloneMap(profile.SecretRefs),
	})
}

func (m *Manager) normalizeAndValidate(profile *Profile) error {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.ProviderID = strings.TrimSpace(profile.ProviderID)
	profile.CredentialMode = strings.TrimSpace(profile.CredentialMode)
	profile.SecretStore = strings.TrimSpace(profile.SecretStore)
	profile.Metadata = trimMap(profile.Metadata)
	profile.CostControls = trimMap(profile.CostControls)
	profile.Secrets = trimMapWithoutMasked(profile.Secrets)
	profile.SecretRefs = trimMap(profile.SecretRefs)

	if profile.ID != "" && !isSafeToken(profile.ID) {
		return errors.New("connection id contains unsupported characters")
	}
	if profile.Name == "" {
		return errors.New("connection name is required")
	}
	if profile.ProviderID == "" {
		return errors.New("provider_id is required")
	}
	if !isSafeToken(profile.ProviderID) {
		return errors.New("provider_id contains unsupported characters")
	}
	if profile.CredentialMode == "" {
		return errors.New("credential_mode is required")
	}
	if !isSafeToken(profile.CredentialMode) {
		return errors.New("credential_mode contains unsupported characters")
	}
	for key := range profile.Metadata {
		if !isSafeFieldKey(key) {
			return fmt.Errorf("metadata field %q contains unsupported characters", key)
		}
	}
	for key := range profile.CostControls {
		if !isSafeFieldKey(key) {
			return fmt.Errorf("cost control field %q contains unsupported characters", key)
		}
	}
	for key := range profile.Secrets {
		if !isSafeFieldKey(key) {
			return fmt.Errorf("secret field %q contains unsupported characters", key)
		}
	}
	for key := range profile.SecretRefs {
		if !isSafeFieldKey(key) {
			return fmt.Errorf("secret ref field %q contains unsupported characters", key)
		}
	}
	if profile.SecretStore != "" && !m.supportsSecretStore(profile.SecretStore) && hasNonEmptySecrets(profile.Secrets) {
		return fmt.Errorf("unsupported secret store %q", profile.SecretStore)
	}
	return nil
}

func (m *Manager) registerSecretStore(store cloudconnections.SecretStore) {
	if store == nil {
		return
	}
	kind := strings.TrimSpace(store.Kind())
	if kind == "" {
		return
	}
	m.secretStoresMu.Lock()
	defer m.secretStoresMu.Unlock()
	m.secretStores[kind] = store
}

func (m *Manager) supportsSecretStore(kind string) bool {
	_, ok := m.secretStoreFor(kind)
	return ok
}

func (m *Manager) secretStoreFor(kind string) (cloudconnections.SecretStore, bool) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = cloudconnections.SecretStoreLocalEncrypted
	}
	m.secretStoresMu.RLock()
	defer m.secretStoresMu.RUnlock()
	store, ok := m.secretStores[kind]
	return store, ok
}

func secretScope(profile Profile) cloudconnections.SecretScope {
	return cloudconnections.SecretScope{
		ConnectionID: profile.ID,
		Provider:     profile.ProviderID,
		AuthMethod:   profile.CredentialMode,
	}
}

func publicProfile(profile Profile) PublicProfile {
	secretStore := strings.TrimSpace(profile.SecretStore)
	if secretStore == "" && len(profile.Secrets) > 0 {
		secretStore = cloudconnections.SecretStoreLocalEncrypted
	}
	return PublicProfile{
		ID:             profile.ID,
		Name:           profile.Name,
		ProviderID:     profile.ProviderID,
		CredentialMode: profile.CredentialMode,
		Metadata:       cloneMap(profile.Metadata),
		CostControls:   cloneMap(profile.CostControls),
		SecretFields:   presentSecretFields(profile.Secrets, profile.SecretRefs),
		SecretStore:    secretStore,
		CreatedAt:      profile.CreatedAt,
		UpdatedAt:      profile.UpdatedAt,
	}
}

func applyLocalSecretReferenceDefaults(profile *Profile) {
	if profile.SecretStore != "" && profile.SecretStore != cloudconnections.SecretStoreLocalEncrypted {
		return
	}
	if len(profile.Secrets) == 0 {
		if len(profile.SecretRefs) == 0 {
			profile.SecretStore = ""
		}
		return
	}
	profile.SecretStore = cloudconnections.SecretStoreLocalEncrypted
	refs := cloneMap(profile.SecretRefs)
	if refs == nil {
		refs = map[string]string{}
	}
	for key, value := range profile.Secrets {
		if strings.TrimSpace(value) != "" {
			refs[key] = "local://agent-provider-connections/" + profile.ID + "/" + key
		}
	}
	profile.SecretRefs = refs
}

func profileLabel(profile Profile) string {
	if profile.Name == "" {
		return fmt.Sprintf("%q", profile.ID)
	}
	return fmt.Sprintf("%q (%s)", profile.ID, profile.Name)
}

func presentSecretFields(secrets, refs map[string]string) []string {
	fields := map[string]bool{}
	for key, value := range secrets {
		if strings.TrimSpace(value) != "" {
			fields[key] = true
		}
	}
	for key, value := range refs {
		if strings.TrimSpace(value) != "" {
			fields[key] = true
		}
	}
	out := make([]string, 0, len(fields))
	for field := range fields {
		out = append(out, field)
	}
	sort.Strings(out)
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

func mergeRefs(existing, submitted map[string]string) map[string]string {
	out := cloneMap(existing)
	for key, value := range submitted {
		if value = strings.TrimSpace(value); value != "" {
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

func trimMapWithoutMasked(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		if key = strings.TrimSpace(key); key != "" {
			if value = strings.TrimSpace(value); value != "" && !isMasked(value) {
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

func cloneProfile(profile Profile) *Profile {
	copied := profile
	copied.Metadata = cloneMap(profile.Metadata)
	copied.CostControls = cloneMap(profile.CostControls)
	copied.Secrets = cloneMap(profile.Secrets)
	copied.SecretRefs = cloneMap(profile.SecretRefs)
	return &copied
}

func hasNonEmptySecrets(secrets map[string]string) bool {
	for _, value := range secrets {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func isMasked(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r != '*' {
			return false
		}
	}
	return true
}

func isSafeToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isSafeFieldKey(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func newID() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("apc_%d", time.Now().UnixNano())
	}
	return "apc_" + hex.EncodeToString(bytes[:])
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
