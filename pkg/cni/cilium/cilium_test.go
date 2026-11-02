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
	"context"
	"fmt"
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

func TestTalosPatches(t *testing.T) {
	p := New()

	patches, err := p.TalosPatches(cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("TalosPatches(enabled): %v", err)
	}
	if patches != nil {
		t.Fatalf("TalosPatches(enabled) = %v, want nil: kube-proxy enabled is Talos's own default", patches)
	}

	patches, err = p.TalosPatches(cni.KubeProxyReplaced)
	if err != nil {
		t.Fatalf("TalosPatches(replaced): %v", err)
	}
	if patches != nil {
		t.Fatalf("TalosPatches(replaced) = %v, want nil: pkg/render owns the kube-proxy document", patches)
	}
}

func TestCheckConsistency(t *testing.T) {
	p := New()

	cases := []struct {
		name    string
		live    cni.LiveConfig
		mode    cni.KubeProxyMode
		wantErr bool
	}{
		{"enabled matches", cni.LiveConfig{KubeProxyReplacement: false}, cni.KubeProxyEnabled, false},
		{"replaced matches", cni.LiveConfig{KubeProxyReplacement: true}, cni.KubeProxyReplaced, false},
		{
			"declares replaced but live still has kube-proxy",
			cni.LiveConfig{KubeProxyReplacement: false}, cni.KubeProxyReplaced, true,
		},
		{
			"declares enabled but live already replaced it",
			cni.LiveConfig{KubeProxyReplacement: true}, cni.KubeProxyEnabled, true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.CheckConsistency(context.Background(), tc.live, tc.mode)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckConsistency(%+v, %s) error = %v, wantErr %v", tc.live, tc.mode, err, tc.wantErr)
			}
		})
	}
}

// TestValues_TalosRequiredFive pins the five settings Cilium needs on Talos
// (ADR 0055): ipam, kubeProxyReplacement, securityContext.capabilities,
// cgroup, and KubePrism's k8sServiceHost/Port.
func TestValues_TalosRequiredFive(t *testing.T) {
	out, err := sigsyaml.Marshal(New().valuesFor(cni.KubeProxyReplaced))
	if err != nil {
		t.Fatalf("marshalling values: %v", err)
	}
	for _, want := range []string{
		"mode: kubernetes", "kubeProxyReplacement: true", "ciliumAgent:", "cleanCiliumState:", "SYS_ADMIN",
		"enabled: false", "hostRoot: /sys/fs/cgroup", "k8sServiceHost: localhost", "k8sServicePort: 7445",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("values missing %q:\n%s", want, out)
		}
	}
}

// TestValues_HubbleCertgenToleratesControlPlane: the hubble-generate-certs Job has to run on a tainted
// control plane, or the agents wait for its secrets and a preset with tainted control planes never
// finishes its hand-over before a worker is approved. The chart's default (no tolerations) would not.
func TestValues_HubbleCertgenToleratesControlPlane(t *testing.T) {
	got, err := EffectiveValues(nil, cni.KubeProxyEnabled, "")
	if err != nil {
		t.Fatal(err)
	}

	tols, _ := values.Get(got, "certgen.tolerations")
	if !strings.Contains(fmt.Sprint(tols), "node-role.kubernetes.io/control-plane") {
		t.Errorf("certgen.tolerations = %v, want the control-plane taint tolerated", tols)
	}
}
