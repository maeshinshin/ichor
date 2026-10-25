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

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSaveBundleFile_PermissionsAndRoundTrip proves architecture.md's
// "Trust" section's two requirements at once: the file and its directory
// end up 0600/0700, and loadBundleFile reads back exactly what was
// written.
func TestSaveBundleFile_PermissionsAndRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	bundle := newTestBundle(t)

	path, err := saveBundleFile("my-cluster", bundle)
	if err != nil {
		t.Fatalf("saveBundleFile: %v", err)
	}

	wantPath := filepath.Join(home, ".ichor", "clusters", "my-cluster", "secrets.yaml")
	if path != wantPath {
		t.Fatalf("saveBundleFile path = %q, want %q", path, wantPath)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	if got := fi.Mode().Perm(); got != bundleFilePerm {
		t.Fatalf("file mode = %o, want %o", got, bundleFilePerm)
	}

	dirFi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}

	if got := dirFi.Mode().Perm(); got != bundleDirPerm {
		t.Fatalf("dir mode = %o, want %o", got, bundleDirPerm)
	}

	got, err := loadBundleFile(path)
	if err != nil {
		t.Fatalf("loadBundleFile: %v", err)
	}

	if got.Cluster.ID != bundle.Cluster.ID || got.Secrets.BootstrapToken != bundle.Secrets.BootstrapToken {
		t.Fatalf("round-tripped bundle does not match: got cluster ID %q, bootstrap token %q",
			got.Cluster.ID, got.Secrets.BootstrapToken)
	}
}

// TestSaveBundleFile_FixesLooseExistingDirectory covers a second run
// against a directory some earlier, looser process left behind: the
// permission still ends up 0700, not whatever it was.
func TestSaveBundleFile_FixesLooseExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := bundleDir("my-cluster")
	if err != nil {
		t.Fatalf("bundleDir: %v", err)
	}

	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("pre-creating loose directory: %v", err)
	}

	if _, err := saveBundleFile("my-cluster", newTestBundle(t)); err != nil {
		t.Fatalf("saveBundleFile: %v", err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}

	if got := fi.Mode().Perm(); got != bundleDirPerm {
		t.Fatalf("dir mode = %o, want %o (saveBundleFile should have tightened it)", got, bundleDirPerm)
	}
}

func TestNagBackup_MentionsPathAndConsequence(t *testing.T) {
	var out bytes.Buffer

	nagBackup(&out, "/home/user/.ichor/clusters/my-cluster/secrets.yaml")

	msg := out.String()
	if !strings.Contains(msg, "/home/user/.ichor/clusters/my-cluster/secrets.yaml") {
		t.Fatalf("expected the backup path in the nag message, got: %s", msg)
	}

	if !strings.Contains(strings.ToLower(msg), "back it up") && !strings.Contains(strings.ToLower(msg), "back up") {
		t.Fatalf("expected a back-up instruction, got: %s", msg)
	}
}
