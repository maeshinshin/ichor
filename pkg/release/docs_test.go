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

package release

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docsRoot is the user-facing documentation. The ADRs, the design notes and the generated reference
// are left out: they record what was true when they were written.
const docsRoot = "../../docs"

var docsSkipped = map[string]bool{"adr": true, "design": true, "reference": true}

// statedVersion finds "Talos v1.14.5", "Kubernetes v1.36.6" and "Cilium 1.20.4", also across a line
// break or in aligned columns.
var statedVersion = regexp.MustCompile(`\b(Talos|Kubernetes|Cilium)(?: chart)?\s+v?(\d+\.\d+\.\d+)\b`)

// notThePin names the statements that deliberately show another version: a node's own Talos version
// in a sample output and the from-version of an upgrade example. Each is "<path under docs> <label> <version>".
var notThePin = map[string]bool{
	"guides/reclaim-a-machine.md Talos 1.13.2":                 true,
	"guides/getting-started/check-the-cluster.md Talos 1.14.3": true,
}

// A guide that restates a pinned version in prose must agree with Pinned, or it tells a reader to
// install a release this build no longer installs.
func TestDocsStateThePinnedVersions(t *testing.T) {
	t.Parallel()

	pinned := map[string]string{
		"Talos":      strings.TrimPrefix(Pinned.Talos, "v"),
		"Kubernetes": strings.TrimPrefix(Pinned.Kubernetes, "v"),
		"Cilium":     Pinned.Cilium,
	}

	err := filepath.WalkDir(docsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(docsRoot, path)
		if d.IsDir() && docsSkipped[rel] {
			return filepath.SkipDir
		}

		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		for _, m := range statedVersion.FindAllStringSubmatch(string(body), -1) {
			if m[2] != pinned[m[1]] && !notThePin[rel+" "+m[1]+" "+m[2]] {
				t.Errorf("docs/%s says %s %s, pkg/release pins %s", rel, m[1], m[2], pinned[m[1]])
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
