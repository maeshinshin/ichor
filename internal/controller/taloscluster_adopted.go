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

package controller

import (
	"context"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const reasonAdopted = "Adopted"

// adviseCARotation gives a cluster that `ichor adopt` created the CARotationAdvised condition
// (True, reason Adopted). Status is a subresource the controller owns, so the CLI,
// which only writes spec and metadata, cannot set it; the adoption annotation is what asks for it.
// The condition is set once: absent only, so a later clearing (a completed rotation, a
// dismissal) is never undone. A cluster that already completed a CA rotation is not advised.
func adviseCARotation(ctx context.Context, c client.Client, cluster *ichorv1alpha1.TalosCluster) error {
	if cluster.Annotations[ichorv1alpha1.TalosClusterAdoptedAnnotation] != "true" ||
		apimeta.FindStatusCondition(cluster.Status.Conditions, ichorv1alpha1.ConditionCARotationAdvised) != nil {
		return nil
	}

	var ops ichorv1alpha1.TalosOperationList
	if err := c.List(ctx, &ops, client.InNamespace(cluster.Namespace)); err != nil {
		return err
	}

	for i := range ops.Items {
		op := &ops.Items[i]
		if op.Spec.ClusterRef.Name == cluster.Name && op.Spec.Kind == ichorv1alpha1.TalosOperationKindCARotation &&
			op.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
			return nil
		}
	}

	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: ichorv1alpha1.ConditionCARotationAdvised, Status: metav1.ConditionTrue, Reason: reasonAdopted,
		Message: "this cluster was adopted: previous administrators' client certificates and the CA keys held by its creator " +
			"remain valid; consider `ichor cluster rotate-ca`",
	})

	return c.Status().Update(ctx, cluster)
}
