package cloudconnections

import "context"

// SecretScope identifies the connection context a SecretStore uses when saving
// or resolving provider credential fields.
type SecretScope struct {
	ConnectionID string
	Provider     string
	AuthMethod   string
}

// StoredSecrets is the persisted output of a SecretStore save operation.
// Values are backend-specific persisted values. For local encrypted storage
// these are encrypted envelopes; for external stores this will usually be nil.
type StoredSecrets struct {
	Values map[string]string
	Refs   map[string]string
}

// LoadedSecrets is the resolved output of a SecretStore load operation.
type LoadedSecrets struct {
	Values         map[string]string
	NeedsMigration bool
}

// SecretStore is the backend contract for storing cloud credential fields
// outside public connection metadata. Implementations return opaque refs that
// can be persisted in Connection.SecretRefs and resolved only for runner or MCP
// workflows that need credentials.
type SecretStore interface {
	Kind() string
	Save(ctx context.Context, scope SecretScope, secrets map[string]string) (StoredSecrets, error)
	Load(ctx context.Context, scope SecretScope, stored StoredSecrets) (LoadedSecrets, error)
	Delete(ctx context.Context, scope SecretScope, stored StoredSecrets) error
}
