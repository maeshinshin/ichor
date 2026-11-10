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

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

func resolverPatch(name string, addrs ...string) ichorv1alpha1.TalosConfigPatch {
	r := &typedconfig.ResolverConfigV1Alpha1{}
	for _, a := range addrs {
		r.ResolverNameservers = append(r.ResolverNameservers, &typedconfig.NameserverConfig{Address: a})
	}

	return ichorv1alpha1.TalosConfigPatch{
		Namespace: testNamespace, Name: name,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
			Layer:      layerCluster,
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: "ResolverConfig", ResolverConfig: r}},
		},
	}
}

// A patch that sets nameservers renders a valid config carrying them.
func TestResolverConfigPatchRenders(t *testing.T) {
	out := mustRender(t, testCluster("false"), testMachine("cp1", roleControlPlane, nil), testBundle(t),
		[]ichorv1alpha1.TalosConfigPatch{resolverPatch("dns", "192.168.1.1", "9.9.9.9")})

	for _, want := range []string{"kind: ResolverConfig", "192.168.1.1", "9.9.9.9"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered config lacks %q:\n%s", want, out)
		}
	}
}

// A machine whose spec already renders a ResolverConfig refuses a patch that carries one;
// without spec.network.dnsServers the same patch is fine.
func TestResolverConfigPatchConflictsWithSpecDNSServers(t *testing.T) {
	m := testMachine("cp1", roleControlPlane, nil)
	m.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{
		MAC: testNICMAC, Addressing: "dhcp", DNSServers: []string{"1.1.1.1"},
	}

	_, _, err := Render(testCluster("false"), m, testBundle(t),
		[]ichorv1alpha1.TalosConfigPatch{resolverPatch("dns", "9.9.9.9")}, nil)
	if err == nil || !strings.Contains(err.Error(), "spec.network.dnsServers") {
		t.Fatalf("want a dnsServers conflict error, got %v", err)
	}

	m.Spec.Network.DNSServers = nil
	if _, _, err := Render(testCluster("false"), m, testBundle(t),
		[]ichorv1alpha1.TalosConfigPatch{resolverPatch("dns", "9.9.9.9")}, nil); err != nil {
		t.Fatalf("patch without dnsServers in the spec: %v", err)
	}
}
