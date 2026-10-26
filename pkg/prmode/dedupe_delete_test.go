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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for ADR 0056's two additions: a repeated request finds the open PR
// instead of opening another, and a tracked CR can be removed by PR.

// lookup records the one GET a FindOpenPullRequest makes.
type lookup struct {
	method, path, rawQuery, auth string
}

func lookupServer(t *testing.T, got *lookup, status int, body any) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = lookup{r.Method, r.URL.Path, r.URL.Query().Encode(), r.Header.Get("Authorization") + r.Header.Get("PRIVATE-TOKEN")}

		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestFindOpenPullRequest_GitHub(t *testing.T) {
	var got lookup

	srv := lookupServer(t, &got, http.StatusOK, []map[string]string{
		{htmlURLKey: "https://github.invalid/acme/r/pull/4", "state": "open"},
	})

	req := testPRRequest
	req.APIBaseURL = srv.URL

	res, found, err := FindOpenPullRequest(context.Background(), HostGitHub, req)
	if err != nil || !found || res.URL != "https://github.invalid/acme/r/pull/4" {
		t.Fatalf("got %v %v %v", res, found, err)
	}

	if got.method != http.MethodGet || got.path != "/repos/acme/cluster-gitops/pulls" {
		t.Fatalf("request = %+v", got)
	}

	if got.rawQuery != "base=main&head=acme%3Aichor%2Fbump-talos&state=open" {
		t.Fatalf("query = %q", got.rawQuery)
	}

	if got.auth != "Bearer "+testPRToken {
		t.Fatalf("auth = %q", got.auth)
	}
}

func TestFindOpenPullRequest_GitHubNone(t *testing.T) {
	var got lookup

	srv := lookupServer(t, &got, http.StatusOK, []map[string]string{})

	req := testPRRequest
	req.APIBaseURL = srv.URL

	if _, found, err := FindOpenPullRequest(context.Background(), HostGitHub, req); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestFindOpenPullRequest_GitLab(t *testing.T) {
	var got lookup

	srv := lookupServer(t, &got, http.StatusOK, []map[string]string{
		{"web_url": "https://gitlab.invalid/acme/r/-/merge_requests/2", "state": "opened"},
	})

	req := testPRRequest
	req.APIBaseURL = srv.URL

	res, found, err := FindOpenPullRequest(context.Background(), HostGitLab, req)
	if err != nil || !found || res.URL != "https://gitlab.invalid/acme/r/-/merge_requests/2" {
		t.Fatalf("got %v %v %v", res, found, err)
	}

	if got.method != http.MethodGet || got.path != "/api/v4/projects/acme/cluster-gitops/merge_requests" {
		t.Fatalf("request = %+v", got)
	}

	if got.rawQuery != "source_branch=ichor%2Fbump-talos&state=opened&target_branch=main" {
		t.Fatalf("query = %q", got.rawQuery)
	}

	if got.auth != testPRToken {
		t.Fatalf("PRIVATE-TOKEN = %q", got.auth)
	}
}

func TestFindOpenPullRequest_Gitea(t *testing.T) {
	var got lookup

	srv := lookupServer(t, &got, http.StatusOK, map[string]string{htmlURLKey: "https://gitea.invalid/acme/r/pulls/5", "state": "open"})

	req := testPRRequest
	req.APIBaseURL = srv.URL

	res, found, err := FindOpenPullRequest(context.Background(), HostGitea, req)
	if err != nil || !found || res.URL != "https://gitea.invalid/acme/r/pulls/5" {
		t.Fatalf("got %v %v %v", res, found, err)
	}

	if got.method != http.MethodGet || got.path != "/api/v1/repos/acme/cluster-gitops/pulls/main/ichor/bump-talos" {
		t.Fatalf("request = %+v", got)
	}

	if got.auth != "token "+testPRToken {
		t.Fatalf("auth = %q", got.auth)
	}
}

func TestFindOpenPullRequest_GiteaNotOpenOrMissing(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   any
	}{
		"404":    {http.StatusNotFound, map[string]string{}},
		"closed": {http.StatusOK, map[string]string{htmlURLKey: "https://gitea.invalid/p/1", "state": "closed"}},
	} {
		t.Run(name, func(t *testing.T) {
			var got lookup

			srv := lookupServer(t, &got, tc.status, tc.body)
			req := testPRRequest
			req.APIBaseURL = srv.URL

			if _, found, err := FindOpenPullRequest(context.Background(), HostGitea, req); err != nil || found {
				t.Fatalf("found=%v err=%v", found, err)
			}
		})
	}
}

// A lookup that fails for another reason must not be read as "none": that
// would open a duplicate. The token is not in the error either.
func TestFindOpenPullRequest_ErrorIsNotNone(t *testing.T) {
	var got lookup

	srv := lookupServer(t, &got, http.StatusUnauthorized, map[string]string{"message": "bad credentials"})
	req := testPRRequest
	req.APIBaseURL = srv.URL

	_, found, err := FindOpenPullRequest(context.Background(), HostGitHub, req)
	if err == nil || found {
		t.Fatalf("found=%v err=%v, want an error", found, err)
	}

	if strings.Contains(err.Error(), testPRToken) {
		t.Fatalf("error leaked the token: %v", err)
	}
}

// TestSubmitPR_ReturnsTheOpenPullRequest: with an open PR for the head
// branch, SubmitPR neither pushes nor posts.
func TestSubmitPR_ReturnsTheOpenPullRequest(t *testing.T) {
	bare := newBareRemote(t, "clusters/prod/cluster.yaml")
	posted := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted = true
		}

		_ = json.NewEncoder(w).Encode(map[string]string{htmlURLKey: "https://gitea.invalid/acme/r/pulls/8", "state": "open"})
	}))
	defer srv.Close()

	res, err := SubmitPR(context.Background(), SubmitPRRequest{
		Host: HostGitea, APIBaseURL: srv.URL, Owner: testOwner, Repo: testRepo, CloneURL: bare, Token: fakeToken,
		BaseBranch: testBaseBranch, HeadBranch: testHeadBranch, SourcePath: "clusters/prod",
		Target: patchTestTarget, Updates: bumpTalosVersion,
	})
	if err != nil || res.URL != "https://gitea.invalid/acme/r/pulls/8" {
		t.Fatalf("got %v %v", res, err)
	}

	if posted {
		t.Fatal("a PR was opened although one is open")
	}

	if out, _ := exec.Command("git", "-C", bare, "for-each-ref", "refs/heads/ichor/").Output(); len(out) != 0 {
		t.Fatalf("a branch was pushed: %s", out)
	}
}

// TestSubmitPR_ReusesABranchLeftByAClosedPR: no open PR but the branch is
// still on the remote (the earlier PR was closed): the push replaces it.
func TestSubmitPR_ReusesABranchLeftByAClosedPR(t *testing.T) {
	bare := newBareRemote(t, "clusters/prod/cluster.yaml")

	var gotBody map[string]string

	srv := giteaServer(t, &gotBody)
	req := SubmitPRRequest{
		Host: HostGitea, APIBaseURL: srv.URL, Owner: testOwner, Repo: testRepo, CloneURL: bare, Token: fakeToken,
		BaseBranch: testBaseBranch, HeadBranch: testHeadBranch, SourcePath: "clusters/prod",
		Target: patchTestTarget, Updates: bumpTalosVersion,
	}

	for range 2 {
		if _, err := SubmitPR(context.Background(), req); err != nil {
			t.Fatalf("SubmitPR: %v", err)
		}
	}
}

const multiDocFile = `# nodes
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: other
  namespace: ichor-system
spec:
  talosVersion: v1.9.0 # keep
---
# the one to go
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.9.0
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: tail
data:
    odd:   "spacing"
`

func TestRemoveDocument(t *testing.T) {
	target := patchTestTarget

	cases := map[string]struct {
		in, want  string
		remaining bool
	}{
		"middle": {
			in: multiDocFile,
			want: multiDocFile[:strings.Index(multiDocFile, "---\n# the one to go")] +
				multiDocFile[strings.Index(multiDocFile, "---\napiVersion: v1"):],
			remaining: true,
		},
		"first": {
			in:        "apiVersion: ichor.maesh.dev/v1alpha1\nkind: TalosCluster\nmetadata:\n  name: prod\n  namespace: ichor-system\n---\nkind: ConfigMap\n  # odd\n",
			want:      "kind: ConfigMap\n  # odd\n",
			remaining: true,
		},
		"last": {
			in:        "kind: ConfigMap\n---\napiVersion: ichor.maesh.dev/v1alpha1\nkind: TalosCluster\nmetadata:\n  name: prod\n  namespace: ichor-system\n",
			want:      "kind: ConfigMap\n",
			remaining: true,
		},
		"only": {in: seedYAML, want: "", remaining: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, remaining, err := RemoveDocument([]byte(tc.in), target)
			if err != nil {
				t.Fatal(err)
			}

			if string(got) != tc.want || remaining != tc.remaining {
				t.Fatalf("got (%v)\n%q\nwant\n%q", remaining, got, tc.want)
			}
		})
	}

	if _, _, err := RemoveDocument([]byte(seedYAML), Target{Kind: "Nope"}); err == nil {
		t.Fatal("a target that matches nothing must fail")
	}
}

func pushFile(t *testing.T, bare, path, content string) {
	t.Helper()

	work := filepath.Join(t.TempDir(), "w")
	runLocalGit(t, "", "clone", "-b", "main", bare, work)
	runLocalGit(t, work, "config", "user.email", "test@example.invalid")
	runLocalGit(t, work, "config", "user.name", "Test")
	writeFile(t, filepath.Join(work, path), content)
	runLocalGit(t, work, "add", "--", path)
	runLocalGit(t, work, "commit", "-m", "more")
	runLocalGit(t, work, "push", "origin", "main")
}

func deleteRequest(srvURL, bare string) SubmitPRRequest {
	return SubmitPRRequest{
		Host: HostGitea, APIBaseURL: srvURL, Owner: testOwner, Repo: testRepo, CloneURL: bare, Token: fakeToken,
		BaseBranch: testBaseBranch, HeadBranch: "ichor/delete-prod", SourcePath: "clusters/prod",
		Target: patchTestTarget, Delete: true, Title: "Delete prod", Requester: "ichor-ui",
	}
}

func fileAt(t *testing.T, bare, branch, path string) (string, bool) {
	t.Helper()

	out, err := exec.Command("git", "-C", bare, "show", branch+":"+path).Output()

	return string(out), err == nil
}

func TestSubmitPR_DeleteRemovesTheFileWhenItIsTheOnlyDocument(t *testing.T) {
	bare := newBareRemote(t, "clusters/prod/cluster.yaml")

	var gotBody map[string]string

	srv := giteaServer(t, &gotBody)

	if _, err := SubmitPR(context.Background(), deleteRequest(srv.URL, bare)); err != nil {
		t.Fatal(err)
	}

	if _, ok := fileAt(t, bare, "ichor/delete-prod", "clusters/prod/cluster.yaml"); ok {
		t.Fatal("the file is still on the branch")
	}

	if _, ok := fileAt(t, bare, "main", "clusters/prod/cluster.yaml"); !ok {
		t.Fatal("main lost the file")
	}

	if !strings.Contains(gotBody["body"], "-kind: TalosCluster") {
		t.Fatalf("PR diff does not show the removal:\n%s", gotBody["body"])
	}
}

func TestSubmitPR_DeleteKeepsTheOtherDocumentsByteForByte(t *testing.T) {
	bare := newBareRemote(t, "clusters/prod/cluster.yaml")
	pushFile(t, bare, "clusters/prod/multi.yaml", multiDocFile)

	var gotBody map[string]string

	srv := giteaServer(t, &gotBody)

	// The seeded cluster.yaml also holds a TalosCluster "prod"; point at
	// the multi-document file by removing the seed first.
	pushRemove(t, bare, "clusters/prod/cluster.yaml")

	if _, err := SubmitPR(context.Background(), deleteRequest(srv.URL, bare)); err != nil {
		t.Fatal(err)
	}

	got, ok := fileAt(t, bare, "ichor/delete-prod", "clusters/prod/multi.yaml")
	if !ok {
		t.Fatal("the multi-document file must stay")
	}

	want := multiDocFile[:strings.Index(multiDocFile, "---\n# the one to go")] +
		multiDocFile[strings.Index(multiDocFile, "---\napiVersion: v1"):]
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func pushRemove(t *testing.T, bare, path string) {
	t.Helper()

	work := filepath.Join(t.TempDir(), "w")
	runLocalGit(t, "", "clone", "-b", "main", bare, work)
	runLocalGit(t, work, "config", "user.email", "test@example.invalid")
	runLocalGit(t, work, "config", "user.name", "Test")

	if err := os.Remove(filepath.Join(work, path)); err != nil {
		t.Fatal(err)
	}

	runLocalGit(t, work, "commit", "-am", "drop")
	runLocalGit(t, work, "push", "origin", "main")
}
