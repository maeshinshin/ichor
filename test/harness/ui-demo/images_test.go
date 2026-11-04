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

// Package uidemo tests the release image scripts: images.sh and hack/pin-image.sh (ADR 0122, follow-up).
package uidemo

import (
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	repoRoot = "../../.."
	release  = "v0.1.0"
	live     = "ghcr.io/maeshinshin"
	imgCtl   = live + "/ichor-controller:" + release
	imgUI    = live + "/ichor-ui:" + release
	hex64    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// run executes a command in dir with env added to the process environment minus make's own flags (a
// `make test` that runs this test would otherwise hand its jobserver to the make started here).
func run(t *testing.T, dir string, env []string, cmd string, args ...string) (string, error) {
	t.Helper()

	c := exec.Command(cmd, args...)
	c.Dir = dir

	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MAKEFLAGS=") && !strings.HasPrefix(kv, "MFLAGS=") && !strings.HasPrefix(kv, "MAKELEVEL=") {
			c.Env = append(c.Env, kv)
		}
	}

	c.Env = append(c.Env, env...)
	out, err := c.CombinedOutput()

	return string(out), err
}

func abs(t *testing.T, rel string) string {
	t.Helper()

	p, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestPinImage(t *testing.T) {
	script := abs(t, filepath.Join(repoRoot, "hack/pin-image.sh"))
	digests := filepath.Join(t.TempDir(), "digests.txt")
	ctl, ui := "sha256:"+hex64, "sha256:"+strings.ReplaceAll(hex64, "0", "f")

	write(t, digests, "localhost:5000/maeshinshin/ichor-controller@"+ctl+"\n"+
		"localhost:5000/maeshinshin/ichor-ui@"+ui+"\n"+
		live+"/charts/ichor@sha256:"+strings.ReplaceAll(hex64, "0", "a")+"\n")

	for label, tc := range map[string]struct{ image, ref, want string }{
		"tag":            {"ichor-controller", imgCtl, imgCtl + "@" + ctl},
		"other image":    {"ichor-ui", imgUI, imgUI + "@" + ui},
		"replaces a pin": {"ichor-ui", imgUI + "@sha256:" + strings.Repeat("1", 64), imgUI + "@" + ui},
	} {
		t.Run(label, func(t *testing.T) {
			out, err := run(t, t.TempDir(), nil, script, digests, tc.image, tc.ref)
			if err != nil || strings.TrimSpace(out) != tc.want {
				t.Fatalf("pin-image = %q, %v; want %q", out, err, tc.want)
			}
		})
	}

	// An image with no digest is an error, never the tag: a release asked to pin must not fall back.
	out, err := run(t, t.TempDir(), nil, script, digests, "ichor-installer", live+"/ichor-installer:"+release)
	if err == nil || !strings.Contains(out, "no digest for ichor-installer") {
		t.Fatalf("pin-image for an image with no digest = %q, %v; want a refusal", out, err)
	}
}

// fakeGo stands in for `go build -o OUT ...` so images.sh compiles nothing: it writes the version the script
// checks the binary for.
const fakeGo = `#!/usr/bin/env bash
while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
mkdir -p "$(dirname "$out")"
echo "fake binary $VERSION" > "$out"
`

// TestReleaseFlowDryRun runs the release's image steps in order against an in-memory registry: push the
// controller and ichor-ui, render the install manifest naming them by digest, embed it and push the installer.
func TestReleaseFlowDryRun(t *testing.T) {
	for _, tool := range []string{"crane", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed (run inside nix develop)", tool)
		}
	}

	root := abs(t, repoRoot)
	if _, err := os.Stat(filepath.Join(root, "bin/kustomize")); err != nil {
		t.Skip("bin/kustomize is missing (make kustomize)")
	}

	script := filepath.Join(root, "test/harness/ui-demo/images.sh")
	work := t.TempDir()
	host := startRegistry(t)
	base := host + "/base/static:nonroot"
	pushBase(t, base)

	// The script's relative paths (bin/img, dist, internal/installmanifest) resolve in work.
	write(t, filepath.Join(work, "gobin/go"), fakeGo)

	for _, f := range []string{"manager/manager", "ichor-ui/ichor-ui", "ichor/ichor"} {
		write(t, filepath.Join(work, "bin/img", f), "fake binary "+release+"\n")
	}

	env := []string{
		"VERSION=" + release, "REGISTRY=" + host, "BASE_IMAGE=" + base,
		"PATH=" + filepath.Join(work, "gobin") + ":" + os.Getenv("PATH"),
	}
	digests := filepath.Join(work, "digests.txt")
	step := func(mode string, extra ...string) (string, error) {
		return run(t, work, append(env, extra...), script, mode)
	}

	// 1. The controller and ichor-ui go first; only they are in the digest file.
	if out, err := step("push-core", "DIGEST_FILE="+digests); err != nil {
		t.Fatalf("push-core: %v\n%s", err, out)
	}

	lines := strings.Fields(read(t, digests))
	if len(lines) != 2 || !strings.HasPrefix(lines[0], host+"/maeshinshin/ichor-controller@sha256:") ||
		!strings.HasPrefix(lines[1], host+"/maeshinshin/ichor-ui@sha256:") {
		t.Fatalf("digest file after push-core = %q; want the controller and ichor-ui only", lines)
	}

	for _, l := range lines {
		repo, digest, _ := strings.Cut(l, "@")
		got, err := run(t, work, nil, "crane", "digest", "--insecure", repo+":"+release)
		if err != nil || strings.TrimSpace(got) != digest {
			t.Fatalf("registry digest of %s = %q, %v; want %s", repo, got, err, digest)
		}
	}

	// 2. The manifest, rendered with and without the digests.
	render := func(extra ...string) string {
		t.Helper()

		out := filepath.Join(work, "dist-"+strings.Join(append(extra, "tags"), ""))
		args := append([]string{
			"-o", "manifests", "-o", "generate", "build-installer",
			"IMG=" + imgCtl, "IMG_UI=" + imgUI, "INSTALL_OUT=" + out,
		}, extra...)

		if o, err := run(t, root, nil, "make", args...); err != nil {
			t.Fatalf("make build-installer: %v\n%s", err, o)
		}

		return out
	}

	tagged := render()
	m := read(t, filepath.Join(tagged, "install.yaml"))
	if !strings.Contains(m, "image: "+imgCtl+"\n") || !strings.Contains(m, "image: "+imgUI+"\n") {
		t.Fatal("without IMAGE_DIGESTS the manifest must name the images by tag, as a local build-installer does")
	}

	pinned := render("IMAGE_DIGESTS=" + digests)

	manifest := read(t, filepath.Join(pinned, "install.yaml"))
	for _, l := range lines {
		_, digest, _ := strings.Cut(l, "@")

		img := imgCtl
		if strings.Contains(l, "ichor-ui@") {
			img = imgUI
		}

		if want := "image: " + img + "@" + digest + "\n"; !strings.Contains(manifest, want) {
			t.Errorf("the rendered install manifest lacks %q", want)
		}
	}

	// 3. The installer embeds a manifest that names the digests, and refuses one that does not.
	write(t, filepath.Join(work, "dist/network-policies.yaml"), "# policies\n")
	write(t, filepath.Join(work, "dist/install.yaml"), read(t, filepath.Join(tagged, "install.yaml")))

	out, err := step("build-installer", "IMAGE_DIGESTS="+digests)
	if err == nil || !strings.Contains(out, "does not name") {
		t.Fatalf("build-installer with a tag-only manifest = %q, %v; want a refusal", out, err)
	}

	write(t, filepath.Join(work, "dist/install.yaml"), manifest)

	if out, err := step("build-installer", "IMAGE_DIGESTS="+digests); err != nil {
		t.Fatalf("build-installer: %v\n%s", err, out)
	}

	if got := read(t, filepath.Join(work, "internal/installmanifest/install.yaml")); got != manifest {
		t.Fatal("the installer was not given the digest-pinned manifest")
	}

	// 4. The installer is pushed last and appended to the digest file; the core lines are untouched.
	if out, err := step("push-installer", "DIGEST_FILE="+digests); err != nil {
		t.Fatalf("push-installer: %v\n%s", err, out)
	}

	after := strings.Fields(read(t, digests))
	installer := host + "/maeshinshin/ichor-installer@sha256:"
	if len(after) != 3 || after[0] != lines[0] || after[1] != lines[1] || !strings.HasPrefix(after[2], installer) {
		t.Fatalf("digest file after push-installer = %q", after)
	}
}

// TestChartPinsItsDefaultImages packages the chart with the digests of a release and renders the archive: the
// default controller and ichor-ui images are <repo>:<tag>@<digest>, and the chart in the tree is not touched.
func TestChartPinsItsDefaultImages(t *testing.T) {
	for _, tool := range []string{"helm", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed (run inside nix develop)", tool)
		}
	}

	root := abs(t, repoRoot)
	packed, digests := t.TempDir(), filepath.Join(t.TempDir(), "digests.txt")
	ctl, ui := "sha256:"+hex64, "sha256:"+strings.Repeat("e", 64)

	write(t, digests, live+"/ichor-controller@"+ctl+"\n"+live+"/ichor-ui@"+ui+"\n")

	pkg := func(dist, pins string) (string, error) {
		return run(t, root, nil, "make", "helm-package",
			"CHART_VERSION=0.1.0", "APP_VERSION="+release, "CHART_DIST="+dist, "IMAGE_DIGESTS="+pins)
	}

	if out, err := pkg(packed, digests); err != nil {
		t.Fatalf("make helm-package: %v\n%s", err, out)
	}

	out, err := run(t, root, nil, "helm", "template", "ichor", filepath.Join(packed, "ichor-0.1.0.tgz"))
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}

	for _, want := range []string{`image: "` + imgCtl + "@" + ctl + `"`, `image: "` + imgUI + "@" + ui + `"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the packaged chart lacks %s", want)
		}
	}

	if v := read(t, filepath.Join(root, "dist/chart/values.yaml")); strings.Contains(v, "@sha256:") {
		t.Error("packaging with digests changed dist/chart/values.yaml")
	}

	// A digest file without one of the images is an error, not a chart with a tag.
	only := filepath.Join(t.TempDir(), "only-controller.txt")
	write(t, only, live+"/ichor-controller@"+ctl+"\n")

	if out, err := pkg(t.TempDir(), only); err == nil || !strings.Contains(out, "no digest for ichor-ui") {
		t.Fatalf("helm-package without an ichor-ui digest = %q, %v; want a refusal", out, err)
	}
}

func startRegistry(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	return u.Host
}

// pushBase puts a tiny random image where images.sh's BASE_IMAGE points, in place of the distroless base.
func pushBase(t *testing.T, ref string) {
	t.Helper()

	img, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}

	tag, err := name.NewTag(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}

	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
}
