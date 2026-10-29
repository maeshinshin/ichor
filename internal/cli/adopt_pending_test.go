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
	"encoding/base64"
	"encoding/json"
	"testing"

	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

func ichorRecordWithStatus(t *testing.T, status release.Status) *corev1.Secret {
	t.Helper()

	cfg, err := cilium.EffectiveValues(nil, cni.KubeProxyEnabled, tunnelGeneve)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(&release.Release{
		Name: string(detectedCilium), Version: 1, Config: cfg, Info: &release.Info{Status: status},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := helmRecord("ichor-system")
	rec.Data = map[string][]byte{helmReleaseKey: []byte(base64.StdEncoding.EncodeToString(raw))}

	return rec
}

// A rerun after a killed Cilium install: Ichor's own record is pending-*, so adopt runs the same
// Cilium step as on a fresh cluster (which repairs it exclusively); a deployed record is left alone.
func TestAdopt_RerunRepairsIchorsPendingCiliumRecord(t *testing.T) {
	for name, tc := range map[string]struct {
		status release.Status
		calls  int
	}{
		"pending-install": {release.StatusPendingInstall, 1},
		"pending-upgrade": {release.StatusPendingUpgrade, 1},
		"deployed":        {release.StatusDeployed, 0},
	} {
		t.Run(name, func(t *testing.T) {
			f := newNoCNIFlow(t)

			node := func(name, cidr string) *corev1.Node { return cniNode(name, cidr, true, "") }
			f.deps.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(
				node("cp", "10.244.0.0/24"), node("wk", nodeCIDR), testDS("cilium", ciliumImage),
				ichorRecordWithStatus(t, tc.status), ciliumCM("false"),
			).Build()

			if err := f.run(t); err != nil {
				t.Fatalf("adopt: %v\n%s", err, f.env.stderr.String())
			}

			calls := f.calls()
			if len(calls) != tc.calls {
				t.Fatalf("Cilium steps = %d, want %d\n%s", len(calls), tc.calls, f.env.stderr.String())
			}

			if tc.calls == 1 && calls[0].mode != cni.KubeProxyEnabled {
				t.Errorf("mode = %s, want the record's", calls[0].mode)
			}
		})
	}
}
