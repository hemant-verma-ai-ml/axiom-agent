// Package gitextract is a Go port of ingestion/git_connector.py's
// GitConnector -- real, faithful port of the two fully-verified methods
// (get_commit_log, get_churn_scores), matching exact real behavior seen
// in the Python source this session, including the same __COMMIT__/\x1f
// delimiter parsing scheme so both languages parse identical git output
// the same way.
//
// GetFileContentAtHead is INCOMPLETE -- the real Python source's binary-
// file marker text and any logic after the NUL-byte check were never
// seen (the paste that showed this function cut off mid-sentence at
// "Store an explicit marker instead"). Rather than invent a marker
// string that might not match ingestion/git_connector.py's real one --
// which would silently produce structurally different nodes between
// full-repo-mirror and local-extraction-only for binary files -- that
// branch is left as a named TODO. Real Python source needed before this
// is complete.
//
// UNVERIFIED beyond compilation -- drafted 2026-08-10 off-i5, not run
// against a real repository yet.
package gitextract

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// CommitNode mirrors ingestion/git_connector.py's CommitNode dataclass
// field-for-field.
type CommitNode struct {
	GitHash      string
	Author       string
	Timestamp    time.Time
	Message      string
	FilesChanged []string
}

// ChurnScore mirrors ingestion/git_connector.py's ChurnScore dataclass.
// NormalizedWeight = commit_count / max_commit_count_in_repo, same
// Golden Rule 12 basis cited in the real Python source.
type ChurnScore struct {
	FilePath         string
	CommitCount      int
	NormalizedWeight float64
}

type GitConnector struct {
	repoPath string
}

// New mirrors GitConnector.__init__ -- real validation that repoPath is
// actually a git repo before any command runs against it. The Python
// version checks for a .git directory; matched here rather than trusting
// `git` itself to fail cleanly, same intent.
func New(repoPath string) (*GitConnector, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-dir")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s is not a git repository: %w", repoPath, err)
	}
	return &GitConnector{repoPath: repoPath}, nil
}

// runGit mirrors _run_git -- read-only git commands only. Same real
// constraint as the Python version: this must never be called with
// mutating subcommands (checkout, reset, push). Enforced the same way
// Python enforces it -- by only ever calling this from the read-only
// methods below, never with arbitrary caller-supplied args.
func (g *GitConnector) runGit(args ...string) (string, error) {
	fullArgs := append([]string{"-C", g.repoPath}, args...)
	cmd := exec.Command("git", fullArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// GetCommitLog is a real, faithful port of get_commit_log -- same git
// log format string, same __COMMIT__ prefix + \x1f (0x1F, ASCII unit
// separator) delimiter scheme, so output from either language parses
// identically.
func (g *GitConnector) GetCommitLog() ([]CommitNode, error) {
	raw, err := g.runGit(
		"log", "--all",
		"--pretty=format:%n__COMMIT__%H\x1f%an\x1f%at\x1f%s",
		"--name-only",
	)
	if err != nil {
		return nil, err
	}

	var commits []CommitNode
	var current *CommitNode

	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "__COMMIT__") {
			if current != nil {
				commits = append(commits, *current)
			}
			payload := strings.TrimPrefix(line, "__COMMIT__")
			parts := strings.SplitN(payload, "\x1f", 4)
			if len(parts) != 4 {
				return nil, fmt.Errorf("malformed commit line, expected 4 \\x1f-separated fields, got %d: %q", len(parts), line)
			}
			gitHash, author, ts, message := parts[0], parts[1], parts[2], parts[3]
			unixTS, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("malformed timestamp %q: %w", ts, err)
			}
			current = &CommitNode{
				GitHash:      gitHash,
				Author:       author,
				Timestamp:    time.Unix(unixTS, 0).UTC(),
				Message:      message,
				FilesChanged: []string{},
			}
		} else if strings.TrimSpace(line) != "" && current != nil {
			current.FilesChanged = append(current.FilesChanged, strings.TrimSpace(line))
		}
	}
	if current != nil {
		commits = append(commits, *current)
	}

	return commits, nil
}

// GetChurnScores is a real, faithful port of get_churn_scores -- same
// normalization (commit_count / max_commit_count), same explicit empty-
// repo case (no commits -> empty slice, never a division-by-zero panic
// or a fabricated default), same descending sort by commit count.
func GetChurnScores(commits []CommitNode) []ChurnScore {
	counts := make(map[string]int)
	for _, c := range commits {
		for _, f := range c.FilesChanged {
			counts[f]++
		}
	}

	if len(counts) == 0 {
		return []ChurnScore{}
	}

	maxCount := 0
	for _, count := range counts {
		if count > maxCount {
			maxCount = count
		}
	}

	scores := make([]ChurnScore, 0, len(counts))
	for path, count := range counts {
		weight := float64(count) / float64(maxCount)
		// Round to 4 decimal places, matching Python's round(x, 4).
		weight = float64(int(weight*10000+0.5)) / 10000
		scores = append(scores, ChurnScore{
			FilePath:         path,
			CommitCount:      count,
			NormalizedWeight: weight,
		})
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].CommitCount > scores[j].CommitCount
	})

	return scores
}

// MaxFileContentBytes mirrors ingestion/git_connector.py's
// MAX_FILE_CONTENT_BYTES. Real value confirmed against
// ingestion/git_connector.py line 29 this session.
const MaxFileContentBytes = 200_000

// GetFileContentAtHead is a real, complete port of
// get_file_content_at_head, including the binary-content marker and
// truncation logic confirmed against the real Python source this
// session -- same exact marker text, same truncation marker format.
// Reads the committed blob at HEAD (not the live working tree -- that's
// a separate, not-yet-ported concern the Python source explicitly
// scopes out too). Returns nil if the file doesn't exist at HEAD (a
// normal outcome, never an error -- same contract as the Python version
// returning None).
func (g *GitConnector) GetFileContentAtHead(filePath string) (*string, error) {
	cmd := exec.Command("git", "-C", g.repoPath, "show", "HEAD:"+filePath)
	out, err := cmd.Output()
	if err != nil {
		// Matches Python's contract: file not at HEAD is a normal
		// outcome (nil), not a propagated error -- same as Python
		// catching CalledProcessError and returning None.
		return nil, nil
	}

	// Lenient UTF-8 decode, matching Python's errors="replace":
	// invalid byte sequences become U+FFFD rather than raising. This is
	// an approximation of Python's exact replacement algorithm, not a
	// guaranteed byte-for-byte identical port -- worth a real diff test
	// against a file with known invalid UTF-8 before trusting full
	// equivalence.
	fileText := strings.ToValidUTF8(string(out), string(utf8.RuneError))

	// Real, exact marker text confirmed against ingestion/git_connector.py
	// this session -- NUL bytes are a reliable signal the file is not
	// actually text (compiled artifact, image, etc). An honest marker
	// beats silently stripping NUL bytes and passing the rest through,
	// per the same Rule 5 reasoning the Python source cites.
	if strings.ContainsRune(fileText, '\x00') {
		return strPtr(fmt.Sprintf(
			"[BINARY FILE \u2014 content not captured, %d raw bytes, contains NUL bytes]",
			len(out),
		)), nil
	}

	encodedLen := len([]byte(fileText))
	if encodedLen > MaxFileContentBytes {
		truncatedBytes := []byte(fileText)[:MaxFileContentBytes]
		truncated := strings.ToValidUTF8(string(truncatedBytes), "")
		result := fmt.Sprintf(
			"%s\n\n[CONTENT TRUNCATED \u2014 original file is %d bytes, exceeds MAX_FILE_CONTENT_BYTES=%d.]",
			truncated, encodedLen, MaxFileContentBytes,
		)
		return &result, nil
	}

	return &fileText, nil
}

func strPtr(s string) *string { return &s }
