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
)

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

	Title         string
	Requester     string
	ImpactPreview string
}

// SubmitPR clones req's repository at BaseBranch, locates Target's file
// under SourcePath, applies Updates, pushes the result on a new
// HeadBranch, and opens a pull/merge request -- the whole "find the
// file via the Application's source, edit the right node ... open a PR"
// path architecture.md describes, short of actually merging it (never
// done here, matching "the backend only opens PRs, never merges").
func SubmitPR(ctx context.Context, req SubmitPRRequest) (PRResult, error) {
	dir, err := os.MkdirTemp("", "ichor-prmode-*")
	if err != nil {
		return PRResult{}, fmt.Errorf("prmode: creating a checkout directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	if err := Clone(ctx, req.CloneURL, req.BaseBranch, req.Token, dir); err != nil {
		return PRResult{}, err
	}

	file, err := Locate(dir, req.SourcePath, req.Target)
	if err != nil {
		return PRResult{}, err
	}

	oldData, err := os.ReadFile(file)
	if err != nil {
		return PRResult{}, fmt.Errorf("prmode: reading %s: %w", file, err)
	}

	newData, err := Patch(oldData, req.Target, req.Updates)
	if err != nil {
		return PRResult{}, err
	}

	relPath, err := filepath.Rel(dir, file)
	if err != nil {
		return PRResult{}, fmt.Errorf("prmode: %s is not under the checkout: %w", file, err)
	}

	message := req.Title
	if message == "" {
		message = fmt.Sprintf("Update %s/%s", req.Target.Kind, req.Target.Name)
	}

	files := map[string][]byte{relPath: newData}
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
