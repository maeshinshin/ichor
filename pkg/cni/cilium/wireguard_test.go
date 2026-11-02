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

package cilium

import (
	"reflect"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

// TestWireGuardValues checks spec.security.wireGuard: off leaves the effective values
// exactly as they were, on sets the three Cilium 1.18 encryption values and beats a user value.
func TestWireGuardValues(t *testing.T) {
	plain := &ichorv1alpha1.TalosCluster{}

	base, err := EffectiveValuesForMode(plain, cni.KubeProxyReplaced)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("unset and disabled change nothing", func(t *testing.T) {
		c := plain.DeepCopy()
		c.Spec.Security = &ichorv1alpha1.TalosClusterSecurity{
			WireGuard: &ichorv1alpha1.TalosClusterWireGuard{NodeEncryption: true},
		}

		got, err := EffectiveValuesForMode(c, cni.KubeProxyReplaced)
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(got, base) {
			t.Fatal("disabled wireGuard changed the values")
		}

		if _, ok := values.Get(base, "encryption.enabled"); ok {
			t.Fatal("default values set encryption")
		}
	})

	t.Run("enabled sets the encryption values and wins over user values", func(t *testing.T) {
		c := plain.DeepCopy()
		c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{
			Values: &apiextensionsv1.JSON{Raw: []byte(`{"encryption":{"enabled":false,"type":"ipsec"}}`)},
		}
		c.Spec.Security = &ichorv1alpha1.TalosClusterSecurity{
			WireGuard: &ichorv1alpha1.TalosClusterWireGuard{Enabled: true, NodeEncryption: true},
		}

		got, err := EffectiveValuesForMode(c, cni.KubeProxyReplaced)
		if err != nil {
			t.Fatal(err)
		}

		wants := map[string]any{
			"encryption.enabled": true, "encryption.type": "wireguard", "encryption.nodeEncryption": true,
		}

		for path, want := range wants {
			if v, _ := values.Get(got, path); v != want {
				t.Errorf("%s = %v, want %v", path, v, want)
			}
		}
	})
}
