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
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"helm.sh/helm/v3/pkg/release"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// ADR 0104 F3: the spec adopt writes for Ichor's own Cilium reproduces the deployed values, so the
// controller finds nothing to change.
func TestIchorCiliumReadBack_SpecReproducesTheDeployedValues(t *testing.T) {
	user := map[string]any{"debug": map[string]any{"enabled": true}}

	for _, tunnel := range []string{"vxlan", "geneve"} {
		for _, kp := range []cni.KubeProxyMode{cni.KubeProxyEnabled, cni.KubeProxyReplaced} {
			for _, u := range []map[string]any{nil, user} {
				deployed, err := cilium.EffectiveValues(u, kp, tunnel)
				if err != nil {
					t.Fatal(err)
				}

				raw, err := json.Marshal(&release.Release{Name: "cilium", Version: 1, Config: deployed})
				if err != nil {
					t.Fatal(err)
				}

				rec := helmRecord(ichorControllerNamespace)
				rec.Data = map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(raw))}

				target := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(rec).Build()

				rb, err := ichorCiliumReadBack(context.Background(), target)
				if err != nil || rb == nil {
					t.Fatalf("read back: %v, %v", rb, err)
				}

				// What inspect writes for a fresh cluster.
				cluster := &ichorv1alpha1.TalosCluster{}
				cluster.Spec.CNI.Tunnel = rb.Tunnel

				if len(rb.User) > 0 {
					if err := setCiliumUserValues(cluster, rb.User, string(rb.KubeProxy), rb.Tunnel); err != nil {
						t.Fatal(err)
					}
				}

				got, err := cilium.EffectiveValuesForMode(cluster, rb.KubeProxy)
				if err != nil {
					t.Fatal(err)
				}

				if !reflect.DeepEqual(roundTrip(t, got), roundTrip(t, deployed)) {
					t.Fatalf("tunnel %s kube-proxy %s user %v: adopt's values differ from the deployed ones", tunnel, kp, u)
				}
			}
		}
	}
}

func TestIchorCiliumReadBack_NoRecord(t *testing.T) {
	target := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	if rb, err := ichorCiliumReadBack(context.Background(), target); rb != nil || err != nil {
		t.Fatalf("got %v, %v; want nil, nil", rb, err)
	}
}

func roundTrip(t *testing.T, v map[string]any) map[string]any {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}

	return out
}
