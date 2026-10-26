/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package prmode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// patchedFileContent is the edit-in-place half of SubmitPR: find Target's
// file, apply Updates, and return its path relative to dir with the
// before/after bytes.
func patchedFileContent(dir string, req SubmitPRRequest) (relPath string, oldData, newData []byte, err error) {
	file, err := Locate(dir, req.SourcePath, req.Target)
	if err != nil {
		return "", nil, nil, err
	}

	oldData, err = os.ReadFile(file)
	if err != nil {
		return "", nil, nil, fmt.Errorf("prmode: reading %s: %w", file, err)
	}

	newData, err = Patch(oldData, req.Target, req.Updates)
	if err != nil {
		return "", nil, nil, err
	}

	relPath, err = filepath.Rel(dir, file)
	if err != nil {
		return "", nil, nil, fmt.Errorf("prmode: %s is not under the checkout: %w", file, err)
	}

	return relPath, oldData, newData, nil
}

// removedFileContent is the delete half: find Target's file and remove just
// Target's document. newData is nil when the file held nothing else, which
// CommitAndPush reads as "remove the file".
func removedFileContent(dir string, req SubmitPRRequest) (relPath string, oldData, newData []byte, err error) {
	file, err := Locate(dir, req.SourcePath, req.Target)
	if err != nil {
		return "", nil, nil, err
	}

	oldData, err = os.ReadFile(file)
	if err != nil {
		return "", nil, nil, fmt.Errorf("prmode: reading %s: %w", file, err)
	}

	newData, remaining, err := RemoveDocument(oldData, req.Target)
	if err != nil {
		return "", nil, nil, err
	}

	if !remaining {
		newData = nil
	}

	relPath, err = filepath.Rel(dir, file)
	if err != nil {
		return "", nil, nil, fmt.Errorf("prmode: %s is not under the checkout: %w", file, err)
	}

	return relPath, oldData, newData, nil
}

// newFileContent is the add-a-file half: check that nf.Path stays inside
// dir and names a file that does not exist yet, and create its directory.
func newFileContent(dir string, nf *NewFile) (relPath string, newData []byte, err error) {
	clean := filepath.Clean(nf.Path)
	sep := string(filepath.Separator)

	if nf.Path == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+sep) ||
		clean == ".git" || strings.HasPrefix(clean, ".git"+sep) {
		return "", nil, fmt.Errorf("prmode: new file path %q must be a relative path inside the repository", nf.Path)
	}

	full := filepath.Join(dir, clean)
	if _, statErr := os.Stat(full); statErr == nil {
		return "", nil, fmt.Errorf("prmode: %s already exists in the repository", clean)
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", nil, fmt.Errorf("prmode: creating the directory for %s: %w", clean, err)
	}

	return clean, nf.Content, nil
}

// SubmitPRRequest is everything SubmitPR needs to go from "a tracked CR
// and a new value for one of its fields" to an open pull/merge request.
// It only ever applies to SourceFile Applications (ClassifySource):
// SourceHelm/SourcePlugin have no single file to locate or edit, so
// there is nothing for SubmitPR to do for them -- the caller falls back
// to Diff for those instead (architecture.md's "diff to apply by hand").
type SubmitPRRequest struct {
	Host       Host
	APIBaseURL string

	Owner, Repo string
	CloneURL    string
	Token       string

	// BaseBranch is the Application source's own targetRevision; the CR
	// is read from, and the PR targets, this branch.
	BaseBranch string
	// HeadBranch is the new branch SubmitPR pushes; the caller names it
	// (e.g. including the CR's own name, so two concurrent edits to
	// different CRs never collide).
	HeadBranch string

	// SourcePath is the Application source's own path, passed to
	// Locate unchanged.
	SourcePath string
	Target     Target
	Updates    []FieldUpdate

	// NewFile, when set, adds a file instead of editing one: SourcePath,
	// Target and Updates are ignored. This is strict-mode GitOps, where the CR is not in Git yet, so Locate has nothing
	// to find.
	NewFile *NewFile

	// Delete removes Target: its whole file when it is the only document,
	// otherwise just its document, every other byte kept.
	// Updates and NewFile are ignored.
	Delete bool

	Title         string
	Requester     string
	ImpactPreview string
}

// NewFile is a file SubmitPR adds: Path is relative to the repository
// root and must not exist yet.
type NewFile struct {
	Path    string
	Content []byte
}

// SubmitPR clones req's repository at BaseBranch, locates Target's file
// under SourcePath, applies Updates, pushes the result on a new
// HeadBranch, and opens a pull/merge request -- the whole "find the
// file via the Application's source, edit the right node ... open a PR"
// path architecture.md describes, short of actually merging it (never
// done here, matching "the backend only opens PRs, never merges").
func SubmitPR(ctx context.Context, req SubmitPRRequest) (PRResult, error) {
	// A repeated request finds the PR the first one opened. Asked before
	// cloning, so a duplicate costs one API call.
	probe := PRRequest{
		APIBaseURL: req.APIBaseURL, Owner: req.Owner, Repo: req.Repo, Token: req.Token,
		BaseBranch: req.BaseBranch, HeadBranch: req.HeadBranch,
	}

	if existing, found, err := FindOpenPullRequest(ctx, req.Host, probe); err != nil {
		return PRResult{}, err
	} else if found {
		return existing, nil
	}

	dir, err := os.MkdirTemp("", "ichor-prmode-*")
	if err != nil {
		return PRResult{}, fmt.Errorf("prmode: creating a checkout directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	if err := Clone(ctx, req.CloneURL, req.BaseBranch, req.Token, dir); err != nil {
		return PRResult{}, err
	}

	var (
		relPath          string
		oldData, newData []byte
	)

	switch {
	case req.NewFile != nil:
		relPath, newData, err = newFileContent(dir, req.NewFile)
	case req.Delete:
		relPath, oldData, newData, err = removedFileContent(dir, req)
	default:
		relPath, oldData, newData, err = patchedFileContent(dir, req)
	}

	if err != nil {
		return PRResult{}, err
	}

	message := req.Title
	if message == "" {
		message = fmt.Sprintf("Update %s/%s", req.Target.Kind, req.Target.Name)
	}

	files := map[string][]byte{relPath: newData} // nil newData removes the file
	if err := CommitAndPush(ctx, dir, req.HeadBranch, req.Token, message, files); err != nil {
		return PRResult{}, err
	}

	diff, err := Diff(string(oldData), string(newData), relPath)
	if err != nil {
		return PRResult{}, err
	}

	prReq := PRRequest{
		APIBaseURL: req.APIBaseURL,
		Owner:      req.Owner, Repo: req.Repo, Token: req.Token,
		BaseBranch: req.BaseBranch, HeadBranch: req.HeadBranch,
		Title: req.Title, Body: buildPRBody(req.Requester, diff, req.ImpactPreview),
	}

	return OpenPullRequest(ctx, req.Host, prReq)
}
