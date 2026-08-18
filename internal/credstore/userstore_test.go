package credstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewDefaultUserFileStoreProvisioning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	store, err := NewDefaultUserFileStore()
	if err != nil {
		t.Fatalf("NewDefaultUserFileStore: %v", err)
	}

	keyPath := filepath.Join(dir, "axiom-agent", "key.bin")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("expected key file to be created at %s: %v", keyPath, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file permissions = %v, want 0600", info.Mode().Perm())
	}

	const testVal = "axk_migration_test_456"
	if err := store.Set("AXIOM_AGENT_API_KEY", testVal); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get("AXIOM_AGENT_API_KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != testVal {
		t.Fatalf("Get = %q, want %q", got, testVal)
	}

	// The real bug this guards against: re-provisioning (e.g. a
	// second daemon start) must NOT regenerate/rotate the key. If it
	// did, this would silently orphan a real, working credential --
	// the upload path would start failing with no obvious cause,
	// exactly the class of regression this whole migration must not
	// introduce.
	store2, err := NewDefaultUserFileStore()
	if err != nil {
		t.Fatalf("second NewDefaultUserFileStore (simulating daemon restart): %v", err)
	}
	got2, err := store2.Get("AXIOM_AGENT_API_KEY")
	if err != nil {
		t.Fatalf("Get after re-provisioning: %v", err)
	}
	if got2 != testVal {
		t.Fatalf("value changed after re-provisioning: got %q, want %q -- key was silently rotated", got2, testVal)
	}
}
