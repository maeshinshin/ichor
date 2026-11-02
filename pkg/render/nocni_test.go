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
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// TestNoCNIAdoptedClusterRendersTheLiveConfiguration is the render side of ADR 0104 1.6. A cluster
// adopted with no CNI has, live, no CNI selected (`cluster.network.cni.name: none`, or from the
// v1.14 contract no KubeFlannelCNIConfig document), no inline Cilium entry, KubePrism on and
// kube-proxy as Talos has it. Once adopt has installed Cilium and the hand-over set CNIHandover=True,
// the cilium preset must render exactly those paths, so no class C drift appears.
func TestNoCNIAdoptedClusterRendersTheLiveConfiguration(t *testing.T) {
	handedOver := func(proxy string) mutation {
		return func(c *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine) {
			c.Spec.CNI.Preset = "cilium"
			c.Spec.CNI.KubeProxy = proxy
			apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
				Type: ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: "Test", Message: "test",
			})
		}
	}

	const (
		prism     = "apiVersion: v1alpha1\nkind: KubePrismConfig\nport: 7445\n"
		proxyHead = "apiVersion: v1alpha1\nkind: KubeProxyConfig\n"
		proxyTail = "image: registry.k8s.io/kube-proxy:v" + constants.DefaultKubernetesVersion + "\nconfig: {}\n"
	)

	proxyOn, proxyOff := string(cni.KubeProxyEnabled), string(cni.KubeProxyReplaced)

	for proxy, want := range map[string]string{
		proxyOn:  prism + "---\n" + proxyHead + proxyTail,
		proxyOff: prism + "---\n" + proxyHead + "enabled: false\n" + proxyTail,
	} {
		t.Run("v1.14/"+proxy, func(t *testing.T) {
			out := renderFor(t, versionContract, handedOver(proxy))

			if got := cniDocs(out, "KubeFlannelCNIConfig", "KubeProxyConfig", "KubePrismConfig"); got != want {
				t.Errorf("CNI documents differ from the live no-CNI fixture:\n got: %q\nwant: %q", got, want)
			}

			requireNone(t, out, "KubeFlannelCNIConfig", "inlineManifests", "cilium")
		})
	}

	t.Run("v1.13 selects cni none", func(t *testing.T) {
		out := renderFor(t, contractV113, handedOver(proxyOn))

		requireAll(t, out, legacyCNINone)
		requireNone(t, out, "KubeFlannelCNIConfig", "inlineManifests", "cilium")
	})
}
