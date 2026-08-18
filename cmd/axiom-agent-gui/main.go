// axiom-agent-gui: manual-trigger GUI entry point, ADR-017 §2.
//
// AXIOM-S11: Task 7.2 fully closed. Three real gaps closed this
// session:
//
//   - fyne.Do() async migration: extraction now runs on a real
//     goroutine, with every UI mutation marshalled back through
//     fyne.Do (required since Fyne 2.6 -- thread.go -- and the source
//     of the runtime warning S10's synchronous version left open). A
//     large repo no longer freezes the window during Run().
//   - Drag-and-drop (ADR-017 §2's other input path, alongside the
//     directory picker). Exactly one dropped item is accepted, and it
//     must be a real local directory -- anything else fails loudly
//     with a clear status message, never silently coerced (e.g. by
//     guessing at a dropped file's parent directory).
//   - Credential-loading UI (ADR-021 amended): a Settings dialog
//     (Server URL + API Key) backed by config.json (URL, non-secret)
//     and internal/credstore (API key, encrypted -- Tier 2 #8). Shown
//     automatically on first run via fyne.Lifecycle.SetOnStarted when
//     nothing is configured yet, and reachable anytime afterward via
//     the Settings button. The API key field is always shown blank
//     with a placeholder, never pre-filled with the existing
//     plaintext value -- leaving it blank on save keeps whatever key
//     is already stored.
//
// Shares internal/extractor.Run with cmd/axiom-agent (the daemon) --
// ADR-017 §1: "Both call into the same extraction library -- no
// duplicated extraction logic." Both the button and the drop handler
// in this file now funnel through one shared runExtraction, same
// principle applied to this file's own two trigger paths, not just to
// daemon-vs-GUI.
//
// AXIOM-S10: ADR-017 §3's default confirmed by Hemant -- every folder
// submitted here is automatically added to the daemon's persistent watch
// list (config.AddWatchPath), not just processed once. If the daemon is
// already running, it picks up the new path live via the SIGHUP reload
// mechanism in internal/watcher -- no restart needed. No opt-out UI
// exists yet (every submission auto-adds unconditionally); a "run once,
// don't watch" checkbox would be the real follow-up if that default
// ever needs overriding per-submission.
package main

import (
	"fmt"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/hemant-verma-ai-ml/axiom-agent/internal/config"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/credstore"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/extractor"
)

// credentialsReady reports whether both a server URL (config.json)
// and an API key (credstore) are currently configured. Reads both
// fresh each call rather than caching -- Settings can change either
// at any time, and staleness here would silently block or wrongly
// allow an extraction.
func credentialsReady() bool {
	cfg, err := config.Load()
	if err != nil || cfg.ServerURL == "" {
		return false
	}
	store, err := credstore.NewDefaultUserFileStore()
	if err != nil {
		return false
	}
	if _, err := store.Get("AXIOM_AGENT_API_KEY"); err != nil {
		return false
	}
	return true
}

// validateDroppedPath ensures a dropped item is usable: exactly one
// item, a local file:// URI, and a real directory on disk. Factored
// out of the SetOnDropped closure so it's unit-testable without a
// display (see gui_test.go).
func validateDroppedPath(uris []fyne.URI) (string, error) {
	if len(uris) != 1 {
		return "", fmt.Errorf("drop exactly one folder (got %d items)", len(uris))
	}
	u := uris[0]
	if u.Scheme() != "file" {
		return "", fmt.Errorf("dropped item must be a local folder (got scheme %q)", u.Scheme())
	}
	path := u.Path()
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("dropped item is not a folder: %s", path)
	}
	return path, nil
}

// showSettingsDialog opens the Server URL / API Key form. The API key
// field is always shown blank with a placeholder, never pre-filled
// with the existing plaintext value -- leaving it blank on save keeps
// whatever key is already stored. onSaved runs after a successful
// save so the caller can update status/controls.
func showSettingsDialog(w fyne.Window, status *widget.Label, onSaved func()) {
	cfg, _ := config.Load() // best-effort prefill; a load error just leaves the field blank, never blocks opening the dialog

	haveExistingKey := false
	if store, err := credstore.NewDefaultUserFileStore(); err == nil {
		if _, err := store.Get("AXIOM_AGENT_API_KEY"); err == nil {
			haveExistingKey = true
		}
	}

	serverEntry := widget.NewEntry()
	serverEntry.SetText(cfg.ServerURL)
	serverEntry.SetPlaceHolder("https://axiom.example.com")

	apiKeyEntry := widget.NewPasswordEntry()
	if haveExistingKey {
		apiKeyEntry.SetPlaceHolder("Leave blank to keep the existing key")
	} else {
		apiKeyEntry.SetPlaceHolder("Paste your axk_ API key")
	}

	items := []*widget.FormItem{
		widget.NewFormItem("Server URL", serverEntry),
		widget.NewFormItem("API Key", apiKeyEntry),
	}

	form := dialog.NewForm("Server Settings", "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		serverURL := strings.TrimSpace(serverEntry.Text)
		apiKey := apiKeyEntry.Text // deliberately not trimmed -- preserve the key exactly as pasted

		if serverURL == "" {
			status.SetText("Settings not saved: Server URL is required.")
			return
		}
		if apiKey == "" && !haveExistingKey {
			status.SetText("Settings not saved: API Key is required on first setup.")
			return
		}

		cfg, err := config.Load()
		if err != nil {
			status.SetText(fmt.Sprintf("Settings not saved: %v", err))
			return
		}
		cfg.ServerURL = serverURL
		if err := config.Save(cfg); err != nil {
			status.SetText(fmt.Sprintf("Settings not saved: %v", err))
			return
		}

		if apiKey != "" {
			store, err := credstore.NewDefaultUserFileStore()
			if err != nil {
				status.SetText(fmt.Sprintf("Server URL saved, but API key store unavailable: %v", err))
				return
			}
			if err := store.Set("AXIOM_AGENT_API_KEY", apiKey); err != nil {
				status.SetText(fmt.Sprintf("Server URL saved, but API key write failed: %v", err))
				return
			}
			// Verify by reading back -- never assume a write succeeded
			// (same discipline as cmd/migrate-creds).
			if readback, err := store.Get("AXIOM_AGENT_API_KEY"); err != nil || readback != apiKey {
				status.SetText("Server URL saved, but API key verification failed -- try again.")
				return
			}
		}

		status.SetText("Settings saved.")
		onSaved()
	}, w)
	form.Resize(fyne.NewSize(420, form.MinSize().Height))
	form.Show()
}

// runExtraction performs the real extraction+upload for path off the
// UI goroutine, marshalling every UI mutation back through fyne.Do --
// Fyne's own runtime requires this from v2.6 onward (thread.go). This
// closes the one real, named gap left from S10's synchronous version.
//
// Both trigger paths (the picker button and the drag-and-drop
// handler) call this single function -- ADR-017 §1's "no duplicated
// logic" principle applied to this file's own two entry points.
func runExtraction(path string, pickBtn, createBtn *widget.Button, pathLabel, status *widget.Label) {
	fyne.Do(func() {
		pickBtn.Disable()
		createBtn.Disable()
		pathLabel.SetText(path)
		status.SetText(fmt.Sprintf("Extracting %s ...", path))
	})

	res, err := extractor.Run(path)

	if err != nil {
		fyne.Do(func() {
			pickBtn.Enable()
			createBtn.Enable()
			status.SetText(fmt.Sprintf("Failed: %v", err))
		})
		return
	}

	msg := fmt.Sprintf(
		"Done. %d commits, %d files scanned (%d captured, %d binary, %d truncated, %d missing).",
		res.CommitCount, res.ChurnFileCount, res.CapturedFiles, res.BinaryFiles, res.TruncatedFiles, res.MissingFiles,
	)
	if res.Upload != nil {
		msg += fmt.Sprintf(
			"\nUploaded: project_id=%d, commit_nodes %d/%d, code_file_nodes %d/%d.",
			res.Upload.ProjectID, res.Upload.CommitNodesInserted, res.Upload.CommitNodesTotal,
			res.Upload.CodeFileNodesInserted, res.Upload.CodeFileNodesTotal,
		)
	}

	// AXIOM-S10, ADR-017 §3 (confirmed): auto-add this folder to the
	// daemon's persistent watch list. Real, honest limitation carried
	// forward unchanged from S10: this updates config.json regardless
	// of whether the daemon is currently running; this GUI does not
	// itself send SIGHUP (Tier 2 #7's systemd unit lives separately).
	cfg, err := config.Load()
	if err != nil {
		msg += fmt.Sprintf("\n(Could not add to watch list: %v)", err)
	} else if err := config.AddWatchPath(&cfg, path); err != nil {
		msg += fmt.Sprintf("\n(Could not add to watch list: %v)", err)
	} else {
		msg += "\nAdded to daemon watch list."
	}

	fyne.Do(func() {
		pickBtn.Enable()
		createBtn.Enable()
		status.SetText(msg)
	})
}

func main() {
	// This app genuinely wraps every UI mutation from spawned goroutines
	// in fyne.Do (runExtraction) -- declaring the migration explicitly
	// via metadata is what actually silences Fyne's own warning (it is
	// a declared-metadata check, not a runtime correctness detector --
	// confirmed against internal/build/build.go and app/meta.go).
	app.SetMetadata(fyne.AppMetadata{Migrations: map[string]bool{"fyneDo": true}})
	a := app.NewWithID("com.ruvelta.axiom-agent-gui")
	w := a.NewWindow("AXIOM Agent")
	w.Resize(fyne.NewSize(480, 260))

	status := widget.NewLabel("Select a project folder to create a document.")
	status.Wrapping = fyne.TextWrapWord

	var selectedPath string
	pathLabel := widget.NewLabel("No folder selected.")

	var pickBtn, createBtn *widget.Button

	pickBtn = widget.NewButton("Select Directory", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				status.SetText(fmt.Sprintf("Error selecting folder: %v", err))
				return
			}
			if uri == nil {
				return // user cancelled
			}
			selectedPath = uri.Path()
			pathLabel.SetText(selectedPath)
			status.SetText("Ready.")
		}, w)
	})

	createBtn = widget.NewButton("Create Doc", func() {
		if selectedPath == "" {
			status.SetText("Select a folder first.")
			return
		}
		if !credentialsReady() {
			status.SetText("Set up your server URL and API key first (Settings).")
			return
		}
		go runExtraction(selectedPath, pickBtn, createBtn, pathLabel, status)
	})

	settingsBtn := widget.NewButton("Settings", func() {
		showSettingsDialog(w, status, func() {
			status.SetText("Settings saved. Ready.")
		})
	})

	// Drag-and-drop (ADR-017 §2's other input path). Validation lives
	// in validateDroppedPath so it's real, unit-testable logic, not
	// inline closure code only exercisable via a live display.
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		if !credentialsReady() {
			status.SetText("Set up your server URL and API key first (Settings).")
			return
		}
		path, err := validateDroppedPath(uris)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		selectedPath = path
		go runExtraction(selectedPath, pickBtn, createBtn, pathLabel, status)
	})

	if !credentialsReady() {
		status.SetText("Welcome. Set up your server URL and API key to get started.")
	}

	w.SetContent(container.NewVBox(
		pickBtn,
		pathLabel,
		createBtn,
		settingsBtn,
		status,
	))

	// First-run credential setup: fyne.Lifecycle's SetOnStarted is the
	// framework's own supported hook for "run this once the app's
	// event loop is actually live" -- confirmed against the real
	// glfw driver source (runGL() calls this synchronously, on the
	// main/UI goroutine, right after the loop starts) rather than
	// assumed. Because this callback already runs on the UI thread,
	// showSettingsDialog is called directly here, with no fyne.Do
	// wrapper needed -- that's only required for code originating
	// from a goroutine the developer spawns themselves.
	if !credentialsReady() {
		a.Lifecycle().SetOnStarted(func() {
			showSettingsDialog(w, status, func() {
				status.SetText("Settings saved. Ready.")
			})
		})
	}

	w.ShowAndRun()
}
