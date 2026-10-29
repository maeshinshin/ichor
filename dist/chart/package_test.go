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

package chart

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackagedChart packages the chart the way the release workflow does (--version and
// --app-version), then lints and renders the archive, so what is
// pushed to GHCR is what the tests cover. The default images must be the published ones,
// tagged with the appVersion.
func TestPackagedChart(t *testing.T) {
	dir := t.TempDir()

	pkg := exec.Command("helm", "package", ".", "--version", "9.8.7", "--app-version", "v9.8.7", "--destination", dir)
	if out, err := pkg.CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v\n%s", err, out)
	}

	archive := filepath.Join(dir, "ichor-9.8.7.tgz")

	if out, err := exec.Command("helm", "lint", archive).CombinedOutput(); err != nil {
		t.Fatalf("helm lint %s: %v\n%s", archive, err, out)
	}

	out, err := exec.Command("helm", "template", "ichor", archive).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template %s: %v\n%s", archive, err, out)
	}

	for _, want := range []string{
		`image: "ghcr.io/maeshinshin/ichor-controller:v9.8.7"`,
		`image: "ghcr.io/maeshinshin/ichor-ui:v9.8.7"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("packaged chart render lacks %s", want)
		}
	}
}
