// Package config implements the single shared config file used by both the
// daemon and the GUI (ADR-017 §3) — one source of truth for watched paths,
// not two configs that can drift apart.
//
// Verified: `go build ./...` and `go vet ./...` pass clean as of AXIOM-S9
// (2026-08-13), module-wide, including this file.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is persisted as JSON under the OS config directory
// (os.UserConfigDir()/axiom-agent/config.json on a per-user install;
// an OS-appropriate system path when installed with --system — see
// ADR-017 §4 for the credential-storage split this mirrors).
type Config struct {
	// WatchPaths are directories the daemon watches automatically.
	// The GUI appends to this list on first submission of a new folder
	// unless the user opts out via "run once, don't watch" (ADR-017 §3).
	WatchPaths []string `json:"watch_paths"`

	// DebounceSeconds is the quiet period after the last detected change
	// before a trigger fires. Default per ADR-019: 30.
	DebounceSeconds int `json:"debounce_seconds"`

	// ReconcileMinutes is the interval for the backstop walk that catches
	// anything the native watch silently missed (e.g. an exceeded
	// inotify watch limit). Default per ADR-019: 10.
	ReconcileMinutes int `json:"reconcile_minutes"`
}

func Default() Config {
	return Config{
		WatchPaths:       []string{},
		DebounceSeconds:  30,
		ReconcileMinutes: 10,
	}
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "axiom-agent", "config.json"), nil
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// AddWatchPath appends a new path if not already present, then persists.
// This is the hook the GUI calls on first submission of a new folder,
// implementing the shared-config default from ADR-017 §3.
func AddWatchPath(cfg *Config, path string) error {
	for _, p := range cfg.WatchPaths {
		if p == path {
			return nil
		}
	}
	cfg.WatchPaths = append(cfg.WatchPaths, path)
	return Save(*cfg)
}
