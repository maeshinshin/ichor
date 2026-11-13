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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The preflight reads Nodes with lookup, which is empty under "helm template", and
// a server-side dry run needs a cluster. So the default render must pass (the check is skipped),
// and the condition itself is tested by rendering the helper over fixture Node lists in a copy of
// the chart. The fail() call is not reachable offline; its text is checked in the source.

func TestPreflightSkippedWithoutACluster(t *testing.T) {
	renderChart(t, "ichor")
	renderChartWithValues(t, "ichor", "preflight.cni=false")
}

const noCNIMessage = `{type: Ready, status: "False", reason: KubeletNotReady, ` +
	`message: "NetworkReady=false reason:NetworkPluginNotReady message:cni plugin not initialized"}`

func TestPreflightCondition(t *testing.T) {
	ready := `{type: Ready, status: "True", reason: KubeletReady}`
	diskPressure := `{type: Ready, status: "False", reason: KubeletNotReady, message: "disk pressure"}`

	cases := []struct {
		name, items, want string
	}{
		{"every node has no pod network", nodes(noCNIMessage, noCNIMessage), "true"},
		{"one node is Ready", nodes(noCNIMessage, ready), ""},
		{"NotReady for another reason", nodes(noCNIMessage, diskPressure), ""},
		{"a node with no status", "[{metadata: {name: a}}]", ""},
		{"no nodes", "[]", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyChart(t)
			write(t, filepath.Join(dir, "fixture.yaml"), "items: "+tc.items+"\n")
			write(t, filepath.Join(dir, "templates", "zz-test.yaml"),
				"result: \"{{ include \"ichor.preflight.noCNI\" (fromYaml (.Files.Get \"fixture.yaml\")).items }}\"\n")

			out, err := exec.Command("helm", "template", "t", dir, "-s", "templates/zz-test.yaml").CombinedOutput()
			if err != nil {
				t.Fatalf("helm template: %v\n%s", err, out)
			}

			if want := "result: \"" + tc.want + "\""; !strings.Contains(string(out), want) {
				t.Fatalf("got\n%s\nwant %s", out, want)
			}
		})
	}
}

func TestPreflightMessageNamesTheCommand(t *testing.T) {
	src, err := os.ReadFile("templates/_preflight.tpl")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"ichor bootstrap cni", "preflight.cni=false"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("_preflight.tpl: the failure message must mention %q", want)
		}
	}
}

// nodes is a Node list whose nodes each carry one condition.
func nodes(conditions ...string) string {
	items := make([]string, 0, len(conditions))
	for _, c := range conditions {
		items = append(items, "{status: {conditions: ["+c+"]}}")
	}

	return "[" + strings.Join(items, ", ") + "]"
}

// copyChart copies this directory's chart (not the tests) to a temp dir.
func copyChart(t *testing.T) string {
	t.Helper()

	dst := t.TempDir()

	out, err := exec.Command("cp", "-r", "Chart.yaml", "values.yaml", "templates", dst).CombinedOutput()
	if err != nil {
		t.Fatalf("copying the chart: %v\n%s", err, out)
	}

	return dst
}

func write(t *testing.T, path, text string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
