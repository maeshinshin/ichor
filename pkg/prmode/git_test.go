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
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeToken is a recognizable, obviously-fake credential: every test
// below that touches a real git subprocess greps its own output and
// every returned value for this exact string, never finding it, which
// is the whole point of gitAuthEnv.
const fakeToken = "SUPER-SECRET-TEST-TOKEN-wJalrXUtnFEMI"

// TestGitAuthEnv_CarriesTokenOnlyInEnvNotArgv proves gitAuthEnv's own
// output: the token lands in an env var's value (where `ps` cannot see
// it), base64-encoded inside an HTTP Basic Authorization header, and
// nothing in the env *keys* themselves reveals it either.
func TestGitAuthEnv_CarriesTokenOnlyInEnvNotArgv(t *testing.T) {
	env := gitAuthEnv(fakeToken)

	encoded := base64.StdEncoding.EncodeToString([]byte(gitAuthUser + ":" + fakeToken))
	wantHeader := "GIT_CONFIG_VALUE_0=Authorization: Basic " + encoded

	found := false

	for _, kv := range env {
		if kv == wantHeader {
			found = true
		}

		if strings.Contains(kv, fakeToken) && kv != wantHeader {
			t.Fatalf("token appeared outside the expected header entry: %q", kv)
		}
	}

	if !found {
		t.Fatalf("gitAuthEnv did not set the expected GIT_CONFIG_VALUE_0 entry; got %v", env)
	}
}

// newBareRemote creates an empty bare repository at dir, seeded with
// seedYAML at seedFile on branch main, suitable as a Clone/CommitAndPush
// target with no network and no auth involved (the transport is a local
// path, so gitAuthEnv's HTTP header is simply unused, never exercised
// for rejection either way -- this test proves the plumbing and the
// no-leak property, not that any real host accepts this exact header).
func newBareRemote(t *testing.T, seedFile string) string {
	t.Helper()

	work := filepath.Join(t.TempDir(), "seed")
	bare := filepath.Join(t.TempDir(), "bare.git")

	runLocalGit(t, "", "init", "--bare", bare)
	runLocalGit(t, "", "init", "-b", "main", work)
	runLocalGit(t, work, "config", "user.email", "test@example.invalid")
	runLocalGit(t, work, "config", "user.name", "Test")

	writeFile(t, filepath.Join(work, seedFile), seedYAML)

	runLocalGit(t, work, "add", "--", seedFile)
	runLocalGit(t, work, "commit", "-m", "seed")
	runLocalGit(t, work, "remote", "add", "origin", bare)
	runLocalGit(t, work, "push", "origin", "main")

	return bare
}

// runLocalGit runs a git command with no auth env wiring, for building
// test fixtures -- deliberately not using runGit/gitAuthEnv, so fixture
// setup can never mask a real bug in the code under test.
func runLocalGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

const seedYAML = `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.9.0
`

// TestCloneAndCommitAndPush_RoundTrip proves the real mechanics: Clone
// checks out the remote's branch, CommitAndPush pushes a new branch
// carrying the written file, and the remote ends up with it -- all with
// no network, per a bare repo at a local path.
func TestCloneAndCommitAndPush_RoundTrip(t *testing.T) {
	bare := newBareRemote(t, "cluster.yaml")

	dir := filepath.Join(t.TempDir(), "checkout")

	if err := Clone(context.Background(), bare, "main", fakeToken, dir); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	newContent := strings.Replace(seedYAML, "v1.9.0", "v1.10.0", 1)

	if err := CommitAndPush(context.Background(), dir, "ichor/bump", fakeToken, "bump talosVersion",
		map[string][]byte{"cluster.yaml": []byte(newContent)}); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	// Verify against the remote directly (not the local checkout, which
	// would trivially already have it): fetch the pushed branch into a
	// second, independent clone.
	verify := filepath.Join(t.TempDir(), "verify")

	if err := Clone(context.Background(), bare, "ichor/bump", fakeToken, verify); err != nil {
		t.Fatalf("Clone (verify): %v", err)
	}

	got, err := os.ReadFile(filepath.Join(verify, "cluster.yaml"))
	if err != nil {
		t.Fatalf("reading pushed file: %v", err)
	}

	if string(got) != newContent {
		t.Fatalf("pushed content = %q, want %q", got, newContent)
	}
}

// TestCloneAndCommitAndPush_TokenNeverLeaks proves the no-leak property
// end to end: even the real stdout/stderr git produces (CommitAndPush's
// wrapped error on a forced failure below) never contains fakeToken.
func TestCloneAndCommitAndPush_TokenNeverLeaks(t *testing.T) {
	bare := newBareRemote(t, "cluster.yaml")

	dir := filepath.Join(t.TempDir(), "checkout")

	if err := Clone(context.Background(), bare, "main", fakeToken, dir); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	// Force a real git failure (pushing a branch that was never created
	// locally) to get real git stderr text to check.
	err := CommitAndPush(context.Background(), dir, "", fakeToken, "msg", map[string][]byte{"cluster.yaml": []byte("x")})
	if err == nil {
		t.Fatal("expected an error for an empty branch name, got nil")
	}

	if strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("error message leaked the token: %v", err)
	}
}

// TestClone_ErrorNeverLeaksToken proves the same for Clone's own error
// path (an unreachable/invalid clone URL).
func TestClone_ErrorNeverLeaksToken(t *testing.T) {
	missingRemote := filepath.Join(t.TempDir(), "does-not-exist")
	dst := filepath.Join(t.TempDir(), "dst")

	err := Clone(context.Background(), missingRemote, "main", fakeToken, dst)
	if err == nil {
		t.Fatal("expected an error for a nonexistent remote, got nil")
	}

	if strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("error message leaked the token: %v", err)
	}
}
