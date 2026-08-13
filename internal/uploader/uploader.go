// Package uploader implements the real HTTP call to POST /agent/upload
// (schema/agent_routes.py, axiom repo commit 8437311). Field names below
// are typed to match AgentUploadRequest/CommitPayload/FilePayload
// exactly -- a name mismatch here would silently produce empty JSON on
// the wire, same failure class as the Pydantic list-type bug found in
// AXIOM-S8.
//
// Bridge credential note (real, temporary, NOT ADR-017 §4's actual
// design): the axk_ key is read from AXIOM_AGENT_API_KEY at call time,
// never written to disk, never added to config.Config. ADR-017 §4 calls
// for an OS credential store (per-user install) or an encrypted config
// file (system install) -- neither is built yet (Tier 2 #8). This env
// var path unblocks a real, testable upload today without writing a
// credential to config.json in plaintext, which would be a real
// regression against a locked decision. Replace this file's credential
// loading, not its upload logic, when #8 is actually built.
package uploader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type CommitPayload struct {
	GitHash      string   `json:"git_hash"`
	Author       string   `json:"author"`
	Timestamp    string   `json:"timestamp"`
	Message      string   `json:"message"`
	FilesChanged []string `json:"files_changed"`
}

type FilePayload struct {
	Path             string  `json:"path"`
	Content          *string `json:"content"`
	ChurnCommitCount int     `json:"churn_commit_count"`
	NormalizedWeight float64 `json:"normalized_weight"`
}

type UploadRequest struct {
	ProjectName        string          `json:"project_name"`
	ProjectDescription *string         `json:"project_description,omitempty"`
	SourceLabel        string          `json:"source_label"`
	Commits            []CommitPayload `json:"commits"`
	Files              []FilePayload   `json:"files"`
}

type UploadResponse struct {
	ProjectID             int `json:"project_id"`
	CommitNodesInserted   int `json:"commit_nodes_inserted"`
	CommitNodesTotal      int `json:"commit_nodes_total"`
	CodeFileNodesInserted int `json:"code_file_nodes_inserted"`
	CodeFileNodesTotal    int `json:"code_file_nodes_total"`
}

// Upload POSTs req to serverURL + "/agent/upload" using apiKey as a
// Bearer token (axk_ key, ADR-021 amended -- get_current_client accepts
// axk_ natively, R30).
//
// req.Commits and req.Files are forced to real, non-nil slices here
// (possibly empty -- e.g. a brand-new repo with zero commits -- but
// never nil/omitted). A real full git scan always knows the true
// state, including zero, same principle as load_git_nodes.py's
// load_commits_and_files() always passing real lists to
// insert_git_nodes(), never the Optional[...]=None sentinel that means
// "no claim, leave existing alone." Sending an omitted/nil field here
// for a partial result would risk exactly the silent-wipe bug closed
// in axiom commit 8437311 (Finding 1). runExtraction must always build
// this from a complete scan, never a partial one, for this guarantee
// to hold.
func Upload(serverURL, apiKey string, req UploadRequest) (*UploadResponse, error) {
	if req.Commits == nil {
		req.Commits = []CommitPayload{}
	}
	if req.Files == nil {
		req.Files = []FilePayload{}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("uploader: failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, serverURL+"/agent/upload", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("uploader: failed to build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("uploader: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("uploader: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("uploader: server returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result UploadResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("uploader: failed to parse response: %w", err)
	}

	return &result, nil
}

// RequireEnv reads a required environment variable, returning a real
// error rather than a silent empty string -- same "fail loudly rather
// than risk misattribution" principle load_git_nodes.py's __main__
// block already uses for a missing client_id.
func RequireEnv(name string) (string, error) {
	val := os.Getenv(name)
	if val == "" {
		return "", fmt.Errorf("uploader: required environment variable %s is not set", name)
	}
	return val, nil
}
