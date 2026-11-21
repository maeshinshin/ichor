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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maeshinshin/ichor/internal/imagesig"
)

const (
	newBinary = "new binary"
	oldBinary = "old binary"
	olderTag  = "v0.2.5"
)

// fixtureVerifier accepts the bundle "signed:<digest>" made for exactly that checksums file and the tag's identity.
type fixtureVerifier struct{ tag string }

func (f fixtureVerifier) VerifyBlob(bundle []byte, digest string, id imagesig.Identity) error {
	if id != imagesig.IchorIdentity(f.tag) {
		return errors.New("signed by another identity")
	}

	if string(bundle) != "signed:"+digest {
		return errors.New("the bundle does not sign this digest")
	}

	return nil
}

// fakeRelease serves the assets of tags (tag -> binary content) and records the paths asked for.
type fakeRelease struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits []string
}

func tarball(t *testing.T, binary string) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, f := range []struct{ n, c string }{{"LICENSE", "license"}, {binaryName, binary}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.n, Mode: 0o644, Size: int64(len(f.c)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}

		if _, err := tw.Write([]byte(f.c)); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// newRelease publishes each tag's archive for linux/amd64, its checksums and a bundle. checksumFor and
// bundleFor let a test corrupt one tag's checksums line or bundle.
func newRelease(t *testing.T, tags map[string]string, checksumFor func(tag string, sum string) string, bundleFor func(tag, digest string) string) *fakeRelease {
	t.Helper()

	r := &fakeRelease{}
	files := map[string][]byte{}

	for tag, bin := range tags {
		v := strings.TrimPrefix(tag, "v")
		name := fmt.Sprintf("ichor_%s_linux_amd64.tar.gz", v)
		arch := tarball(t, bin)
		sum := sha256.Sum256(arch)
		line := hex.EncodeToString(sum[:])

		if checksumFor != nil {
			line = checksumFor(tag, line)
		}

		sums := []byte(line + "  " + name + "\n")
		ds := sha256.Sum256(sums)
		digest := "sha256:" + hex.EncodeToString(ds[:])
		bundle := "signed:" + digest

		if bundleFor != nil {
			bundle = bundleFor(tag, digest)
		}

		files["/"+tag+"/"+name] = arch
		files["/"+tag+"/ichor_"+v+"_checksums.txt"] = sums
		files["/"+tag+"/ichor_"+v+"_checksums.txt.sigstore.json"] = []byte(bundle)
	}

	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hits = append(r.hits, req.URL.Path)
		r.mu.Unlock()

		b, ok := files[req.URL.Path]
		if !ok {
			http.NotFound(w, req)

			return
		}

		_, _ = w.Write(b)
	}))
	t.Cleanup(r.srv.Close)

	return r
}

func (r *fakeRelease) archiveFetched() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, h := range r.hits {
		if strings.HasSuffix(h, ".tar.gz") {
			return true
		}
	}

	return false
}

// installedBinary writes a fake current binary into a fresh directory.
func installedBinary(t *testing.T) string {
	t.Helper()

	exe := filepath.Join(t.TempDir(), binaryName)
	if err := os.WriteFile(exe, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}

	return exe
}

func upgrader(r *fakeRelease, exe string) *cliUpgrade {
	return &cliUpgrade{
		Current: noticeNewer, Exe: exe, BaseURL: r.srv.URL, OS: "linux", Arch: "amd64",
		Latest:   func() (string, error) { return humanLatest, nil },
		Verifier: fixtureVerifier{tag: humanLatest},
	}
}

func runUpgrader(t *testing.T, u *cliUpgrade, answer string) (string, error) {
	t.Helper()

	var out, errw bytes.Buffer

	err := u.run(t.Context(), strings.NewReader(answer), &out, &errw)

	return out.String() + errw.String(), err
}

func content(t *testing.T, p string) string {
	t.Helper()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestUpgradeCLIReplacesTheBinaryAfterVerification(t *testing.T) {
	r := newRelease(t, map[string]string{humanLatest: newBinary}, nil, nil)
	exe := installedBinary(t)

	out, err := runUpgrader(t, upgrader(r, exe), "y\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if got := content(t, exe); got != newBinary {
		t.Errorf("the executable holds %q, want the release's binary", got)
	}

	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v, want 0755", fi.Mode().Perm())
	}

	if ents, _ := os.ReadDir(filepath.Dir(exe)); len(ents) != 1 {
		t.Errorf("the directory holds %d entries, want only the executable (no staging file left)", len(ents))
	}
}

func TestUpgradeCLIRefusesABadSignature(t *testing.T) {
	r := newRelease(t, map[string]string{humanLatest: newBinary}, nil,
		func(_, _ string) string { return "signed:sha256:forged" })
	exe := installedBinary(t)

	out, err := runUpgrader(t, upgrader(r, exe), "y\n")
	if err == nil || !strings.Contains(err.Error(), "does not verify") || !strings.Contains(err.Error(), "cosign verify-blob") {
		t.Fatalf("want a refusal naming the signature and the manual steps, got %v\n%s", err, out)
	}

	if got := content(t, exe); got != oldBinary {
		t.Errorf("the executable was changed: %q", got)
	}

	if r.archiveFetched() {
		t.Error("the archive was downloaded before the signature verified")
	}
}

func TestUpgradeCLIRefusesAChecksumMismatch(t *testing.T) {
	r := newRelease(t, map[string]string{humanLatest: newBinary},
		func(_, _ string) string { return strings.Repeat("0", 64) }, nil)
	exe := installedBinary(t)

	out, err := runUpgrader(t, upgrader(r, exe), "y\n")
	if err == nil || !strings.Contains(err.Error(), "does not match the signed checksums") {
		t.Fatalf("want a checksum refusal, got %v\n%s", err, out)
	}

	if got := content(t, exe); got != oldBinary {
		t.Errorf("the executable was changed: %q", got)
	}

	if ents, _ := os.ReadDir(filepath.Dir(exe)); len(ents) != 1 {
		t.Errorf("a staging file was left behind: %d entries", len(ents))
	}
}

func TestUpgradeCLIVersionSelectsTheRelease(t *testing.T) {
	r := newRelease(t, map[string]string{humanLatest: "binary 0.3.0", olderTag: "binary 0.2.5"}, nil, nil)

	exe := installedBinary(t)
	u := upgrader(r, exe)
	u.Version = olderTag
	u.Verifier = fixtureVerifier{tag: olderTag}

	if out, err := runUpgrader(t, u, "y\n"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if got := content(t, exe); got != "binary 0.2.5" {
		t.Errorf("--version v0.2.5 installed %q", got)
	}

	exe = installedBinary(t)
	if out, err := runUpgrader(t, upgrader(r, exe), "y\n"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if got := content(t, exe); got != "binary 0.3.0" {
		t.Errorf("without --version installed %q, want the newest", got)
	}
}

func TestUpgradeCLIReportsAnExecutableThatCannotBeReplaced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}

	r := newRelease(t, map[string]string{humanLatest: newBinary}, nil, nil)
	exe := installedBinary(t)
	dir := filepath.Dir(exe)

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	out, err := runUpgrader(t, upgrader(r, exe), "y\n")
	if err == nil || !strings.Contains(err.Error(), "cannot write next to") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("want a permission error naming the directory, got %v\n%s", err, out)
	}

	if r.archiveFetched() {
		t.Error("the archive was downloaded although the executable cannot be replaced")
	}
}

func TestUpgradeCLIRefusesADevelopmentBuildAndChangesNothingOnCheck(t *testing.T) {
	r := newRelease(t, map[string]string{humanLatest: newBinary}, nil, nil)
	exe := installedBinary(t)

	u := upgrader(r, exe)
	u.Current = "dev"

	if _, err := runUpgrader(t, u, "y\n"); err == nil || !strings.Contains(err.Error(), "development build") {
		t.Errorf("a dev build was not refused: %v", err)
	}

	u = upgrader(r, exe)
	u.Check = true

	out, err := runUpgrader(t, u, "")
	if err != nil || !strings.Contains(out, "would be replaced by v0.3.0") {
		t.Errorf("--check: %v\n%s", err, out)
	}

	if len(r.hits) != 0 || content(t, exe) != oldBinary {
		t.Errorf("--check touched the release (%v) or the binary", r.hits)
	}
}

func TestUpgradeCLIOfflineNamesTheManualSteps(t *testing.T) {
	r := newRelease(t, map[string]string{humanLatest: newBinary}, nil, nil)
	exe := installedBinary(t)
	u := upgrader(r, exe)

	r.srv.Close()

	_, err := runUpgrader(t, u, "y\n")
	if err == nil || !strings.Contains(err.Error(), "air-gapped") || !strings.Contains(err.Error(), "sha256sum -c") {
		t.Errorf("want the offline message with manual steps, got %v", err)
	}
}
