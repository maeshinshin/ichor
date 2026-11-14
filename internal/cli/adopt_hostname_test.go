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
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const generatedAuto = "auto: stable"

// A node with a static hostname (HostnameConfig requires `auto: off` beside it) was blocked on
// HostnameConfig.auto and HostnameConfig.hostname because adopt left spec.hostname empty. The
// fixture is the v1.13 control plane `talosctl gen config` writes, with the hostname edited in.
func TestAdoptTakesTheNodesHostnameIntoTheSpec(t *testing.T) {
	cases := []struct {
		name, hostnameDoc string
		want              string
		blocked           bool
		warning           string
	}{
		{"static hostname", "auto: off\nhostname: node-1.example.test", "node-1.example.test", false, ""},
		{"no hostname document value", generatedAuto, "", false, ""},
		{"hostname that is not RFC 1123", "auto: off\nhostname: Node_1", "", true, `its hostname "Node_1" is not an RFC 1123 name`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp, wk := classAFixture(t, "v114")
			cp = []byte(strings.Replace(string(cp), generatedAuto, tc.hostnameDoc, 1))
			wk = []byte(strings.Replace(string(wk), generatedAuto, tc.hostnameDoc, 1))

			env, deps := classAFlow(t, "v1.14.3", cp, wk)

			out := planOutput(t, env, deps, tc.blocked)
			if tc.blocked != strings.Contains(out, "HostnameConfig.hostname") {
				t.Fatalf("blocked = %v but the plan said:\n%s", tc.blocked, out)
			}

			if !strings.Contains(out, tc.warning) {
				t.Fatalf("the plan lacks %q:\n%s", tc.warning, out)
			}

			if tc.blocked {
				return
			}

			deps.PlanOnly, deps.AssumeYes = false, true
			if err := runAdopt(context.Background(), "d1", testNamespace, deps); err == nil || !strings.Contains(err.Error(), "did not become Ready") {
				t.Fatalf("runAdopt = %v, want the Ready timeout\n%s", err, env.stderr.String())
			}

			var m ichorv1alpha1.TalosMachine
			if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "uuid-" + flowCPAddr}, &m); err != nil {
				t.Fatal(err)
			}

			if m.Spec.Hostname != tc.want {
				t.Errorf("spec.hostname = %q, want %q", m.Spec.Hostname, tc.want)
			}

			// A rerun renders from the written spec: the hostname matches, nothing is left to patch.
			deps.PlanOnly, deps.AssumeYes = true, false
			env.stderr.Reset()

			if again := planOutput(t, env, deps, false); strings.Count(again, "already matches a fresh render") != 2 {
				t.Errorf("the rerun plan does not match both machines:\n%s", again)
			}
		})
	}
}

// The legacy form states the hostname in machine.network.hostname; the facts read either.
func TestReadTalosFactsHostname(t *testing.T) {
	legacy := "version: v1alpha1\nmachine:\n  type: worker\n  network:\n    hostname: legacy-1\n"
	doc := "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: doc-1\n"

	for name, tc := range map[string]struct{ cfg, want string }{
		"legacy": {legacy, "legacy-1"}, "document": {"version: v1alpha1\nmachine:\n  type: worker\n---\n" + doc, "doc-1"}, "absent": {"version: v1alpha1\nmachine:\n  type: worker\n", ""},
	} {
		f, err := ReadTalosFacts([]byte(tc.cfg))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if f.Hostname != tc.want {
			t.Errorf("%s: hostname = %q, want %q", name, f.Hostname, tc.want)
		}
	}
}
