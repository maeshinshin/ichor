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
	"slices"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// cniDocs returns the documents of a rendered config whose kind is one of kinds (the CNI-relevant
// ones, ADR 0103 1.1a, ADR 0104 1.6), joined with their separators.
func cniDocs(out string, kinds ...string) string {
	var keep []string

	for d := range strings.SplitSeq(out, "---\n") {
		if slices.ContainsFunc(kinds, func(k string) bool { return strings.Contains(d, "kind: "+k) }) {
			keep = append(keep, d)
		}
	}

	return strings.Join(keep, "---\n")
}

// TestUnmanagedRendersTheLiveCNIConfiguration compares the CNI-relevant output of an unmanaged
// cluster with what a live Talos node (Talos's own flannel, kube-proxy per the recorded value)
// carries, byte for byte.
func TestUnmanagedRendersTheLiveCNIConfiguration(t *testing.T) {
	const (
		flannel   = "apiVersion: v1alpha1\nkind: KubeFlannelCNIConfig\nbackendType: vxlan\nbackendPort: 4789\n"
		proxyHead = "apiVersion: v1alpha1\nkind: KubeProxyConfig\n"
		proxyTail = "image: registry.k8s.io/kube-proxy:v" + constants.DefaultKubernetesVersion + "\nconfig: {}\n"
		proxyOn   = proxyHead + proxyTail
		proxyOff  = proxyHead + "enabled: false\n" + proxyTail
	)

	unmanaged := func(talosCNI, proxy string) mutation {
		return func(c *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine) {
			c.Spec.CNI.Preset = ichorv1alpha1.TalosClusterCNIPresetUnmanaged
			c.Spec.CNI.KubeProxy = proxy
			c.Spec.CNI.Unmanaged = &ichorv1alpha1.TalosClusterCNIUnmanaged{TalosCNI: talosCNI}
		}
	}

	for _, tc := range []struct {
		talosCNI, proxy, want string
	}{
		{"default", "enabled", proxyOn + "---\n" + flannel},
		{"default", "replaced", proxyOff + "---\n" + flannel},
		{"none", "enabled", proxyOn},
		{"none", "replaced", proxyOff},
	} {
		t.Run(tc.talosCNI+"/"+tc.proxy, func(t *testing.T) {
			out := renderFor(t, versionContract, unmanaged(tc.talosCNI, tc.proxy))
			got := cniDocs(out, "KubeFlannelCNIConfig", "KubeProxyConfig")
			if got != tc.want {
				t.Errorf("CNI documents differ from the live fixture:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}

	t.Run("v1.13 default emits no cni block, none emits cni none", func(t *testing.T) {
		requireNone(t, renderFor(t, contractV113, unmanaged("default", "enabled")), legacyCNINone)
		requireAll(t, renderFor(t, contractV113, unmanaged("none", "enabled")), legacyCNINone)
	})

	t.Run("no inline cni entry", func(t *testing.T) {
		out := renderFor(t, versionContract, unmanaged("default", "enabled"))
		requireNone(t, out, "inlineManifests")
	})
}
