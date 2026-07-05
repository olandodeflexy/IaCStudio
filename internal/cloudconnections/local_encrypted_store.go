package cloudconnections

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type localEncryptedSecretStore struct {
	keyForEncrypt func() ([]byte, error)
	keyForDecrypt func() ([]byte, error)
	encryptOnce   sync.Once
	encryptKey    []byte
	encryptErr    error
	decryptOnce   sync.Once
	decryptKey    []byte
	decryptErr    error
}

func newLocalEncryptedSecretStore(keyForEncrypt, keyForDecrypt func() ([]byte, error)) *localEncryptedSecretStore {
	return &localEncryptedSecretStore{
		keyForEncrypt: keyForEncrypt,
		keyForDecrypt: keyForDecrypt,
	}
}

// NewLocalEncryptedFileSecretStore returns the same local encrypted secret
// backend used by Cloud Connections, backed by key material at keyPath.
func NewLocalEncryptedFileSecretStore(keyPath string) SecretStore {
	return newLocalEncryptedSecretStore(
		func() ([]byte, error) {
			if key, ok := environmentEncryptionKey(); ok {
				return key, nil
			}
			return loadOrCreateLocalKey(keyPath)
		},
		func() ([]byte, error) {
			if key, ok := environmentEncryptionKey(); ok {
				return key, nil
			}
			return loadExistingLocalKey(keyPath)
		},
	)
}

func (s *localEncryptedSecretStore) Kind() string {
	return SecretStoreLocalEncrypted
}

func (s *localEncryptedSecretStore) Save(ctx context.Context, scope SecretScope, secrets map[string]string) (StoredSecrets, error) {
	if err := ctx.Err(); err != nil {
		return StoredSecrets{}, err
	}

	values := map[string]string{}
	var key []byte
	for name, value := range secrets {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if key == nil {
			loaded, err := s.encryptionKey()
			if err != nil {
				return StoredSecrets{}, err
			}
			key = loaded
		}
		encrypted, err := encryptSecretValue(key, value)
		if err != nil {
			return StoredSecrets{}, fmt.Errorf("encrypt cloud connection secret %q: %w", name, err)
		}
		values[name] = encrypted
	}
	if len(values) == 0 {
		return StoredSecrets{}, nil
	}
	return StoredSecrets{
		Values: values,
		Refs:   localEncryptedSecretRefs(scope.ConnectionID, scope.AuthMethod, values),
	}, nil
}

func (s *localEncryptedSecretStore) Load(ctx context.Context, _ SecretScope, stored StoredSecrets) (LoadedSecrets, error) {
	if err := ctx.Err(); err != nil {
		return LoadedSecrets{}, err
	}

	values := map[string]string{}
	needsMigration := false
	var key []byte
	for name, value := range stored.Values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if !isEncryptedSecret(value) {
			needsMigration = true
			values[name] = value
			continue
		}
		if key == nil {
			loaded, err := s.decryptionKey()
			if err != nil {
				return LoadedSecrets{}, err
			}
			key = loaded
		}
		plaintext, err := decryptSecretValue(key, value)
		if err != nil {
			return LoadedSecrets{}, fmt.Errorf("decrypt cloud connection secret %q: %w", name, err)
		}
		values[name] = plaintext
	}
	if len(values) == 0 {
		values = nil
	}
	return LoadedSecrets{
		Values:         values,
		NeedsMigration: needsMigration,
	}, nil
}

func (s *localEncryptedSecretStore) Delete(ctx context.Context, _ SecretScope, _ StoredSecrets) error {
	return ctx.Err()
}

func (s *localEncryptedSecretStore) encryptionKey() ([]byte, error) {
	s.encryptOnce.Do(func() {
		s.encryptKey, s.encryptErr = s.keyForEncrypt()
	})
	return s.encryptKey, s.encryptErr
}

func (s *localEncryptedSecretStore) decryptionKey() ([]byte, error) {
	s.decryptOnce.Do(func() {
		s.decryptKey, s.decryptErr = s.keyForDecrypt()
	})
	return s.decryptKey, s.decryptErr
}
