// Package watcher implements ADR-019's hybrid trigger: a native fsnotify
// watch as the primary trigger, backed by a low-frequency reconciliation
// walk that catches anything the native watch silently missed — the same
// pattern used by VS Code's own file watcher, for the same reason (Linux's
// per-user inotify watch limit is a real ceiling a large repo can exceed,
// and raising it needs root, which the locked per-user install cannot do).
//
// Real, second reason the inotify ceiling matters more than it first
// looked: without exclusion logic, a naive recursive watch adds every
// real directory on disk under a root — including .git's own internal
// object store and anything the client's .gitignore excludes
// (node_modules, .venv, build artifacts, whatever their stack uses).
// That can multiply the real watch count by orders of magnitude on an
// ordinary repo, making the inotify ceiling a near-certainty rather than
// an edge case. Fixed here by asking git itself what it already knows
// via `git ls-files --others --ignored --exclude-standard --directory`
// rather than maintaining a guessed, always-incomplete hardcoded list of
// "common" directories to skip.
//
// Verified: `go build ./...` and `go vet ./...` pass clean module-wide
// as of AXIOM-S10 (2026-08-17), including this file's gitignore-aware
// exclusion logic.
package watcher

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/hemant-verma-ai-ml/axiom-agent/internal/config"
)

// TriggerFunc is called once, per watched root, after the debounce window
// elapses with no further changes.
type TriggerFunc func(rootPath string)

type Watcher struct {
	cfg     config.Config
	trigger TriggerFunc
	fsw     *fsnotify.Watcher

	mu      sync.Mutex
	pending map[string]*time.Timer // root path -> pending debounce timer

	// ignoredDirs caches, per watched root, the set of absolute
	// directory paths git itself considers ignored (untracked +
	// .gitignore-matched). Only ever read/written from the single Run()
	// goroutine (addRecursive, handleEvent, runReconciliation all run
	// synchronously from Run's select loop) — no mutex needed, unlike
	// `pending`, which is also touched from debounce timer callbacks on
	// their own goroutines.
	ignoredDirs map[string]map[string]bool

	// reload receives a signal (sent by Reload(), called from main.go's
	// SIGHUP handler) telling Run's select loop to re-read config.json
	// from disk and start watching any newly-added paths. Buffered size
	// 1: a signal sent while a reload is already pending is dropped
	// rather than blocking the sender. A coalesced extra reload is
	// harmless -- addRecursive on an already-watched root is a safe
	// no-op, the same property runReconciliation already relies on
	// every 10 minutes. Only ADDED paths are picked up live; a path
	// REMOVED from config still requires a restart, since nothing in
	// this package calls fsw.Remove (same real, named limitation
	// shouldSkip's doc comment already states for the gitignore case).
	reload chan struct{}
}

func New(cfg config.Config, trigger TriggerFunc) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		cfg:         cfg,
		trigger:     trigger,
		fsw:         fsw,
		pending:     make(map[string]*time.Timer),
		ignoredDirs: make(map[string]map[string]bool),
		reload:      make(chan struct{}, 1),
	}, nil
}

// Reload sends a non-blocking signal telling Run's select loop to re-read
// config.json and start watching any newly-added paths. Safe to call from
// any goroutine (e.g. a SIGHUP handler) -- never touches watcher state
// directly, only notifies Run's own goroutine to do so, preserving the
// single-goroutine invariant addRecursive/handleEvent/runReconciliation
// already depend on.
func (w *Watcher) Reload() {
	select {
	case w.reload <- struct{}{}:
	default:
		// A reload is already pending; this one is redundant.
	}
}

// Run blocks until stop is closed. It starts the native watch on every
// configured root (recursively adding subdirectories as they appear) and
// the reconciliation ticker in parallel.
func (w *Watcher) Run(stop <-chan struct{}) error {
	for _, root := range w.cfg.WatchPaths {
		if err := w.addRecursive(root); err != nil {
			// Non-fatal: log and continue with other roots. A single bad
			// path (e.g. deleted since config was saved) should not take
			// down watching for the rest.
			log.Printf("watcher: failed to watch %s: %v", root, err)
		}
	}
	log.Printf("watcher: initial watch setup complete for %d root(s) — entering event loop", len(w.cfg.WatchPaths))

	reconcile := time.NewTicker(time.Duration(w.cfg.ReconcileMinutes) * time.Minute)
	defer reconcile.Stop()

	for {
		select {
		case <-stop:
			return w.fsw.Close()

		case event, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(event)

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher: fsnotify error: %v", err)

		case <-reconcile.C:
			w.runReconciliation()

		case <-w.reload:
			w.reloadConfig()
		}
	}
}

// reloadConfig re-reads config.json from disk and starts watching any
// paths present in the fresh config but not yet in w.cfg.WatchPaths.
// Runs only from Run's own goroutine (the reload channel case), so it
// shares the same single-goroutine safety as addRecursive/handleEvent --
// no mutex needed here either, consistent with ignoredDirs' documented
// invariant above.
func (w *Watcher) reloadConfig() {
	fresh, err := config.Load()
	if err != nil {
		log.Printf("watcher: reload failed to load config: %v", err)
		return
	}

	existing := make(map[string]bool, len(w.cfg.WatchPaths))
	for _, p := range w.cfg.WatchPaths {
		existing[p] = true
	}

	for _, root := range fresh.WatchPaths {
		if existing[root] {
			continue
		}
		log.Printf("watcher: reload picked up new watch path %s", root)
		if err := w.addRecursive(root); err != nil {
			log.Printf("watcher: reload failed to watch %s: %v", root, err)
			continue
		}
		w.cfg.WatchPaths = append(w.cfg.WatchPaths, root)
	}
}

// computeIgnoredDirs asks git itself which directories under root are
// ignored, rather than guessing. --others limits to untracked paths,
// --ignored + --exclude-standard applies the client's real .gitignore
// (plus .git/info/exclude and any global gitignore), --directory reports
// a whole ignored directory as one entry instead of recursing into it —
// exactly the granularity needed to skip a whole subtree with one
// filepath.SkipDir rather than checking every file inside it.
//
// Real, honest limitation: if this isn't a git repo, git isn't
// installed, or the command fails for any other reason, this returns an
// empty set and an error — callers fall back to watching everything
// rather than silently skipping watch setup entirely. A real repo with
// a working .gitignore should never hit this path in practice.
func computeIgnoredDirs(root string) (map[string]bool, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory")
	out, err := cmd.Output()
	if err != nil {
		return map[string]bool{}, err
	}

	ignored := make(map[string]bool)
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return ignored, nil
	}
	for _, line := range strings.Split(trimmed, "\n") {
		if line == "" {
			continue
		}
		// git reports paths relative to root, directories with a
		// trailing slash thanks to --directory.
		absPath := filepath.Join(root, strings.TrimSuffix(line, "/"))
		ignored[absPath] = true
	}
	return ignored, nil
}

// addRecursive walks root, adding a real fsnotify watch on every
// directory EXCEPT .git (always, unconditionally — it's never source,
// and its own object store can be large enough alone to matter) and
// anything git itself reports as ignored for this repo.
func (w *Watcher) addRecursive(root string) error {
	ignored, err := computeIgnoredDirs(root)
	if err != nil {
		log.Printf("watcher: could not compute gitignore exclusions for %s (%v) — watching everything under it, which may be slower or risk the inotify limit on a large repo", root, err)
		ignored = map[string]bool{}
	}
	w.ignoredDirs[root] = ignored

	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if ignored[path] {
			return filepath.SkipDir
		}
		return w.fsw.Add(path)
	})
}

// shouldSkip applies the same two real exclusion rules addRecursive uses
// (unconditional .git, cached gitignore set for this root) to a single
// path — used by handleEvent's dynamic add-on-Create path, so a
// directory created mid-session (e.g. `npm install` creating
// node_modules) is excluded the same way the initial walk would have
// excluded it, not just at startup.
//
// Real, named limitation: ignoredDirs is only refreshed when
// addRecursive runs for a root — at startup and at each reconciliation
// pass (default every 10 minutes per ADR-019). A .gitignore edited
// mid-session won't affect already-scheduled watches until the next
// reconciliation, and even then, nothing in this package currently
// calls fsw.Remove — a watch already added before something became
// ignored is never actively removed, only prevented from being added
// again. Real, separate follow-up work, not solved here.
func (w *Watcher) shouldSkip(root, path string) bool {
	if filepath.Base(path) == ".git" {
		return true
	}
	if ignored, ok := w.ignoredDirs[root]; ok {
		return ignored[path]
	}
	return false
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	root := w.rootFor(event.Name)
	if root == "" {
		return
	}

	// New directories need to be added to the watch dynamically, per
	// ADR-019 — a static recursive add at startup misses anything created
	// afterward. Same exclusion rules as the initial walk apply here too.
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			if !w.shouldSkip(root, event.Name) {
				if err := w.fsw.Add(event.Name); err != nil {
					log.Printf("watcher: failed to add new dir %s: %v", event.Name, err)
				}
			}
		}
	}

	w.scheduleTrigger(root)
}

// scheduleTrigger implements the debounce window from ADR-019: each new
// event for a root resets its timer rather than firing immediately, so an
// actively-edited project doesn't regenerate on every save.
func (w *Watcher) scheduleTrigger(root string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if t, exists := w.pending[root]; exists {
		t.Stop()
	}

	debounce := time.Duration(w.cfg.DebounceSeconds) * time.Second
	w.pending[root] = time.AfterFunc(debounce, func() {
		w.mu.Lock()
		delete(w.pending, root)
		w.mu.Unlock()
		w.trigger(root)
	})
}

// runReconciliation re-walks every configured root, which also refreshes
// ignoredDirs for each root (addRecursive recomputes it every call) —
// real, incidental benefit: a .gitignore change mid-session is picked up
// at the next reconciliation pass, not just at startup.
func (w *Watcher) runReconciliation() {
	for _, root := range w.cfg.WatchPaths {
		if err := w.addRecursive(root); err != nil {
			log.Printf("watcher: reconciliation failed for %s: %v", root, err)
		}
	}
}

func (w *Watcher) rootFor(path string) string {
	for _, root := range w.cfg.WatchPaths {
		if rel, err := filepath.Rel(root, path); err == nil && rel != ".." {
			return root
		}
	}
	return ""
}
