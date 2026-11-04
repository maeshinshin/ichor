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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// spec.certSANs, the registry mirror options and install.wipe are the values `ichor adopt` records
// from a foreign cluster (ADR 0103 1.7 class A); each is rendered in the form the contract's own
// generator uses, so a talosctl-made node shows no difference.
func TestRenderClassAValues(t *testing.T) {
	for _, contract := range []string{versionContract, contractV113} {
		t.Run(contract, func(t *testing.T) {
			cluster := testCluster("false")
			cluster.Spec.VersionContract = contract
			cluster.Spec.CertSANs = []string{"10.5.0.2", "cp.example.test"}
			cluster.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{
				{Host: "ghcr.io", Endpoints: []string{"https://m.example.test/p"}, OverridePath: true, SkipFallback: true},
			}

			m := testMachine("cp", "controlplane", nil)
			m.Spec.Install = &ichorv1alpha1.TalosMachineInstall{
				Disk: ichorv1alpha1.TalosMachineDisk{DevPath: "/dev/vda"}, Image: "example.test/installer:v1", Wipe: true,
			}

			out, _, err := Render(cluster, m, testBundle(t), nil, nil)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			got := string(out)

			requireAll(t, got, "- cp.example.test", "kind: RegistryMirrorConfig", "name: ghcr.io",
				"skipFallback: true", "overridePath: true", "wipe: true")

			if contract == versionContract {
				requireAll(t, got, "certExtraSANs")
			}

			requireNone(t, got, "machine.registries", "registries:\n        mirrors")
		})
	}
}
