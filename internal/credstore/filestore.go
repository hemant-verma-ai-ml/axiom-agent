package credstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Default paths for the system-install fallback. Overridable via
// NewFileStore for testing (see filestore_test.go, which never
// touches these real paths).
const (
	DefaultKeyPath  = "/var/lib/axiom-agent/key.bin"
	DefaultDataPath = "/var/lib/axiom-agent/credentials.enc"
)

// fileStore backs CredentialStore with a custom AES-256-GCM encrypted
// file, keyed by a random 32-byte file provisioned once at install
// time (see scripts/install-system.sh) — 0600, owned by the
// axiom-agent service account.
type fileStore struct {
	keyPath  string
	dataPath string
}

// NewFileStore returns a CredentialStore backed by an encrypted file
// at dataPath, keyed by the key at keyPath. Both paths must already
// exist with correct ownership/permissions — this constructor does
// not create or provision them (that's install-system.sh's job).
func NewFileStore(keyPath, dataPath string) CredentialStore {
	return &fileStore{keyPath: keyPath, dataPath: dataPath}
}

// NewDefaultFileStore returns a CredentialStore using the standard
// system-install paths (DefaultKeyPath, DefaultDataPath).
func NewDefaultFileStore() CredentialStore {
	return NewFileStore(DefaultKeyPath, DefaultDataPath)
}

func (f *fileStore) loadKey() ([]byte, error) {
	key, err := os.ReadFile(f.keyPath)
	if err != nil {
		return nil, fmt.Errorf("credstore: read key file %s: %w (has scripts/install-system.sh been run?)", f.keyPath, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("credstore: key file %s is %d bytes, want 32 — corrupt or wrong file", f.keyPath, len(key))
	}
	return key, nil
}

func (f *fileStore) loadData() (map[string]string, error) {
	data := map[string]string{}
	raw, err := os.ReadFile(f.dataPath)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil // empty store, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("credstore: read data file %s: %w", f.dataPath, err)
	}
	if len(raw) == 0 {
		return data, nil
	}

	key, err := f.loadKey()
	if err != nil {
		return nil, err
	}
	plaintext, err := decrypt(key, raw)
	if err != nil {
		return nil, fmt.Errorf("credstore: decrypt %s: %w", f.dataPath, err)
	}
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("credstore: parse decrypted data: %w", err)
	}
	return data, nil
}

func (f *fileStore) saveData(data map[string]string) error {
	key, err := f.loadKey()
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("credstore: marshal data: %w", err)
	}
	ciphertext, err := encrypt(key, plaintext)
	if err != nil {
		return fmt.Errorf("credstore: encrypt: %w", err)
	}

	// Write to a temp file in the same directory, then rename — avoids
	// a torn/partial write on crash (same discipline as config.Save()
	// elsewhere in this codebase).
	dir := filepath.Dir(f.dataPath)
	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("credstore: create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once rename succeeds

	if _, err := tmp.Write(ciphertext); err != nil {
		tmp.Close()
		return fmt.Errorf("credstore: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("credstore: close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("credstore: chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, f.dataPath); err != nil {
		return fmt.Errorf("credstore: rename into place: %w", err)
	}
	return nil
}

func (f *fileStore) Get(key string) (string, error) {
	data, err := f.loadData()
	if err != nil {
		return "", err
	}
	val, ok := data[key]
	if !ok {
		return "", fmt.Errorf("credstore: key %q not found in %s", key, f.dataPath)
	}
	return val, nil
}

func (f *fileStore) Set(key, value string) error {
	data, err := f.loadData()
	if err != nil {
		return err
	}
	data[key] = value
	return f.saveData(data)
}

func (f *fileStore) Delete(key string) error {
	data, err := f.loadData()
	if err != nil {
		return err
	}
	if _, ok := data[key]; !ok {
		return nil // already absent, not an error
	}
	delete(data, key)
	return f.saveData(data)
}

// encrypt seals plaintext with AES-256-GCM under key, prepending the
// random nonce to the returned ciphertext.
func encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt reverses encrypt: ciphertext must be nonce||sealed as
// produced by encrypt.
func decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext shorter than nonce size — corrupt file")
	}
	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open (wrong key, or corrupt/tampered data): %w", err)
	}
	return plaintext, nil
}
