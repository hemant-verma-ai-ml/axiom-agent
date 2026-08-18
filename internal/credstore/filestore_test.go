package credstore

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.bin")
	dataPath := filepath.Join(dir, "credentials.enc")

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatalf("write test key: %v", err)
	}

	store := NewFileStore(keyPath, dataPath)
	const testVal = "axk_test_value_123"

	if err := store.Set("AXIOM_AGENT_API_KEY", testVal); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get("AXIOM_AGENT_API_KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != testVal {
		t.Fatalf("Get returned %q, want %q", got, testVal)
	}

	// The file on disk must not contain the plaintext anywhere — the
	// entire point of this backend.
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("read data file: %v", err)
	}
	if bytes.Contains(raw, []byte(testVal)) {
		t.Fatal("data file contains unencrypted plaintext — encryption is not working")
	}

	// A wrong key must fail to decrypt, not silently return garbage
	// or someone else's data.
	wrongKey := make([]byte, 32)
	if _, err := rand.Read(wrongKey); err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}
	wrongKeyPath := filepath.Join(dir, "wrong-key.bin")
	if err := os.WriteFile(wrongKeyPath, wrongKey, 0o600); err != nil {
		t.Fatalf("write wrong key: %v", err)
	}
	wrongStore := NewFileStore(wrongKeyPath, dataPath)
	if _, err := wrongStore.Get("AXIOM_AGENT_API_KEY"); err == nil {
		t.Fatal("Get with wrong key succeeded, want decryption failure")
	}

	// Delete then Get must fail cleanly, not panic or return stale data.
	if err := store.Delete("AXIOM_AGENT_API_KEY"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get("AXIOM_AGENT_API_KEY"); err == nil {
		t.Fatal("Get after Delete succeeded, want not-found error")
	}
}

func TestFileStoreMissingKeyFile(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(
		filepath.Join(dir, "nonexistent-key.bin"),
		filepath.Join(dir, "credentials.enc"),
	)
	if err := store.Set("X", "Y"); err == nil {
		t.Fatal("Set with missing key file succeeded, want error")
	}
}
