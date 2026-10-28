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
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// injectHostname renders spec.hostname, the name the controller uses for the Kubernetes node and
// the etcd member, so the node must actually carry it. It is the machine layer, applied over
// the patches. From the v1.14 contract it is the HostnameConfig document, with the generator's
// `auto: stable` turned off (a static hostname and an automatic one cannot coexist); before
// it, machine.network.hostname. An empty spec.hostname leaves the generator's default alone.
func injectHostname(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, m *ichorv1alpha1.TalosMachine,
) (coreconfig.Provider, error) {
	if m.Spec.Hostname == "" {
		return cfg, nil
	}

	if contract.MultidocNetworkConfigSupported() {
		doc := network.NewHostnameConfigV1Alpha1()
		doc.ConfigAuto = new(nethelpers.AutoHostnameKindOff)
		doc.ConfigHostname = m.Spec.Hostname

		return mergeDocument(cfg, "hostname", doc)
	}

	//nolint:staticcheck // the legacy block is the only form a pre-1.14 contract accepts
	return mergeDocument(cfg, "hostname", &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		MachineConfig: &mv1alpha1.MachineConfig{
			MachineNetwork: &mv1alpha1.NetworkConfig{NetworkHostname: m.Spec.Hostname}, //nolint:staticcheck // see above
		},
	})
}
