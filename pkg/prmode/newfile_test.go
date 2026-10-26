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

func giteaServer(t *testing.T, got *map[string]string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(got)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{htmlURLKey: "https://gitea.invalid/acme/r/pulls/9"})
	}))
	t.Cleanup(srv.Close)

	return srv
}

// TestSubmitPR_NewFile proves a request with NewFile adds that file to the
// repository, on a new branch, without Locate or Patch (strict mode, ADR
// 0056: the CR is not in Git yet, so there is nothing to locate).
func TestSubmitPR_NewFile(t *testing.T) {
	bare := newBareRemote(t, "clusters/prod/cluster.yaml")

	var gotBody map[string]string

	srv := giteaServer(t, &gotBody)

	req := SubmitPRRequest{
		Host: HostGitea, APIBaseURL: srv.URL,
		Owner: testOwner, Repo: testRepo, CloneURL: bare, Token: fakeToken,
		BaseBranch: testBaseBranch, HeadBranch: testHeadBranch,
		NewFile:   &NewFile{Path: "clusters/prod/machines/node-a.yaml", Content: []byte("kind: TalosMachine\n")},
		Title:     "Approve node-a",
		Requester: "ichor-ui",
	}

	res, err := SubmitPR(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitPR: %v", err)
	}

	if res.URL == "" || !strings.Contains(gotBody["body"], "+kind: TalosMachine") {
		t.Fatalf("url=%q body=%q: want a URL and the new file in the diff", res.URL, gotBody["body"])
	}

	verify := filepath.Join(t.TempDir(), "verify")
	if err := Clone(context.Background(), bare, testHeadBranch, fakeToken, verify); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(verify, "clusters", "prod", "machines", "node-a.yaml"))
	if err != nil || string(data) != "kind: TalosMachine\n" {
		t.Fatalf("pushed file = %q, %v", data, err)
	}
}

func TestSubmitPR_NewFileRefusals(t *testing.T) {
	bare := newBareRemote(t, "cluster.yaml")

	for name, path := range map[string]string{
		"exists":    "cluster.yaml",
		"dotdot":    "../escape.yaml",
		"absolute":  "/tmp/escape.yaml",
		"dot-git":   ".git/hooks/x",
		"empty":     "",
		"traversal": "a/../../b.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			var gotBody map[string]string

			srv := giteaServer(t, &gotBody)

			_, err := SubmitPR(context.Background(), SubmitPRRequest{
				Host: HostGitea, APIBaseURL: srv.URL,
				Owner: testOwner, Repo: testRepo, CloneURL: bare, Token: fakeToken,
				BaseBranch: testBaseBranch, HeadBranch: testHeadBranch,
				NewFile: &NewFile{Path: path, Content: []byte("x: 1\n")},
			})
			if err == nil {
				t.Fatal("want a refusal")
			}

			if gotBody != nil {
				t.Fatal("the PR API must not be called")
			}
		})
	}
}

func TestPatch_AddsAMissingLeafKey(t *testing.T) {
	input := "apiVersion: ichor.maesh.dev/v1alpha1\nkind: TalosCluster\nmetadata:\n  name: prod\n  namespace: ichor-system\n" +
		"spec:\n  # keep me\n  talosVersion: v1.9.0\n"

	got, err := Patch([]byte(input), patchTestTarget, []FieldUpdate{{Path: []string{specKey, "role"}, Value: "worker"}})
	if err != nil {
		t.Fatal(err)
	}

	if want := input + "  role: worker\n"; string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// A missing intermediate key is still an error: only the leaf is added.
	_, err = Patch([]byte(input), patchTestTarget, []FieldUpdate{{Path: []string{"nope", "role"}, Value: "x"}})
	if err == nil {
		t.Fatal("want an error for a missing parent key")
	}
}
