// axiom-agent-gui: manual-trigger GUI entry point, ADR-017 SS2.
//
// Real, deliberate scope of this first version: directory picker + a
// "Create Doc" button, one-shot run against whatever folder is chosen.
// ADR-017 SS2 specifies "drag-and-drop OR select directory" -- this
// implements the directory-picker half first, proven working, before
// adding drag-and-drop's own real complexity on top.
//
// Shares internal/extractor.Run with cmd/axiom-agent (the daemon) --
// ADR-017 SS1: "no duplicated extraction logic." This file only builds
// UI around the same Run() call the daemon's watcher closure calls.
//
// AXIOM-S10: ADR-017 SS3's default confirmed by Hemant -- every folder
// submitted here is automatically added to the daemon's persistent watch
// list (config.AddWatchPath), not just processed once. If the daemon is
// already running, it picks up the new path live via the SIGHUP reload
// mechanism added this session in internal/watcher -- no restart needed.
// No opt-out UI exists yet (every submission auto-adds unconditionally);
// a "run once, don't watch" checkbox would be the real follow-up if that
// default ever needs overriding per-submission.
//
// NOT YET implemented, named as real gaps rather than silently stubbed:
//   - Drag-and-drop (ADR-017 SS2's other input path)
//   - Credential loading UI (ADR-021 amended) -- this version still
//     relies on the same AXIOM_AGENT_API_KEY / AXIOM_AGENT_SERVER_URL
//     env-var bridge as the daemon, not a real first-run "paste your
//     key" flow. That's real, separate scope, not assumed solved here.
package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/hemant-verma-ai-ml/axiom-agent/internal/config"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/extractor"
)

func main() {
	a := app.NewWithID("com.ruvelta.axiom-agent-gui")
	w := a.NewWindow("AXIOM Agent")
	w.Resize(fyne.NewSize(480, 240))

	status := widget.NewLabel("Select a project folder to create a document.")
	status.Wrapping = fyne.TextWrapWord

	var selectedPath string
	pathLabel := widget.NewLabel("No folder selected.")

	pickBtn := widget.NewButton("Select Directory", func() {
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

	var createBtn *widget.Button
	createBtn = widget.NewButton("Create Doc", func() {
		if selectedPath == "" {
			status.SetText("Select a folder first.")
			return
		}
		createBtn.Disable()
		status.SetText(fmt.Sprintf("Extracting %s ...", selectedPath))

		// Real, deliberate: run synchronously on this first version.
		// Fyne callbacks run on the UI goroutine; a genuinely large repo
		// would freeze the window during Run(). Named here rather than
		// silently accepted -- worth moving to a goroutine + fyne.Do
		// (or equivalent) once this synchronous version is proven
		// correct, not before.
		res, err := extractor.Run(selectedPath)
		createBtn.Enable()
		if err != nil {
			status.SetText(fmt.Sprintf("Failed: %v", err))
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

		// AXIOM-S10, ADR-017 SS3 (confirmed): auto-add this folder to the
		// daemon's persistent watch list. Real, honest limitation: this
		// updates config.json regardless of whether the daemon is
		// currently running. If it is running, SIGHUP (sent separately,
		// e.g. by an installer/tray action -- not from inside this GUI
		// process) makes it pick this up live; this GUI does not send
		// that signal itself, since it may be running on a machine where
		// the daemon isn't installed as a service at all yet (Tier 2 #7,
		// still open).
		cfg, err := config.Load()
		if err != nil {
			msg += fmt.Sprintf("\n(Could not add to watch list: %v)", err)
		} else if err := config.AddWatchPath(&cfg, selectedPath); err != nil {
			msg += fmt.Sprintf("\n(Could not add to watch list: %v)", err)
		} else {
			msg += "\nAdded to daemon watch list."
		}

		status.SetText(msg)
	})

	w.SetContent(container.NewVBox(
		pickBtn,
		pathLabel,
		createBtn,
		status,
	))

	w.ShowAndRun()
}
