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
	"fmt"
	"slices"
	"strings"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The kubernetes.io / k8s.io labels a kubelet (and the NodeRestriction admission plugin) may
// set on its own Node; every other label in those namespaces, node-role.kubernetes.io/* included,
// is refused. Talos validates only a label's syntax (pkg/machinery/labels), so without this check
// the kubelet would fail at start-up.
var (
	kubeletLabelNamespaces = []string{"kubelet.kubernetes.io", "node.kubernetes.io"}
	kubeletLabelKeys       = map[string]bool{
		"kubernetes.io/hostname": true, "kubernetes.io/arch": true, "kubernetes.io/os": true,
		"node.kubernetes.io/instance-type": true, "beta.kubernetes.io/instance-type": true,
		"beta.kubernetes.io/arch": true, "beta.kubernetes.io/os": true,
		"failure-domain.beta.kubernetes.io/zone": true, "failure-domain.beta.kubernetes.io/region": true,
		"topology.kubernetes.io/zone": true, "topology.kubernetes.io/region": true,
	}
)

func inDomain(prefix, domain string) bool {
	return prefix == domain || strings.HasSuffix(prefix, "."+domain)
}

func validateNodeLabels(nodeLabels map[string]string) error {
	for k := range nodeLabels {
		prefix, _, found := strings.Cut(k, "/")
		if !found || kubeletLabelKeys[k] {
			continue
		}

		restricted := (inDomain(prefix, "kubernetes.io") || inDomain(prefix, "k8s.io")) &&
			!slices.ContainsFunc(kubeletLabelNamespaces, func(ns string) bool { return inDomain(prefix, ns) })

		if restricted {
			return fmt.Errorf("render: spec.nodeLabels %q is in a reserved kubernetes.io / k8s.io namespace "+
				"the kubelet may not set (NodeRestriction)", k)
		}
	}

	return nil
}

// injectNodeLabels merges the machine's spec.nodeLabels into cfg. It runs after injectStorage:
// the labels are the operator's intent (machine layer) and win a key shared with the
// TalosStorage's system-layer label.
func injectNodeLabels(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, m *ichorv1alpha1.TalosMachine,
) (coreconfig.Provider, error) {
	if len(m.Spec.NodeLabels) == 0 {
		return cfg, nil
	}

	if err := validateNodeLabels(m.Spec.NodeLabels); err != nil {
		return nil, err
	}

	var doc config.Document

	if contract.MultidocKubernetesConfigSupported() {
		d := k8s.NewKubeNodeConfigV1Alpha1()
		d.LabelsConfig = m.Spec.NodeLabels
		doc = d
	} else {
		//nolint:staticcheck // the legacy block is the only form a pre-1.14 contract accepts
		doc = &mv1alpha1.Config{
			ConfigVersion: mv1alpha1.Version,
			MachineConfig: &mv1alpha1.MachineConfig{MachineNodeLabels: m.Spec.NodeLabels},
		}
	}

	return mergeDocument(cfg, "spec.nodeLabels", doc)
}
