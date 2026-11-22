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

// Package e2elabels fails when a Makefile label filter names a label no e2e spec carries:
// such a filter selects nothing and the target silently runs a smaller set than it claims.
package e2elabels

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const repoRoot = "../../.."

var (
	filter = regexp.MustCompile(`-ginkgo\.label-filter="?([^"\n]+)`)
	word   = regexp.MustCompile(`[A-Za-z][\w-]*`)
	label  = regexp.MustCompile(`Label\(([^)]*)\)`)
)

func TestEveryMakefileLabelFilterWordIsOnASpec(t *testing.T) {
	mk, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	onSpec := map[string]bool{}
	err = filepath.WalkDir(filepath.Join(repoRoot, "test/e2e"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		src, err := os.ReadFile(p)
		for _, m := range label.FindAllStringSubmatch(string(src), -1) {
			for _, w := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
				onSpec[w[1]] = true
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	filters := filter.FindAllStringSubmatch(string(mk), -1)
	if len(filters) == 0 {
		t.Fatal("no -ginkgo.label-filter found in the Makefile: this check has gone empty")
	}
	for _, f := range filters {
		for _, w := range word.FindAllString(f[1], -1) {
			if !onSpec[w] {
				t.Errorf("Makefile label filter %q names label %q, which no spec in test/e2e carries", f[1], w)
			}
		}
	}
}
