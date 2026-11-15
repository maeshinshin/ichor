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

package upgrade

import (
	"slices"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// KubernetesStageSteps is one stage of a Kubernetes upgrade and the machines each of its steps handles together.
type KubernetesStageSteps struct {
	Stage string
	Steps [][]*ichorv1alpha1.TalosMachine
}

// KubernetesOrder is the order a Kubernetes upgrade visits the machines in, stage by stage. It is the model the
// plan shown for confirmation is built from; the executor's kubernetesBatch walks the same order, and a test in
// internal/controller checks that it still does. The first three stages visit every control plane
// (controlPlanes, sorted), one at a time. The Kubelets stage visits heldCPs one at a time, then heldWorkers
// maxUnavailable at a time, then own (the machines of the node the controller runs on, which both lists leave
// out) one at a time, last.
func KubernetesOrder(
	controlPlanes, heldCPs, heldWorkers, own []*ichorv1alpha1.TalosMachine, maxUnavailable int32,
) []KubernetesStageSteps {
	one := func(ms []*ichorv1alpha1.TalosMachine) (steps [][]*ichorv1alpha1.TalosMachine) {
		for _, m := range ms {
			steps = append(steps, []*ichorv1alpha1.TalosMachine{m})
		}

		return steps
	}

	batches := slices.Collect(slices.Chunk(heldWorkers, int(max(maxUnavailable, 1))))
	kubelets := slices.Concat(one(heldCPs), batches, one(own))

	return []KubernetesStageSteps{
		{ichorv1alpha1.KubernetesStageAPIServers, one(controlPlanes)},
		{ichorv1alpha1.KubernetesStageControlPlaneComponents, one(controlPlanes)},
		{ichorv1alpha1.KubernetesStageKubeProxy, one(controlPlanes)},
		{ichorv1alpha1.KubernetesStageKubelets, kubelets},
	}
}
