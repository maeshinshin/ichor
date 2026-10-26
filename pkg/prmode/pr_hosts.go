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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// titleKey is the one request-body field name every host spells the
// same way.
const titleKey = "title"

// defaultGitHubAPI is GitHub's own public API root, used when
// req.APIBaseURL is empty (GitHub Enterprise Server deployments set
// their own).
const defaultGitHubAPI = "https://api.github.com"

// postJSON POSTs body (marshaled as JSON) to urlStr with the given
// headers plus Content-Type/Accept: application/json, decodes the
// response JSON into result, and returns an error naming the status
// code (never the headers, so a bad token is never echoed into an
// error message) if the host did not report success.
func postJSON(ctx context.Context, urlStr string, headers map[string]string, body, result any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("prmode: encoding request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("prmode: building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("prmode: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("prmode: reading response from %s: %w", req.URL.Path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("prmode: %s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, respBody)
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("prmode: decoding response from %s: %w", req.URL.Path, err)
	}

	return nil
}

// errNotFound is getJSON's answer for a 404, which only the Gitea lookup
// reads as "no such pull request".
var errNotFound = errors.New("prmode: not found")

// getJSON GETs urlStr with headers and decodes the JSON response into
// result. Like postJSON, its errors name the status and never the headers.
func getJSON(ctx context.Context, urlStr string, headers map[string]string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return fmt.Errorf("prmode: building request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("prmode: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("prmode: reading response from %s: %w", req.URL.Path, err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("prmode: %s %s: status %d: %s", req.Method, req.URL.Path, resp.StatusCode, respBody)
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("prmode: decoding response from %s: %w", req.URL.Path, err)
	}

	return nil
}

// findGitHubPR implements "List pull requests"
// (docs.github.com/en/rest/pulls/pulls#list-pull-requests, verified
// 2026-10-26): GET /repos/{owner}/{repo}/pulls?state=open&head=
// {owner}:{branch}&base={base} -> [{html_url, state}]. head is
// "user:ref-name" and is required in that form to filter.
func findGitHubPR(ctx context.Context, req PRRequest) (PRResult, bool, error) {
	base := req.APIBaseURL
	if base == "" {
		base = defaultGitHubAPI
	}

	q := url.Values{"state": {"open"}, "head": {req.Owner + ":" + req.HeadBranch}, "base": {req.BaseBranch}}
	urlStr := base + "/repos/" + url.PathEscape(req.Owner) + "/" + url.PathEscape(req.Repo) + "/pulls?" + q.Encode()

	headers := map[string]string{
		"Authorization":        "Bearer " + req.Token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}

	var list []struct {
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}

	if err := getJSON(ctx, urlStr, headers, &list); err != nil {
		return PRResult{}, false, err
	}

	for _, pr := range list {
		if pr.State == "open" && pr.HTMLURL != "" {
			return PRResult{URL: pr.HTMLURL}, true, nil
		}
	}

	return PRResult{}, false, nil
}

// findGitLabPR implements "List project merge requests"
// (docs.gitlab.com/api/merge_requests, verified 2026-10-26): GET
// /api/v4/projects/:id/merge_requests?state=opened&source_branch=
// &target_branch= -> [{web_url, state}].
func findGitLabPR(ctx context.Context, req PRRequest) (PRResult, bool, error) {
	if req.APIBaseURL == "" {
		return PRResult{}, false, fmt.Errorf("prmode: GitLab requires --api-base-url (no public default)")
	}

	q := url.Values{"state": {"opened"}, "source_branch": {req.HeadBranch}, "target_branch": {req.BaseBranch}}
	urlStr := req.APIBaseURL + "/api/v4/projects/" + url.PathEscape(req.Owner+"/"+req.Repo) + "/merge_requests?" + q.Encode()

	var list []struct {
		WebURL string `json:"web_url"`
		State  string `json:"state"`
	}

	if err := getJSON(ctx, urlStr, map[string]string{"PRIVATE-TOKEN": req.Token}, &list); err != nil {
		return PRResult{}, false, err
	}

	for _, mr := range list {
		if mr.State == "opened" && mr.WebURL != "" {
			return PRResult{URL: mr.WebURL}, true, nil
		}
	}

	return PRResult{}, false, nil
}

// findGiteaPR implements "Get a pull request by base and head"
// (docs.gitea.com/api/1.22/operations/repo-get-pull-request-by-base-head,
// verified 2026-10-26): GET /api/v1/repos/{owner}/{repo}/pulls/{base}/
// {head} -> PullRequest, or 404. The list endpoint has no head filter, so
// this is the lookup; a PR that is not "open" (the docs do not say which
// one is returned when several share the branches) counts as none.
func findGiteaPR(ctx context.Context, req PRRequest) (PRResult, bool, error) {
	if req.APIBaseURL == "" {
		return PRResult{}, false, fmt.Errorf("prmode: Gitea/Forgejo requires --api-base-url (no public default)")
	}

	urlStr := req.APIBaseURL + "/api/v1/repos/" + url.PathEscape(req.Owner) + "/" + url.PathEscape(req.Repo) +
		"/pulls/" + escapeRef(req.BaseBranch) + "/" + escapeRef(req.HeadBranch)

	var pr struct {
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}

	err := getJSON(ctx, urlStr, map[string]string{"Authorization": "token " + req.Token}, &pr)
	if errors.Is(err, errNotFound) {
		return PRResult{}, false, nil
	}

	if err != nil {
		return PRResult{}, false, err
	}

	if pr.State != "open" || pr.HTMLURL == "" {
		return PRResult{}, false, nil
	}

	return PRResult{URL: pr.HTMLURL}, true, nil
}

// escapeRef escapes each "/"-separated segment of a branch name but keeps
// the slashes, as Gitea's wildcard route expects.
func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}

	return strings.Join(parts, "/")
}

// openGitHubPR implements "Create a pull request"
// (docs.github.com/en/rest/pulls/pulls, verified 2026-10-26):
// POST /repos/{owner}/{repo}/pulls, Authorization: Bearer <token>,
// {title, head, base, body} -> .html_url.
func openGitHubPR(ctx context.Context, req PRRequest) (PRResult, error) {
	base := req.APIBaseURL
	if base == "" {
		base = defaultGitHubAPI
	}

	urlStr := base + "/repos/" + url.PathEscape(req.Owner) + "/" + url.PathEscape(req.Repo) + "/pulls"

	headers := map[string]string{
		"Authorization":        "Bearer " + req.Token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}

	body := map[string]string{
		titleKey: req.Title, "head": req.HeadBranch, "base": req.BaseBranch, "body": req.Body,
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}

	if err := postJSON(ctx, urlStr, headers, body, &result); err != nil {
		return PRResult{}, err
	}

	return PRResult{URL: result.HTMLURL}, nil
}

// openGitLabPR implements "Create MR" (docs.gitlab.com/api/merge_requests,
// verified 2026-10-26): POST /api/v4/projects/:id/merge_requests,
// PRIVATE-TOKEN: <token>, {source_branch, target_branch, title,
// description} -> .web_url. :id is the URL-encoded "owner/repo" path.
func openGitLabPR(ctx context.Context, req PRRequest) (PRResult, error) {
	if req.APIBaseURL == "" {
		return PRResult{}, fmt.Errorf("prmode: GitLab requires --api-base-url (no public default)")
	}

	project := url.PathEscape(req.Owner + "/" + req.Repo)
	urlStr := req.APIBaseURL + "/api/v4/projects/" + project + "/merge_requests"

	headers := map[string]string{"PRIVATE-TOKEN": req.Token}

	body := map[string]string{
		"source_branch": req.HeadBranch, "target_branch": req.BaseBranch, titleKey: req.Title, "description": req.Body,
	}

	var result struct {
		WebURL string `json:"web_url"`
	}

	if err := postJSON(ctx, urlStr, headers, body, &result); err != nil {
		return PRResult{}, err
	}

	return PRResult{URL: result.WebURL}, nil
}

// openGiteaPR implements "Create a pull request"
// (docs.gitea.com/api/operations/repo-create-pull-request, verified
// 2026-10-26; Forgejo is API-compatible): POST /api/v1/repos/{owner}/
// {repo}/pulls, Authorization: token <token>, {head, base, title, body}
// -> .html_url.
func openGiteaPR(ctx context.Context, req PRRequest) (PRResult, error) {
	if req.APIBaseURL == "" {
		return PRResult{}, fmt.Errorf("prmode: Gitea/Forgejo requires --api-base-url (no public default)")
	}

	urlStr := req.APIBaseURL + "/api/v1/repos/" + url.PathEscape(req.Owner) + "/" + url.PathEscape(req.Repo) + "/pulls"

	headers := map[string]string{"Authorization": "token " + req.Token}

	body := map[string]string{
		"head": req.HeadBranch, "base": req.BaseBranch, titleKey: req.Title, "body": req.Body,
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}

	if err := postJSON(ctx, urlStr, headers, body, &result); err != nil {
		return PRResult{}, err
	}

	return PRResult{URL: result.HTMLURL}, nil
}
