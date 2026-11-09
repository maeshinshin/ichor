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

// Package uidemo tests the release scripts that pin images by digest: hack/pin-image.sh and the chart packaging.
package uidemo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	repoRoot = "../../.."
	release  = "v0.1.0"
	live     = "ghcr.io/maeshinshin"
	imgCtl   = live + "/ichor-controller:" + release
	imgUI    = live + "/ichor-ui:" + release
	hex64    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// run executes a command in dir with the process environment minus make's own flags (a
// `make test` that runs this test would otherwise hand its jobserver to the make started here).
func run(t *testing.T, dir, cmd string, args ...string) (string, error) {
	t.Helper()

	c := exec.Command(cmd, args...)
	c.Dir = dir

	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MAKEFLAGS=") && !strings.HasPrefix(kv, "MFLAGS=") && !strings.HasPrefix(kv, "MAKELEVEL=") {
			c.Env = append(c.Env, kv)
		}
	}

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
			out, err := run(t, t.TempDir(), script, digests, tc.image, tc.ref)
			if err != nil || strings.TrimSpace(out) != tc.want {
				t.Fatalf("pin-image = %q, %v; want %q", out, err, tc.want)
			}
		})
	}

	// An image with no digest is an error, never the tag: a release asked to pin must not fall back.
	out, err := run(t, t.TempDir(), script, digests, "ichor-installer", live+"/ichor-installer:"+release)
	if err == nil || !strings.Contains(out, "no digest for ichor-installer") {
		t.Fatalf("pin-image for an image with no digest = %q, %v; want a refusal", out, err)
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
		return run(t, root, "make", "helm-package",
			"CHART_VERSION=0.1.0", "APP_VERSION="+release, "CHART_DIST="+dist, "IMAGE_DIGESTS="+pins)
	}

	if out, err := pkg(packed, digests); err != nil {
		t.Fatalf("make helm-package: %v\n%s", err, out)
	}

	out, err := run(t, root, "helm", "template", "ichor", filepath.Join(packed, "ichor-0.1.0.tgz"))
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
