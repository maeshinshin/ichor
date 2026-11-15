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

package suppressions

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const repoRoot = "../../.."

// hiddenFailure matches the ways a Makefile or shell script throws a failure away: a status forced to success
// (|| true, || :, set +e), stderr sent to /dev/null (which drops the diagnosis), and in a Makefile a "-" prefix or
// .IGNORE (the recipe goes on whatever the command returned). A pipeline whose last command masks the first, and
// $(shell ...) whose status make ignores, are not matched: widen it when one of those bites.
var hiddenFailure = regexp.MustCompile(
	`\|\|\s*(?:true|:)(?:[\s;)"]|$)|2>\s*/dev/null|&>\s*/dev/null|>\s*/dev/null\s+2>&1|\bset\s+\+e\b`)

// makeIgnore is the Makefile-only forms: a recipe line prefixed "-" (after any "@" or "+"), and .IGNORE.
var makeIgnore = regexp.MustCompile(`^\t[@+]*-[^-]|^\.IGNORE\b`)

var trailingReason = regexp.MustCompile(`(?:^|\s)#\s*\S`)

// isComment reports whether a line is a shell or make comment, including a recipe line that is only "@# text".
func isComment(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, "\t @+-"), "#")
}

// explained reports whether the match at lines[i] carries its reason: a "# why" after it on the same line, or a comment
// on the line directly above it. A suppression inside a backslash-continued Makefile recipe has no such line, so
// it is not explainable there: let it fail, or write it as its own command.
func explained(lines []string, i, end int) bool {
	if trailingReason.MatchString(lines[i][end:]) {
		return true
	}

	return i > 0 && isComment(lines[i-1])
}

// TestEverySuppressedFailureIsExplained fails when the Makefile, an included *.mk or a hack/ script discards a failure
// without a comment saying why. A suppression with a stated reason is allowed; one without is how `make install`
// hid a failed `kustomize build` behind `2>/dev/null || true` until someone noticed nothing had been installed.
func TestEverySuppressedFailureIsExplained(t *testing.T) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}

	ls := exec.Command("git", "ls-files", "-z", "Makefile", "*.mk", "hack/*.sh")
	ls.Dir = root

	out, err := ls.Output()
	if err != nil {
		t.Fatal(err)
	}

	checked := 0

	for name := range strings.SplitSeq(string(out), "\x00") {
		src, err := os.ReadFile(filepath.Join(root, name))
		if name == "" || err != nil {
			continue // a tracked file deleted in the working tree
		}

		checked++
		lines := strings.Split(string(src), "\n")
		isMake := filepath.Base(name) == "Makefile" || filepath.Ext(name) == ".mk"

		for i, line := range lines {
			if isComment(line) {
				continue
			}

			found := hiddenFailure.FindAllStringIndex(line, -1)
			if isMake && makeIgnore.MatchString(line) {
				found = append(found, []int{0, 0})
			}

			for _, loc := range found {
				if !explained(lines, i, loc[1]) {
					t.Errorf("%s:%d discards a failure and does not say why: %s\n\tlet it fail, or put a one-line "+
						"comment with the reason on the line above (or after it)", name, i+1, strings.TrimSpace(line))

					break
				}
			}
		}
	}

	if checked < 4 {
		t.Fatalf("only %d files checked: is git ls-files finding the Makefile and hack/?", checked)
	}
}
