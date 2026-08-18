package credstore

import (
	"fmt"

	"github.com/zalando/go-keyring"
)

// keyringStore backs CredentialStore with the OS-native credential
// store via zalando/go-keyring. Intended for the per-user install
// path (interactive desktop use — the Fyne GUI, or a per-user daemon
// like the one running under i5's own account today).
type keyringStore struct {
	service string
}

// NewKeyringStore returns a CredentialStore backed by the OS keychain.
func NewKeyringStore() CredentialStore {
	return &keyringStore{service: ServiceName}
}

func (k *keyringStore) Get(key string) (string, error) {
	val, err := keyring.Get(k.service, key)
	if err != nil {
		return "", fmt.Errorf("credstore: keyring get %q: %w", key, err)
	}
	return val, nil
}

func (k *keyringStore) Set(key, value string) error {
	if err := keyring.Set(k.service, key, value); err != nil {
		return fmt.Errorf("credstore: keyring set %q: %w", key, err)
	}
	return nil
}

func (k *keyringStore) Delete(key string) error {
	err := keyring.Delete(k.service, key)
	if err != nil && err != keyring.ErrNotFound {
		return fmt.Errorf("credstore: keyring delete %q: %w", key, err)
	}
	return nil
}
