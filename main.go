// axiom-agent daemon entrypoint.
//
// UNVERIFIED: written 2026-08-10, not compiled or tested. Real `go build`
// and `go vet` must run on i5 before this is treated as working code.
//
// Deliberately NOT included in this draft, named as real gaps rather than
// silently stubbed:
//   - Service registration (systemd user unit / LaunchAgent / Scheduled
//     Task per ADR from earlier this session) — that's an install-time
//     concern handled by the installer, not this binary.
//   - Credential loading/validation (ADR-021, amended) — runExtraction
//     below has no client_id, no axk_ key, no real API call. It proves
//     local extraction works; it does not yet upload anything anywhere.
//   - The upload endpoint itself — does not exist on the server side at
//     all yet, separate real work from what this file covers.
package main

import (
	"log"
	"os/signal"
	"syscall"

	"context"
	"strings"

	"github.com/hemant-verma-ai-ml/axiom-agent/internal/config"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/gitextract"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/watcher"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("axiom-agent: failed to load config: %v", err)
	}

	if len(cfg.WatchPaths) == 0 {
		log.Println("axiom-agent: no watch paths configured yet — daemon idle until the GUI or a config edit adds one")
	}

	w, err := watcher.New(cfg, runExtraction)
	if err != nil {
		log.Fatalf("axiom-agent: failed to start watcher: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Println("axiom-agent: daemon started")
	if err := w.Run(ctx.Done()); err != nil {
		log.Fatalf("axiom-agent: watcher exited with error: %v", err)
	}
	log.Println("axiom-agent: daemon stopped")
}

// runExtraction runs the real gitextract package against rootPath and
// logs real, verifiable counts — same "never assume it worked, read it
// back" discipline as ingestion/load_git_nodes.py's own verify_load().
//
// Real, deliberate scope of what this does NOT do yet: no client_id, no
// credential (ADR-021 amended — axk_ key, not yet loaded here), no
// upload. This proves local extraction works end-to-end on a real repo;
// it does not send anything anywhere. Wiring the upload comes after a
// real server-side endpoint exists to receive it — building that call
// now would have nowhere real to point.
//
// A failure here logs and returns rather than crashing the daemon — one
// bad watched root (not a git repo, permissions issue, whatever) must
// not take down watching for every other configured root, same
// non-fatal-per-root principle watcher.go already uses for a failed Add.
func runExtraction(rootPath string) {
	conn, err := gitextract.New(rootPath)
	if err != nil {
		log.Printf("axiom-agent: extraction failed for %s: %v", rootPath, err)
		return
	}

	commits, err := conn.GetCommitLog()
	if err != nil {
		log.Printf("axiom-agent: failed to read commit log for %s: %v", rootPath, err)
		return
	}

	churn := gitextract.GetChurnScores(commits)

	var capturedFiles, binaryFiles, truncatedFiles, missingFiles int
	for _, score := range churn {
		content, err := conn.GetFileContentAtHead(score.FilePath)
		if err != nil {
			log.Printf("axiom-agent: failed to read %s at HEAD: %v", score.FilePath, err)
			continue
		}
		if content == nil {
			missingFiles++
			continue
		}
		switch {
		case strings.HasPrefix(*content, "[BINARY FILE \u2014"):
			binaryFiles++
		default:
			capturedFiles++
		}
		if strings.Contains(*content, "[CONTENT TRUNCATED") {
			truncatedFiles++
		}
	}

	// Real, verifiable summary — same intent as verify_load()'s printed
	// counts, not just "trigger fired, trust me."
	log.Printf(
		"axiom-agent: extraction complete for %s — %d commits, %d churned files (%d content captured, %d binary, %d truncated, %d missing at HEAD). NOT UPLOADED — no server endpoint exists yet.",
		rootPath, len(commits), len(churn), capturedFiles, binaryFiles, truncatedFiles, missingFiles,
	)
}
