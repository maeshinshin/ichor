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

// Control planes without Talos's default exclude-from-external-load-balancers label are adopted as
// they are: adding it would take them out of Service type=LoadBalancer membership. adopt records
// spec.controlPlane.omitDefaultNodeLabels instead of blocking (ADR 0144).
const (
	fixtureV113 = "v113"
	fixtureV114 = "v114"
	talosV113   = "v1.13.0"
)

func TestAdoptKeepsMissingControlPlaneLabels(t *testing.T) {
	const (
		legacyLabels = "    nodeLabels:\n        node.kubernetes.io/exclude-from-external-load-balancers: \"\"\n"
		docLabels    = "labels:\n    node-role.kubernetes.io/control-plane: \"\"\n    node.kubernetes.io/exclude-from-external-load-balancers: \"\"\n"
	)

	for _, tc := range []struct {
		name, fixture, version, labels, without string
		wantOmit                                bool
	}{
		{"v1.14 without the labels", fixtureV114, ver1143, docLabels, "labels: {}\n", true},
		{"v1.13 without machine.nodeLabels", fixtureV113, talosV113, legacyLabels, "", true},
		{"v1.14 with the labels", fixtureV114, ver1143, docLabels, docLabels, false},
		{"v1.13 with the labels", fixtureV113, talosV113, legacyLabels, legacyLabels, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp, wk := classAFixture(t, tc.fixture)
			if !strings.Contains(string(cp), tc.labels) {
				t.Fatalf("the fixture lacks the label block %q", tc.labels)
			}

			cp = []byte(strings.Replace(string(cp), tc.labels, tc.without, 1))

			env, deps := classAFlow(t, tc.version, cp, wk)

			out := planOutput(t, env, deps, false)
			if strings.Contains(out, "cannot patch automatically") || strings.Contains(out, "exclude-from-external-load-balancers") {
				t.Fatalf("the plan blocks or mentions the label:\n%s", out)
			}

			deps.PlanOnly, deps.AssumeYes = false, true
			_ = runAdopt(context.Background(), "d1", testNamespace, deps)

			var cluster ichorv1alpha1.TalosCluster
			if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "d1"}, &cluster); err != nil {
				t.Fatal(err)
			}

			if got := cluster.Spec.ControlPlane.OmitDefaultNodeLabels; got != tc.wantOmit {
				t.Errorf("spec.controlPlane.omitDefaultNodeLabels = %v, want %v", got, tc.wantOmit)
			}

			// A rerun renders from the written spec: both machines match.
			deps.PlanOnly, deps.AssumeYes = true, false
			env.stderr.Reset()

			if again := planOutput(t, env, deps, false); strings.Count(again, "already matches a fresh render") != 2 {
				t.Errorf("the rerun plan does not match both machines:\n%s", again)
			}
		})
	}
}
