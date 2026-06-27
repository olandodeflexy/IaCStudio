package cloudconnections

import (
	"sort"
	"strconv"
	"strings"
)

// Option customizes a Cloud Connections manager.
type Option func(*Manager)

// WithSecretStore registers a secret store backend. Later registrations for the
// same kind replace earlier ones, which keeps tests and future backend wiring
// straightforward while preserving fail-closed lookup behavior.
func WithSecretStore(store SecretStore) Option {
	return func(m *Manager) {
		m.registerSecretStore(store)
	}
}

// SupportedSecretStores returns the registered backend kinds for diagnostics
// and future UI/API discovery.
func (m *Manager) SupportedSecretStores() []string {
	kinds := make([]string, 0, len(m.secretStores))
	for kind := range m.secretStores {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func (m *Manager) normalizeAndValidate(connection *Connection) error {
	if err := normalizeConnectionFields(connection); err != nil {
		return err
	}
	if connection.SecretStore != "" && !m.supportsSecretStore(connection.SecretStore) {
		return unsupportedSecretStoreError(connection.SecretStore)
	}
	return nil
}

func (m *Manager) registerSecretStore(store SecretStore) {
	if store == nil {
		return
	}
	kind := strings.TrimSpace(store.Kind())
	if kind == "" {
		return
	}
	m.secretStores[kind] = store
}

func (m *Manager) supportsSecretStore(kind string) bool {
	_, ok := m.secretStoreFor(kind)
	return ok
}

func (m *Manager) secretStoreFor(kind string) (SecretStore, bool) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = SecretStoreLocalEncrypted
	}
	store, ok := m.secretStores[kind]
	return store, ok
}

func unsupportedSecretStoreError(kind string) error {
	return errUnsupportedSecretStore{kind: kind}
}

type errUnsupportedSecretStore struct {
	kind string
}

func (e errUnsupportedSecretStore) Error() string {
	return "unsupported secret store " + strconv.Quote(e.kind)
}
