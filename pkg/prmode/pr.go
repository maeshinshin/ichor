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
	"strings"
)

// Host identifies which git hosting API OpenPullRequest calls.
// architecture.md: "GitHub, GitLab and Gitea/Forgejo are supported" --
// Forgejo is Gitea's own API-compatible fork, so GitHost ends there too.
type Host int

const (
	HostGitHub Host = iota
	HostGitLab
	HostGitea
)

// PRRequest is everything OpenPullRequest needs to open one PR/MR. It
// never carries enough to merge one -- there is no such field, matching
// architecture.md's "the backend only opens PRs, never merges".
type PRRequest struct {
	// APIBaseURL is the host's API root, e.g. "https://api.github.com",
	// "https://gitlab.example.com", "https://gitea.example.com" (self-
	// hosted GitLab/Gitea; GitHub Enterprise Server would set this too).
	APIBaseURL string

	Owner, Repo string
	Token       string

	BaseBranch, HeadBranch string

	Title string
	Body  string
}

// PRResult is what a caller gets back: just enough to show or log, never
// anything about merge state (this package never queries or changes
// that).
type PRResult struct {
	URL string
}

// OpenPullRequest opens one pull/merge request via host's REST API --
// never git push options (and GitLab's own push-option path reports the created MR's URL
// only as human-readable push output, not a structured value, which is
// worse to parse reliably than the one REST call this needs anyway for
// the other two hosts).
func OpenPullRequest(ctx context.Context, host Host, req PRRequest) (PRResult, error) {
	switch host {
	case HostGitHub:
		return openGitHubPR(ctx, req)
	case HostGitLab:
		return openGitLabPR(ctx, req)
	case HostGitea:
		return openGiteaPR(ctx, req)
	default:
		return PRResult{}, fmt.Errorf("prmode: unknown host %v", host)
	}
}

// FindOpenPullRequest reports the open pull/merge request whose head is
// req.HeadBranch and whose base is req.BaseBranch, if any. It is how a
// repeated request for the same change finds the PR the first one opened
// instead of opening another. Merged and closed ones do
// not count.
func FindOpenPullRequest(ctx context.Context, host Host, req PRRequest) (PRResult, bool, error) {
	switch host {
	case HostGitHub:
		return findGitHubPR(ctx, req)
	case HostGitLab:
		return findGitLabPR(ctx, req)
	case HostGitea:
		return findGiteaPR(ctx, req)
	default:
		return PRResult{}, false, fmt.Errorf("prmode: unknown host %v", host)
	}
}

// buildPRBody is the PR description architecture.md asks for: "a PR
// whose description includes the impact preview (affected nodes, reboot
// required or not) and who asked". impactPreview is an opaque string
// slot, never computed here -- that backend does not exist yet -- included verbatim when non-empty so a later unit can
// supply it with no change to this function's signature. requester is
// AuditRecord.actor's value, today always a fixed string (OIDC identity
// is phase 9); diff is Diff's or Patch's own before/after rendering.
func buildPRBody(requester, diff, impactPreview string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Requested by: %s\n", requester)

	if impactPreview != "" {
		b.WriteString("\n## Impact\n\n")
		b.WriteString(impactPreview)
		b.WriteString("\n")
	}

	b.WriteString("\n## Diff\n\n```diff\n")
	b.WriteString(diff)
	b.WriteString("```\n")

	return b.String()
}
