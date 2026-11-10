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
	"maps"
	"slices"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// dropDefaultNodeLabels removes the control-plane labels the generator adds (spec.controlPlane.
// omitDefaultNodeLabels, ADR 0144): machine.nodeLabels' exclude-from-external-load-balancers
// before the v1.14 contract, KubeNodeConfig.labels' role and exclude labels from it. A strategic
// merge cannot delete a key, so the generated documents are edited.
func dropDefaultNodeLabels(cfg coreconfig.Provider, contract *coreconfig.VersionContract) (coreconfig.Provider, error) {
	generated := []string{constants.LabelNodeRoleControlPlane, constants.LabelExcludeFromExternalLB}

	if !contract.MultidocKubernetesConfigSupported() {
		return cfg.PatchV1Alpha1(func(c *mv1alpha1.Config) error {
			if c.MachineConfig != nil {
				//nolint:staticcheck // the legacy block is the only form a pre-1.14 contract has
				c.MachineConfig.MachineNodeLabels = without(c.MachineConfig.MachineNodeLabels, generated)
			}

			return nil
		})
	}

	docs := slices.Clone(cfg.Documents())

	for i, d := range docs {
		if n, ok := d.(*k8s.KubeNodeConfigV1Alpha1); ok {
			n = n.Clone().(*k8s.KubeNodeConfigV1Alpha1)
			n.LabelsConfig = without(n.LabelsConfig, generated)
			docs[i] = n
		}
	}

	out, err := container.New(docs...)
	if err != nil {
		return nil, fmt.Errorf("render: dropping the default node labels: %w", err)
	}

	return out, nil
}

// without is labels minus keys, nil when nothing is left.
func without(labels map[string]string, keys []string) map[string]string {
	labels = maps.Clone(labels)
	for _, k := range keys {
		delete(labels, k)
	}

	if len(labels) == 0 {
		return nil
	}

	return labels
}
