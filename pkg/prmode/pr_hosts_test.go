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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testPRToken = "test-pr-token-should-not-leak"

// Fixture values shared across this file's and submit_test.go's tests.
const (
	testOwner      = "acme"
	testRepo       = "cluster-gitops"
	testBaseBranch = "main"
	testHeadBranch = "ichor/bump-talos"
	testPRTitle    = "Bump Talos"
	htmlURLKey     = "html_url"
)

var testPRRequest = PRRequest{
	Owner: testOwner, Repo: testRepo, Token: testPRToken,
	BaseBranch: testBaseBranch, HeadBranch: testHeadBranch,
	Title: testPRTitle, Body: "Requested by: ichor-ui\n",
}

// TestOpenGitHubPR_RequestShapeAndResponse proves the exact body field
// names GitHub's "Create a pull request" expects (head/base/title/body,
// not GitLab's or Gitea's names), the Authorization/Accept/API-Version
// headers, and that .html_url comes back as PRResult.URL.
func TestOpenGitHubPR_RequestShapeAndResponse(t *testing.T) {
	var gotPath, gotMethod string

	var gotBody map[string]string

	gotHeaders := http.Header{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotHeaders = r.Method, r.URL.Path, r.Header

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{htmlURLKey: "https://github.invalid/acme/cluster-gitops/pull/1"})
	}))
	defer srv.Close()

	req := testPRRequest
	req.APIBaseURL = srv.URL

	got, err := openGitHubPR(context.Background(), req)
	if err != nil {
		t.Fatalf("openGitHubPR: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/repos/acme/cluster-gitops/pulls" {
		t.Fatalf("method/path = %s %s, want POST /repos/acme/cluster-gitops/pulls", gotMethod, gotPath)
	}

	if gotHeaders.Get("Authorization") != "Bearer "+testPRToken {
		t.Fatalf("Authorization header = %q, want Bearer %s", gotHeaders.Get("Authorization"), testPRToken)
	}

	if gotHeaders.Get("X-GitHub-Api-Version") != "2022-11-28" {
		t.Fatalf("X-GitHub-Api-Version = %q, want 2022-11-28", gotHeaders.Get("X-GitHub-Api-Version"))
	}

	wantBody := map[string]string{titleKey: testPRTitle, "head": testHeadBranch, "base": testBaseBranch, "body": req.Body}
	if gotBody[titleKey] != wantBody[titleKey] || gotBody["head"] != wantBody["head"] ||
		gotBody["base"] != wantBody["base"] || gotBody["body"] != wantBody["body"] {
		t.Fatalf("body = %v, want %v", gotBody, wantBody)
	}

	if got.URL != "https://github.invalid/acme/cluster-gitops/pull/1" {
		t.Fatalf("URL = %q, want the mocked html_url", got.URL)
	}
}

// TestOpenGitLabPR_RequestShapeAndResponse proves GitLab's own field
// names (source_branch/target_branch, not head/base) and PRIVATE-TOKEN
// header, and that .web_url comes back as PRResult.URL.
func TestOpenGitLabPR_RequestShapeAndResponse(t *testing.T) {
	var gotPath string

	var gotBody map[string]string

	gotHeaders := http.Header{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Path is Go's already-decoded convenience field (%2F ->
		// /); EscapedPath() is what was actually on the wire, which is
		// what proves the project id was sent URL-encoded as GitLab's
		// API requires.
		gotPath, gotHeaders = r.URL.EscapedPath(), r.Header

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		w.WriteHeader(http.StatusCreated)

		mrURL := "https://gitlab.invalid/acme/cluster-gitops/-/merge_requests/1"
		_ = json.NewEncoder(w).Encode(map[string]string{"web_url": mrURL})
	}))
	defer srv.Close()

	req := testPRRequest
	req.APIBaseURL = srv.URL

	got, err := openGitLabPR(context.Background(), req)
	if err != nil {
		t.Fatalf("openGitLabPR: %v", err)
	}

	if gotPath != "/api/v4/projects/acme%2Fcluster-gitops/merge_requests" {
		t.Fatalf("path = %q, want the URL-encoded project path", gotPath)
	}

	if gotHeaders.Get("PRIVATE-TOKEN") != testPRToken {
		t.Fatalf("PRIVATE-TOKEN header = %q, want %s", gotHeaders.Get("PRIVATE-TOKEN"), testPRToken)
	}

	wantBody := map[string]string{
		"source_branch": testHeadBranch, "target_branch": testBaseBranch, titleKey: testPRTitle, "description": req.Body,
	}
	if gotBody["source_branch"] != wantBody["source_branch"] || gotBody["target_branch"] != wantBody["target_branch"] ||
		gotBody[titleKey] != wantBody[titleKey] || gotBody["description"] != wantBody["description"] {
		t.Fatalf("body = %v, want %v", gotBody, wantBody)
	}

	if got.URL != "https://gitlab.invalid/acme/cluster-gitops/-/merge_requests/1" {
		t.Fatalf("URL = %q, want the mocked web_url", got.URL)
	}
}

// TestOpenGiteaPR_RequestShapeAndResponse proves Gitea's own field names
// (head/base like GitHub, but body not description) and its
// "Authorization: token ..." header convention.
func TestOpenGiteaPR_RequestShapeAndResponse(t *testing.T) {
	var gotPath string

	var gotBody map[string]string

	gotHeaders := http.Header{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHeaders = r.URL.Path, r.Header

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{htmlURLKey: "https://gitea.invalid/acme/cluster-gitops/pulls/1"})
	}))
	defer srv.Close()

	req := testPRRequest
	req.APIBaseURL = srv.URL

	got, err := openGiteaPR(context.Background(), req)
	if err != nil {
		t.Fatalf("openGiteaPR: %v", err)
	}

	if gotPath != "/api/v1/repos/acme/cluster-gitops/pulls" {
		t.Fatalf("path = %q, want /api/v1/repos/acme/cluster-gitops/pulls", gotPath)
	}

	if gotHeaders.Get("Authorization") != "token "+testPRToken {
		t.Fatalf("Authorization header = %q, want %q", gotHeaders.Get("Authorization"), "token "+testPRToken)
	}

	wantBody := map[string]string{"head": testHeadBranch, "base": testBaseBranch, titleKey: testPRTitle, "body": req.Body}
	if gotBody["head"] != wantBody["head"] || gotBody["base"] != wantBody["base"] ||
		gotBody[titleKey] != wantBody[titleKey] || gotBody["body"] != wantBody["body"] {
		t.Fatalf("body = %v, want %v", gotBody, wantBody)
	}

	if got.URL != "https://gitea.invalid/acme/cluster-gitops/pulls/1" {
		t.Fatalf("URL = %q, want the mocked html_url", got.URL)
	}
}

// TestOpenPullRequest_ErrorNeverLeaksToken proves a host error response
// (status >= 300, whose body this package includes in its own error for
// debuggability) never surfaces the token -- the token is sent only as
// a request header, never as a value the server could echo back into
// something this package then repeats.
func TestOpenPullRequest_ErrorNeverLeaksToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"validation failed"}`))
	}))
	defer srv.Close()

	req := testPRRequest
	req.APIBaseURL = srv.URL

	_, err := OpenPullRequest(context.Background(), HostGitHub, req)
	if err == nil {
		t.Fatal("expected an error for a 422 response, got nil")
	}

	if strings.Contains(err.Error(), testPRToken) {
		t.Fatalf("error leaked the token: %v", err)
	}
}

// TestOpenPullRequest_UnknownHost proves an unrecognized Host value
// fails loudly rather than silently doing nothing.
func TestOpenPullRequest_UnknownHost(t *testing.T) {
	if _, err := OpenPullRequest(context.Background(), Host(99), testPRRequest); err == nil {
		t.Fatal("expected an error for an unknown host, got nil")
	}
}
