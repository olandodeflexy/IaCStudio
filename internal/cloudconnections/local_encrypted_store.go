package cloudconnections

import (
	"context"
	"fmt"
	"strings"
)

type localEncryptedSecretStore struct {
	keyForEncrypt func() ([]byte, error)
	keyForDecrypt func() ([]byte, error)
}

func newLocalEncryptedSecretStore(keyForEncrypt, keyForDecrypt func() ([]byte, error)) *localEncryptedSecretStore {
	return &localEncryptedSecretStore{
		keyForEncrypt: keyForEncrypt,
		keyForDecrypt: keyForDecrypt,
	}
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
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if key == nil {
			loaded, err := s.keyForEncrypt()
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
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !isEncryptedSecret(value) {
			needsMigration = true
			values[name] = value
			continue
		}
		if key == nil {
			loaded, err := s.keyForDecrypt()
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
