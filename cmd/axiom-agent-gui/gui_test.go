package main

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"

	"github.com/hemant-verma-ai-ml/axiom-agent/internal/config"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/credstore"
)

// These tests exercise credentialsReady and validateDroppedPath
// directly -- neither creates a window or app, so no display/Xvfb is
// required. Runtime-level verification (does the actual window build
// and run) is covered separately via Xvfb, same as S10; this file
// covers the validation logic itself, which Xvfb's "doesn't crash on
// startup" check does not.

func TestCredentialsReadyFalseWhenNothingConfigured(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if credentialsReady() {
		t.Fatal("credentialsReady() = true with nothing configured, want false")
	}
}

func TestCredentialsReadyFalseWithOnlyServerURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.ServerURL = "https://axiom.example.com"
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if credentialsReady() {
		t.Fatal("credentialsReady() = true with only ServerURL set, want false (API key still missing)")
	}
}

func TestCredentialsReadyTrueWhenBothSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.ServerURL = "https://axiom.example.com"
	if err := config.Save(cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	store, err := credstore.NewDefaultUserFileStore()
	if err != nil {
		t.Fatalf("NewDefaultUserFileStore: %v", err)
	}
	if err := store.Set("AXIOM_AGENT_API_KEY", "axk_test"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if !credentialsReady() {
		t.Fatal("credentialsReady() = false with both ServerURL and API key set, want true")
	}
}

func TestValidateDroppedPathRejectsMultiple(t *testing.T) {
	dir := t.TempDir()
	u1 := storage.NewFileURI(dir)
	u2 := storage.NewFileURI(dir)
	if _, err := validateDroppedPath([]fyne.URI{u1, u2}); err == nil {
		t.Fatal("validateDroppedPath with 2 items succeeded, want error")
	}
}

func TestValidateDroppedPathRejectsZero(t *testing.T) {
	if _, err := validateDroppedPath([]fyne.URI{}); err == nil {
		t.Fatal("validateDroppedPath with 0 items succeeded, want error")
	}
}

func TestValidateDroppedPathRejectsNonFileScheme(t *testing.T) {
	u := storage.NewURI("https://example.com/somewhere")
	if _, err := validateDroppedPath([]fyne.URI{u}); err == nil {
		t.Fatal("validateDroppedPath with https:// scheme succeeded, want error")
	}
}

func TestValidateDroppedPathRejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-dir.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	u := storage.NewFileURI(filePath)
	if _, err := validateDroppedPath([]fyne.URI{u}); err == nil {
		t.Fatal("validateDroppedPath on a plain file succeeded, want error")
	}
}

func TestValidateDroppedPathRejectsNonexistentPath(t *testing.T) {
	dir := t.TempDir()
	u := storage.NewFileURI(filepath.Join(dir, "does-not-exist"))
	if _, err := validateDroppedPath([]fyne.URI{u}); err == nil {
		t.Fatal("validateDroppedPath on a nonexistent path succeeded, want error")
	}
}

func TestValidateDroppedPathAcceptsRealDirectory(t *testing.T) {
	dir := t.TempDir()
	u := storage.NewFileURI(dir)
	got, err := validateDroppedPath([]fyne.URI{u})
	if err != nil {
		t.Fatalf("validateDroppedPath on a real directory failed: %v", err)
	}
	if got != dir {
		t.Fatalf("validateDroppedPath returned %q, want %q", got, dir)
	}
}
