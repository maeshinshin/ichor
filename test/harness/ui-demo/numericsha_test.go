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

package uidemo

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/yaml"
)

// A git sha is hex, so one can look like a number: 9794517 is an int to YAML, 1234e56 a float, and 0123456 is a
// numeric semver identifier with a leading zero. These shas are fed to every place that writes one into a typed field.
var numericShas = []string{"9794517", "1234e56", "0123456"}

// gitShim puts a `git` first on PATH that answers `rev-parse --verify` with the full sha and `rev-parse --short` with
// the short one, and hands every other call to the real git. It returns the environment to run a script with.
func gitShim(t *testing.T, short string) []string {
	t.Helper()

	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	write(t, filepath.Join(dir, "git"), "#!/bin/sh\n"+
		"case \"$*\" in\n"+
		"  *--verify*) echo "+short+strings.Repeat("0", 33)+" ;;\n"+
		"  *--short*) echo "+short+" ;;\n"+
		"  *) exec "+real+" \"$@\" ;;\n"+
		"esac\n")

	return append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
}

func runEnv(t *testing.T, env []string, dir, cmd string, args ...string) (string, error) {
	t.Helper()

	c := exec.Command(cmd, args...)
	c.Dir, c.Env = dir, env
	out, err := c.CombinedOutput()

	return string(out), err
}

// TestClusterGateLabelsSurviveANumericSha renders the gate's Job and parses it as the API server does: a label value
// that YAML reads as a number does not unmarshal into a map[string]string.
func TestClusterGateLabelsSurviveANumericSha(t *testing.T) {
	root := abs(t, repoRoot)

	for _, short := range numericShas {
		t.Run(short, func(t *testing.T) {
			out, err := runEnv(t, append(gitShim(t, short), "GATE_RENDER=1"), root, "hack/cluster-gate.sh")
			if err != nil {
				t.Fatalf("cluster-gate.sh: %v\n%s", err, out)
			}

			_, job, ok := strings.Cut(out, "\n---\n")
			if !ok {
				t.Fatalf("no Job after the claim in:\n%s", out)
			}

			// kubectl converts YAML to JSON with no target type. sigs.k8s.io/yaml's Unmarshal would
			// coerce a number to the string the field wants, and hide the bug.
			raw, err := yaml.YAMLToJSON([]byte(job))
			if err != nil {
				t.Fatal(err)
			}

			var j batchv1.Job
			if err := json.Unmarshal(raw, &j); err != nil {
				t.Fatalf("the Job does not parse for sha %s: %v", short, err)
			}

			for _, labels := range []map[string]string{j.Labels, j.Spec.Template.Labels} {
				if labels["ichor.gate/sha"] != short {
					t.Errorf("ichor.gate/sha = %q, want %q", labels["ichor.gate/sha"], short)
				}
			}
		})
	}
}

// TestDemoVersionIsSemverForANumericSha runs the version recipe the demo README gives and requires a valid semantic
// version: a numeric prerelease identifier with a leading zero is not one, which is why the sha follows a "g".
func TestDemoVersionIsSemverForANumericSha(t *testing.T) {
	var recipe string

	for line := range strings.SplitSeq(read(t, "README.md"), "\n") {
		if strings.HasPrefix(line, "V=") {
			recipe, _, _ = strings.Cut(line, "   #")
		}
	}

	if recipe == "" {
		t.Fatal("README.md has no V= line giving the demo version")
	}

	for _, short := range numericShas {
		t.Run(short, func(t *testing.T) {
			out, err := runEnv(t, gitShim(t, short), t.TempDir(), "bash", "-c", recipe+"; echo $V")
			if v := strings.TrimSpace(out); err != nil || !semver.IsValid(v) {
				t.Fatalf("demo version %q (%v) is not a valid semantic version for sha %s", v, err, short)
			}
		})
	}
}

// shaSites are the tracked files that derive a short sha. A file that does so and is not listed here fails
// TestEveryShaSiteIsCovered, so a new one cannot land unexamined: cover it with a test above or explain why it is safe.
var shaSites = map[string]string{
	"hack/cluster-gate.sh":           "labels the Job: TestClusterGateLabelsSurviveANumericSha",
	"hack/remote-gate.sh":            "only echoed in a log line, never written into a typed field",
	"test/harness/ui-demo/README.md": "the demo version: TestDemoVersionIsSemverForANumericSha",
}

// shaDerivation is the usual ways to shorten a sha: rev-parse --short, ${X:0:7}, --abbrev, git log --format=%h and
// cut -c1-7. It does not catch every spelling (head -c7, awk substr, a Go slice in a script's generator): when a
// new way to shorten a sha lands, widen it.
var shaDerivation = regexp.MustCompile(
	`rev-parse.*--short|--short.*rev-parse|\$\{[A-Za-z_]*:0:[0-9]+\}|--abbrev|` +
		`--(?:format|pretty)[= ]['"]?%h|\bcut +-c *[0-9]*-[0-9]+`)

func TestEveryShaSiteIsCovered(t *testing.T) {
	ls := exec.Command("git", "ls-files", "-z")
	ls.Dir = abs(t, repoRoot)

	out, err := ls.Output()
	if err != nil {
		t.Fatal(err)
	}

	for name := range strings.SplitSeq(string(out), "\x00") {
		// Scripts, workflows, Makefiles and READMEs build versions and labels; ADRs and the changelog only describe.
		prose := strings.HasPrefix(name, "docs/adr/") || name == "CHANGELOG.md" || strings.HasPrefix(name, "site/")
		if name == "" || prose || strings.HasSuffix(name, "_test.go") {
			continue
		}

		src, err := os.ReadFile(filepath.Join(abs(t, repoRoot), name))
		if err != nil {
			continue // a tracked file deleted in the working tree
		}

		if bytes.IndexByte(src, 0) >= 0 {
			continue // a binary file
		}

		if loc := shaDerivation.FindIndex(src); loc != nil && shaSites[name] == "" {
			t.Errorf("%s derives a short git sha (%q) and is not in shaSites: a sha can be all digits (a YAML "+
				"number) or start with 0 (invalid semver); cover it with a test", name, src[loc[0]:loc[1]])
		}
	}
}
