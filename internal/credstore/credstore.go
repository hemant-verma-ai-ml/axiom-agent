// Package credstore provides credential storage for axiom-agent.
//
// Two backends, one interface:
//   - keyringStore: per-user install, backed by the OS credential
//     store (Windows Credential Manager / macOS Keychain / Linux
//     Secret Service) via zalando/go-keyring.
//   - fileStore: system-install/headless path, a custom AES-256-GCM
//     encrypted file. The key is a locally-generated random file with
//     no interactive passphrase, since a non-interactive systemd
//     service can't prompt for one at boot. Deliberately NOT derived
//     from /etc/machine-id: that file is world-readable by default
//     and systemd itself documents it as non-confidential, so it
//     would provide no real protection as sole key material. Same
//     trust model as an SSH host key instead — see
//     scripts/install-system.sh.
//
// See ADR-017 §4 and AXIOM master prompt v3.4, Tier 2 Task 7.2 #8.
package credstore

// CredentialStore is the common interface both backends implement.
type CredentialStore interface {
	// Get retrieves the value stored under key. Returns an error if
	// the key does not exist or the store cannot be read/decrypted.
	Get(key string) (string, error)

	// Set stores value under key, creating or overwriting it.
	Set(key, value string) error

	// Delete removes key from the store. Not an error if key is
	// already absent.
	Delete(key string) error
}

// ServiceName identifies this application to the OS keychain
// (keyring backend) and namespaces entries internally.
const ServiceName = "axiom-agent"
