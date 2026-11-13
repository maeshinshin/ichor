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

	"github.com/siderolabs/talos/pkg/machinery/role"
)

// osWindows names the one platform these permission-bit assertions don't
// apply to.
const osWindows = "windows"

// TestSaveBundleFile_PermissionsAndRoundTrip proves architecture.md's
// "Trust" section's two requirements at once: the file and its directory
// end up 0600/0700, and loadBundleFile reads back exactly what was
// written.
func TestSaveBundleFile_PermissionsAndRoundTrip(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("POSIX permission bits don't apply on windows")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	bundle := newTestBundle(t)

	path, err := saveBundleFile("my-cluster", bundle, "")
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
	if runtime.GOOS == osWindows {
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

	if _, err := saveBundleFile("my-cluster", newTestBundle(t), ""); err != nil {
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

// TestWriteFileAtomic_SyncsFileBeforeClose proves the fsync call site
// writeFileAtomic's doc comment promises is actually reached on the
// happy path: swap fsyncFile for a spy, write once, and check it was
// called exactly once, on the temp file, before any error could have
// swallowed it silently. A real crash cannot be reproduced in a unit
// test; this is the part that can be: the code path to fsyncFile runs
// every time, not just when someone remembers to call it.
func TestWriteFileAtomic_SyncsFileBeforeClose(t *testing.T) {
	var syncedPaths []string

	orig := fsyncFile
	fsyncFile = func(f *os.File) error {
		syncedPaths = append(syncedPaths, f.Name())
		return orig(f)
	}

	t.Cleanup(func() { fsyncFile = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := writeFileAtomic(path, []byte("hello")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	if len(syncedPaths) != 1 {
		t.Fatalf("fsyncFile called %d times, want 1 (got %v)", len(syncedPaths), syncedPaths)
	}

	if got := filepath.Dir(syncedPaths[0]); got != filepath.Clean(dir) {
		t.Fatalf("synced file %q is not in the target directory %q", syncedPaths[0], dir)
	}
}

// fakeSyncError is a test-only error type injected via fsyncFile, so
// TestWriteFileAtomic_PropagatesSyncError can assert on it by identity
// rather than by matching an OS-specific message string.
type fakeSyncError struct{}

func (*fakeSyncError) Error() string { return "fake sync failure injected by a test" }

var errFakeSync error = &fakeSyncError{}

// TestWriteFileAtomic_PropagatesSyncError proves a failing fsync fails
// the whole write rather than being swallowed: the file must never end
// up renamed into place if its data was never confirmed durable.
func TestWriteFileAtomic_PropagatesSyncError(t *testing.T) {
	orig := fsyncFile
	fsyncFile = func(*os.File) error { return errFakeSync }
	t.Cleanup(func() { fsyncFile = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	err := writeFileAtomic(path, []byte("hello"))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), errFakeSync.Error()) {
		t.Fatalf("expected the sync error to propagate, got: %v", err)
	}

	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("expected no file at path when fsync fails, but one exists")
	}
}

// TestWriteFileAtomic_RoundTrip is the ordinary case: content and
// permissions survive the write.
func TestWriteFileAtomic_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	if err := writeFileAtomic(path, []byte("hello")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back %s: %v", path, err)
	}

	if string(got) != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}

	if runtime.GOOS != osWindows {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		if got := fi.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode = %o, want 0600", got)
		}
	}
}

// TestFsyncDir_ErrorsOnMissingDir proves fsyncDir's own failure path is
// reachable and returns a clear error rather than panicking or
// succeeding silently.
func TestFsyncDir_ErrorsOnMissingDir(t *testing.T) {
	err := fsyncDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a missing directory, got nil")
	}
}

// TestLoadBundleFileFillsClock: a bundle read from a file must be able to mint a client certificate, which
// `ichor restore cluster` does to dial the node. Without a Clock it panicked in dialRecover.
func TestLoadBundleFileFillsClock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path := saveTestBundle(t, "my-cluster", newTestBundle(t), "")

	got, err := loadBundleFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := got.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin)); err != nil {
		t.Fatalf("GenerateTalosAPIClientCertificate: %v", err)
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
