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
	"encoding/json"
	"reflect"
	"testing"

	"helm.sh/helm/v3/pkg/release"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// asRecord is values as a decoded release record holds them.
func asRecord(t *testing.T, v map[string]any) map[string]any {
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

func TestReadBackRelease_ReproducesTheDeployedValues(t *testing.T) {
	t.Parallel()

	user := map[string]any{
		"hubble": map[string]any{"relay": map[string]any{keyEnabled: true}},
		keyDebug: map[string]any{keyEnabled: true},
	}

	for _, tunnel := range []string{"", tunnelVxlan, tunnelGeneve} {
		for _, kp := range []cni.KubeProxyMode{cni.KubeProxyEnabled, cni.KubeProxyReplaced} {
			for _, u := range []map[string]any{nil, user} {
				deployed, err := EffectiveValues(u, kp, tunnel, 0)
				if err != nil {
					t.Fatal(err)
				}

				rb, err := ReadBackRelease(&release.Release{Config: asRecord(t, deployed)})
				if err != nil {
					t.Fatal(err)
				}

				if rb.KubeProxy != kp || rb.Tunnel != tunnel {
					t.Fatalf("read back %s/%q, installed %s/%q", rb.KubeProxy, rb.Tunnel, kp, tunnel)
				}

				if u == nil && len(rb.User) != 0 {
					t.Fatalf("no user values were set, read back %v", rb.User)
				}

				again, err := EffectiveValues(rb.User, rb.KubeProxy, rb.Tunnel, 0)
				if err != nil {
					t.Fatal(err)
				}

				if !reflect.DeepEqual(asRecord(t, deployed), asRecord(t, again)) {
					t.Fatalf("tunnel %q kube-proxy %s user %v: values differ:\n%v\n%v", tunnel, kp, u, deployed, again)
				}
			}
		}
	}
}
