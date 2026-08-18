package credstore

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// User-scoped credential storage for the per-user-headless case: a
// daemon running under a normal user account (not the dedicated
// axiom-agent system user), with no OS keyring service available --
// i5's actual real deployment today. Reuses the identical
// AES-256-GCM fileStore backend as the system-install path; only the
// location and provisioning (no root required) differ.
//
// Real, deliberate reasoning for not using a keyring here instead:
// gnome-keyring's non-interactive auto-unlock depends on PAM
// capturing a login password during an interactive session. This
// daemon is intentionally started by systemd independent of any
// login (that's the whole point of Tier 2 #7's linger fix), so there
// is no such moment to hook into. The realistic non-interactive
// options collapse to a blank-password keyring -- no stronger than
// this file-based key, just with an extra D-Bus service in the loop
// and a false appearance of added security. That would be security
// theater, not institutional grade.

// UserKeyPath returns the per-user key file location:
// os.UserConfigDir()/axiom-agent/key.bin -- alongside config.json.
func UserKeyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("credstore: resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "axiom-agent", "key.bin"), nil
}

// UserDataPath returns the per-user encrypted credentials location:
// os.UserConfigDir()/axiom-agent/credentials.enc.
func UserDataPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("credstore: resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "axiom-agent", "credentials.enc"), nil
}

// NewDefaultUserFileStore returns a CredentialStore rooted at the
// user's config directory, generating the key file if it doesn't
// already exist yet (the per-user analog of
// scripts/install-system.sh -- same key-generation logic, no root
// required).
func NewDefaultUserFileStore() (CredentialStore, error) {
	keyPath, err := UserKeyPath()
	if err != nil {
		return nil, err
	}
	dataPath, err := UserDataPath()
	if err != nil {
		return nil, err
	}
	if err := ensureUserKeyFile(keyPath); err != nil {
		return nil, err
	}
	return NewFileStore(keyPath, dataPath), nil
}

// ensureUserKeyFile generates a fresh random 32-byte key at path if
// none exists yet. Idempotent by design -- an existing key is never
// touched or rotated implicitly. Rotating it silently would
// invalidate any already-encrypted credentials, orphaning a real,
// working credential on nothing more than a routine daemon restart
// (same rule scripts/install-system.sh documents for the
// system-install path -- see TestNewDefaultUserFileStoreProvisioning
// for the regression test that guards this specifically).
func ensureUserKeyFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already provisioned, leave it alone
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("credstore: stat key file %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("credstore: create key dir %s: %w", dir, err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("credstore: generate key: %w", err)
	}

	// Atomic write, same discipline as fileStore.saveData -- avoids a
	// torn/partial key file on crash.
	tmp, err := os.CreateTemp(dir, ".key-*.tmp")
	if err != nil {
		return fmt.Errorf("credstore: create temp key file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once rename succeeds

	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return fmt.Errorf("credstore: write temp key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("credstore: close temp key file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("credstore: chmod temp key file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("credstore: rename key file into place: %w", err)
	}
	return nil
}
