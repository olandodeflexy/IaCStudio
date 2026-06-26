package cloudconnections

import "context"

// SecretScope identifies the connection context a SecretStore uses when saving
// or resolving provider credential fields.
type SecretScope struct {
	ConnectionID string
	Provider     string
	AuthMethod   string
}

// SecretStore is the backend contract for storing cloud credential fields
// outside public connection metadata. Implementations return opaque refs that
// can be persisted in Connection.SecretRefs and resolved only for runner or MCP
// workflows that need credentials.
type SecretStore interface {
	Kind() string
	Save(ctx context.Context, scope SecretScope, secrets map[string]string) (map[string]string, error)
	Load(ctx context.Context, refs map[string]string) (map[string]string, error)
	Delete(ctx context.Context, refs map[string]string) error
}
