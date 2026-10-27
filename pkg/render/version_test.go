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

package render

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"go.yaml.in/yaml/v4"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// pinnedVersion and pinnedFingerprint hold Version and the output fingerprint it was last bumped
// for. They move together: a render change updates the fingerprint and bumps Version (ADR 0088).
const (
	pinnedVersion     = 1
	pinnedFingerprint = "sha256:77b19bf738859cec40165ecf68a8dec26fcd5d7e51b2f0deb0e37f13342b6118"
)

// leaves flattens a YAML stream into "doc<n>.path" -> value.
func leaves(t *testing.T, raw []byte) map[string]string {
	t.Helper()

	out := map[string]string{}
	dec := yaml.NewDecoder(bytes.NewReader(raw))

	for n := 0; ; n++ {
		var doc any

		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}

			t.Fatalf("decoding rendered configuration: %v", err)
		}

		var walk func(path string, v any)
		walk = func(path string, v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, c := range x {
					walk(path+"."+k, c)
				}
			case []any:
				for i, c := range x {
					walk(fmt.Sprintf("%s[%d]", path, i), c)
				}
			default:
				out[fmt.Sprintf("doc%d%s", n, path)] = fmt.Sprint(x)
			}
		}

		walk("", doc)
	}
}

// renderFingerprint renders the same inputs under two different secrets bundles and hashes every
// key path, keeping a value only where both renders agree -- so secret material drops out and
// anything Ichor itself decides stays in.
func renderFingerprint(t *testing.T) string {
	t.Helper()

	other, err := secrets.NewBundle(secrets.NewFixedClock(time.Now().Add(time.Hour)), testContract(t))
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}

	var lines []string

	for _, role := range []string{roleControlPlane, roleWorker} {
		cluster := testCluster("false")
		m := testMachine("node-"+role, role, map[string]string{labelEnv: "fingerprint"})
		m.Spec.Hostname = "node-" + role
		patches := []ichorv1alpha1.TalosConfigPatch{sysctlPatch("p", layerCluster, 0, nil, "1")}

		a := leaves(t, []byte(mustRender(t, cluster, m, testBundle(t), patches)))
		b := leaves(t, []byte(mustRender(t, cluster, m, other, patches)))

		for k, v := range a {
			if b[k] != v {
				v = "<varies>"
			}

			lines = append(lines, role+":"+k+"="+v)
		}
	}

	slices.Sort(lines)

	return Hash([]byte(strings.Join(lines, "\n")))
}

// TestRenderVersionMatchesOutput fails when Render's output for fixed inputs changes without
// Version being bumped (and the pin above updated). It is what keeps an Ichor upgrade that
// changes rendering from being reported as node drift.
func TestRenderVersionMatchesOutput(t *testing.T) {
	got := renderFingerprint(t)

	if Version != pinnedVersion || got != pinnedFingerprint {
		t.Fatalf("render output or Version changed: Version=%d (pinned %d), fingerprint=%s (pinned %s).\n"+
			"If the output changed on purpose, bump Version in version.go and set pinnedVersion and "+
			"pinnedFingerprint here; if Version alone changed, set the pin to match.",
			Version, pinnedVersion, got, pinnedFingerprint)
	}
}
