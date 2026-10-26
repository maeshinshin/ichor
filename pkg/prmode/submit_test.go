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
	"path/filepath"
	"strings"
	"testing"
)

// TestSubmitPR_EndToEnd drives the whole pipeline -- clone a (local,
// no-network) bare repo, locate and patch the CR, push a new branch,
// open a PR against a mocked Gitea API -- proving every piece this unit
// added composes, not just that each piece works alone.
func TestSubmitPR_EndToEnd(t *testing.T) {
	bare := newBareRemote(t, "clusters/prod/cluster.yaml")

	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)

			return
		}

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding PR request body: %v", err)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{htmlURLKey: "https://gitea.invalid/acme/cluster-gitops/pulls/7"})
	}))
	defer srv.Close()

	req := SubmitPRRequest{
		Host: HostGitea, APIBaseURL: srv.URL,
		Owner: testOwner, Repo: testRepo, CloneURL: bare, Token: fakeToken,
		BaseBranch: testBaseBranch, HeadBranch: testHeadBranch,
		SourcePath: testClustersProdPath,
		Target:     patchTestTarget,
		Updates:    bumpTalosVersion,
		Title:      "Bump Talos to v1.10.0",
		Requester:  testRequester,
	}

	result, err := SubmitPR(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitPR: %v", err)
	}

	if result.URL != "https://gitea.invalid/acme/cluster-gitops/pulls/7" {
		t.Fatalf("result.URL = %q, want the mocked html_url", result.URL)
	}

	wrongHead := gotBody["head"] != testHeadBranch
	wrongBase := gotBody["base"] != testBaseBranch
	wrongTitle := gotBody[titleKey] != "Bump Talos to v1.10.0"

	if wrongHead || wrongBase || wrongTitle {
		t.Fatalf("PR request body = %v, want head/base/title set from the request", gotBody)
	}

	hasRequester := strings.Contains(gotBody["body"], "Requested by: ichor-ui")
	hasDiff := strings.Contains(gotBody["body"], "talosVersion: v1.10.0")

	if !hasRequester || !hasDiff {
		t.Fatalf("PR description missing requester or diff content:\n%s", gotBody["body"])
	}

	// The remote itself, not just the local checkout SubmitPR already
	// cleaned up, must carry the pushed branch and its patched content.
	verify := filepath.Join(t.TempDir(), "verify")

	if err := Clone(context.Background(), bare, testHeadBranch, fakeToken, verify); err != nil {
		t.Fatalf("Clone (verify): %v", err)
	}

	gotFile, err := os.ReadFile(filepath.Join(verify, "clusters", "prod", "cluster.yaml"))
	if err != nil {
		t.Fatalf("reading pushed file: %v", err)
	}

	if !strings.Contains(string(gotFile), "talosVersion: v1.10.0") {
		t.Fatalf("pushed file was not patched:\n%s", gotFile)
	}
}

// TestSubmitPR_TargetNotFoundFailsLoudlyAndNeverOpensAPR proves a
// mismatched Target fails before ever reaching the PR-opening HTTP call
// -- the httptest server below asserts it was never hit.
func TestSubmitPR_TargetNotFoundFailsLoudlyAndNeverOpensAPR(t *testing.T) {
	bare := newBareRemote(t, "cluster.yaml")

	called := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)

			return
		}

		called = true
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{htmlURLKey: "unexpected"})
	}))
	defer srv.Close()

	missingTarget := patchTestTarget
	missingTarget.Name = "does-not-exist"

	req := SubmitPRRequest{
		Host: HostGitea, APIBaseURL: srv.URL,
		Owner: testOwner, Repo: testRepo, CloneURL: bare, Token: fakeToken,
		BaseBranch: testBaseBranch, HeadBranch: testHeadBranch,
		SourcePath: "",
		Target:     missingTarget,
		Updates:    bumpTalosVersion,
	}

	_, err := SubmitPR(context.Background(), req)
	if err == nil {
		t.Fatal("expected an error for a Target absent from the repository, got nil")
	}

	if strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("error leaked the token: %v", err)
	}

	if called {
		t.Fatal("expected the PR API to never be called when the CR was never found")
	}
}
