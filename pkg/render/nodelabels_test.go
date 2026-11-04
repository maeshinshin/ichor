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
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	contractV112 = "v1.12"
	rackOne      = "rack-1"
	zoneKey      = "zone"
)

// TestRenderNodeLabels: TalosMachine.spec.nodeLabels reaches the rendered config under both
// contracts, the machine's labels beat the TalosStorage's on a shared key, and a label the
// kubelet or NodeRestriction would refuse is a render error.
func TestRenderNodeLabels(t *testing.T) {
	bundle := testBundle(t)

	for _, tc := range []struct{ name, contract string }{{"v1.14", ""}, {"v1.12", contractV112}} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := testCluster("true")
			if tc.contract != "" {
				cluster.Spec.VersionContract = tc.contract
			}

			m := testMachine("node-a", roleWorker, map[string]string{zoneKey: rackOne})

			requireAll(t, mustRender(t, cluster, m, bundle, nil), "zone: rack-1")

			// Same key as the preset's label: the machine layer wins over the system layer.
			st := testStorage("longhorn", map[string]string{zoneKey: rackOne})
			sys := &SystemInputs{Storage: []ichorv1alpha1.TalosStorage{st}}
			m = testMachine("node-a", roleWorker, map[string]string{zoneKey: rackOne, longhornDiskLabel: "false"})

			out, _, err := Render(cluster, m, bundle, nil, sys)
			if err != nil {
				t.Fatal(err)
			}

			requireAll(t, string(out), longhornDiskLabel+": \"false\"")
			requireNone(t, string(out), longhornDiskLabel+": \"true\"", longhornDiskLabel+": true")
		})
	}

	t.Run("restricted label is refused", func(t *testing.T) {
		m := testMachine("node-a", roleWorker, map[string]string{"node-role.kubernetes.io/worker": ""})

		_, _, err := Render(testCluster("true"), m, bundle, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "node-role.kubernetes.io/worker") {
			t.Fatalf("want a render error naming the label, got %v", err)
		}
	})
}
