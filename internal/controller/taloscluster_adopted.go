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
	"strconv"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const reasonAdopted = "Adopted"

// adviseCARotation owns the CARotationAdvised condition of a cluster that `ichor adopt` created. Status is a
// subresource the controller owns, so the CLI, which only writes spec
// and metadata, cannot set it; the adoption annotation is what asks for it.
//
// It sets the condition True, reason Adopted, once: absent only, so a later clearing is never
// undone. A cluster that already completed a CA rotation is not advised. It sets the condition
// False (reason Dismissed or Rotated) while it is True and the user has set the dismissal
// annotation or a CA rotation has completed.
func adviseCARotation(ctx context.Context, c client.Client, cluster *ichorv1alpha1.TalosCluster) error {
	cond := apimeta.FindStatusCondition(cluster.Status.Conditions, ichorv1alpha1.ConditionCARotationAdvised)
	if cond == nil && cluster.Annotations[ichorv1alpha1.TalosClusterAdoptedAnnotation] != strconv.FormatBool(true) ||
		cond != nil && cond.Status != metav1.ConditionTrue {
		return nil
	}

	lower := ""

	if cluster.Annotations[ichorv1alpha1.TalosClusterCARotationAdviceDismissedAnnotation] == strconv.FormatBool(true) {
		lower = "Dismissed"
	} else if rotated, err := caRotationCompleted(ctx, c, cluster); err != nil {
		return err
	} else if rotated {
		lower = "Rotated"
	}

	switch {
	case cond == nil && lower != "", cond != nil && lower == "":
		return nil
	case cond == nil:
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ichorv1alpha1.ConditionCARotationAdvised, Status: metav1.ConditionTrue, Reason: reasonAdopted,
			Message: "this cluster was adopted: previous administrators' client certificates and the CA keys held by its creator " +
				"remain valid; consider `ichor cluster rotate-ca`",
		})
	default:
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ichorv1alpha1.ConditionCARotationAdvised, Status: metav1.ConditionFalse, Reason: lower,
			Message: "the CA rotation advice no longer applies",
		})
	}

	return c.Status().Update(ctx, cluster)
}

// caRotationCompleted reports whether a ca-rotation operation on cluster has completed.
func caRotationCompleted(ctx context.Context, c client.Client, cluster *ichorv1alpha1.TalosCluster) (bool, error) {
	var ops ichorv1alpha1.TalosOperationList
	if err := c.List(ctx, &ops, client.InNamespace(cluster.Namespace)); err != nil {
		return false, err
	}

	for i := range ops.Items {
		op := &ops.Items[i]
		if op.Spec.ClusterRef.Name == cluster.Name && op.Spec.Kind == ichorv1alpha1.TalosOperationKindCARotation &&
			op.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
			return true, nil
		}
	}

	return false, nil
}

// caRotationOperationClusters maps a ca-rotation operation to its cluster, so a completed rotation
// lowers the advice without waiting for another event.
func caRotationOperationClusters(_ context.Context, obj client.Object) []reconcile.Request {
	op, ok := obj.(*ichorv1alpha1.TalosOperation)
	if !ok || op.Spec.Kind != ichorv1alpha1.TalosOperationKindCARotation {
		return nil
	}

	var req reconcile.Request

	req.Namespace, req.Name = op.Namespace, op.Spec.ClusterRef.Name

	return []reconcile.Request{req}
}

// reclaimDoneClusters maps a MachineReclaim that ended Done to its target cluster, so discovery
// probes the machine that just reached maintenance mode now and not at the next 5 minute pass.
// Nothing is written: the cluster is only enqueued.
func reclaimDoneClusters(_ context.Context, obj client.Object) []reconcile.Request {
	mr, ok := obj.(*ichorv1alpha1.MachineReclaim)
	if !ok || mr.Status.Phase != ichorv1alpha1.MachineReclaimDone || mr.Spec.ClusterRef == nil || mr.Spec.ClusterRef.Name == "" {
		return nil
	}

	var req reconcile.Request

	req.Namespace, req.Name = mr.Namespace, mr.Spec.ClusterRef.Name

	return []reconcile.Request{req}
}
