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
	"path/filepath"
	"strings"
	"time"

	"github.com/hemant-verma-ai-ml/axiom-agent/internal/config"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/gitextract"
	"github.com/hemant-verma-ai-ml/axiom-agent/internal/uploader"
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

// runExtraction runs the real gitextract package against rootPath, then
// uploads the result via internal/uploader.Upload — same "never assume
// it worked, read it back" discipline as ingestion/load_git_nodes.py's
// own verify_load(), now closing the loop server-side too (real
// commit_nodes_total/code_file_nodes_total in the response, not just a
// this-call insert count -- axiom commit 8437311).
//
// commits/files sent to Upload are always real, complete slices from a
// full scan of rootPath -- never a partial result. This matters: the
// upload request's Optional[...]=None sentinel on the server side means
// "no claim, leave existing nodes alone" (Finding 1, axiom commit
// 8437311); a partial extraction sent as if complete would silently
// look like a legitimate zero-count assertion instead of an error.
//
// AXIOM_AGENT_SERVER_URL and AXIOM_AGENT_API_KEY are required
// environment variables (see uploader.RequireEnv) -- a real, temporary
// bridge, not ADR-017 §4's actual credential storage. Missing either
// logs and returns rather than crashing the daemon, same non-fatal
// principle as every other failure mode here.
//
// A failure here logs and returns rather than crashing the daemon — one
// bad watched root (not a git repo, permissions issue, credential
// missing, upload failed, whatever) must not take down watching for
// every other configured root, same non-fatal-per-root principle
// watcher.go already uses for a failed Add.
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
	filePayloads := make([]uploader.FilePayload, 0, len(churn))
	for _, score := range churn {
		content, err := conn.GetFileContentAtHead(score.FilePath)
		if err != nil {
			log.Printf("axiom-agent: failed to read %s at HEAD: %v", score.FilePath, err)
			continue
		}
		if content == nil {
			missingFiles++
			filePayloads = append(filePayloads, uploader.FilePayload{
				Path:             score.FilePath,
				Content:          nil,
				ChurnCommitCount: score.CommitCount,
				NormalizedWeight: score.NormalizedWeight,
			})
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
		filePayloads = append(filePayloads, uploader.FilePayload{
			Path:             score.FilePath,
			Content:          content,
			ChurnCommitCount: score.CommitCount,
			NormalizedWeight: score.NormalizedWeight,
		})
	}

	// Real, verifiable summary — same intent as verify_load()'s printed
	// counts, not just "trigger fired, trust me."
	log.Printf(
		"axiom-agent: extraction complete for %s — %d commits, %d churned files (%d content captured, %d binary, %d truncated, %d missing at HEAD).",
		rootPath, len(commits), len(churn), capturedFiles, binaryFiles, truncatedFiles, missingFiles,
	)

	serverURL, err := uploader.RequireEnv("AXIOM_AGENT_SERVER_URL")
	if err != nil {
		log.Printf("axiom-agent: upload skipped for %s: %v", rootPath, err)
		return
	}
	apiKey, err := uploader.RequireEnv("AXIOM_AGENT_API_KEY")
	if err != nil {
		log.Printf("axiom-agent: upload skipped for %s: %v", rootPath, err)
		return
	}

	commitPayloads := make([]uploader.CommitPayload, 0, len(commits))
	for _, c := range commits {
		commitPayloads = append(commitPayloads, uploader.CommitPayload{
			GitHash:      c.GitHash,
			Author:       c.Author,
			Timestamp:    c.Timestamp.Format(time.RFC3339),
			Message:      c.Message,
			FilesChanged: c.FilesChanged,
		})
	}

	uploadReq := uploader.UploadRequest{
		ProjectName: filepath.Base(rootPath),
		SourceLabel: rootPath,
		Commits:     commitPayloads,
		Files:       filePayloads,
	}

	result, err := uploader.Upload(serverURL, apiKey, uploadReq)
	if err != nil {
		log.Printf("axiom-agent: upload FAILED for %s: %v", rootPath, err)
		return
	}

	log.Printf(
		"axiom-agent: uploaded %s — project_id=%d, commit_nodes %d/%d inserted/total, code_file_nodes %d/%d inserted/total",
		rootPath, result.ProjectID, result.CommitNodesInserted, result.CommitNodesTotal, result.CodeFileNodesInserted, result.CodeFileNodesTotal,
	)
}
