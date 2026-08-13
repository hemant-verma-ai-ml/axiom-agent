// axiom-agent daemon entrypoint.
//
// UNVERIFIED: written 2026-08-10, not compiled or tested. Real `go build`
// and `go vet` must run on i5 before this is treated as working code.
//
// Deliberately NOT included in this file, named as a real gap rather than
// silently stubbed:
//   - Service registration (systemd user unit / LaunchAgent / Scheduled
//     Task per ADR from earlier this session) — that's an install-time
//     concern handled by the installer, not this binary.
//
// AXIOM-S9 update: runExtraction now calls the real, live
// POST /agent/upload endpoint (axiom repo commit 8437311) via
// internal/uploader. Credential loading is a real, temporary BRIDGE, not
// ADR-017 §4's actual design: the axk_ key comes from AXIOM_AGENT_API_KEY
// at call time, never written to disk. ADR-017 §4's real OS-keychain /
// encrypted-config storage (Tier 2 #8) is still unbuilt — replace the
// credential-loading call in runExtraction, not the upload logic itself,
// when #8 lands.
package main

import (
	"log"
	"os/signal"
	"syscall"

	"context"

	"github.com/hemant-verma-ai-ml/axiom-agent/internal/config"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/extractor"
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

	w, err := watcher.New(cfg, func(rootPath string) {
		res, err := extractor.Run(rootPath)
		if err != nil {
			log.Printf("axiom-agent: %v", err)
			return
		}
		log.Printf(
			"axiom-agent: extraction complete for %s — %d commits, %d churned files (%d content captured, %d binary, %d truncated, %d missing at HEAD).",
			res.RootPath, res.CommitCount, res.ChurnFileCount, res.CapturedFiles, res.BinaryFiles, res.TruncatedFiles, res.MissingFiles,
		)
		if res.Upload != nil {
			log.Printf(
				"axiom-agent: uploaded %s — project_id=%d, commit_nodes %d/%d inserted/total, code_file_nodes %d/%d inserted/total",
				res.RootPath, res.Upload.ProjectID, res.Upload.CommitNodesInserted, res.Upload.CommitNodesTotal, res.Upload.CodeFileNodesInserted, res.Upload.CodeFileNodesTotal,
			)
		}
	})
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
