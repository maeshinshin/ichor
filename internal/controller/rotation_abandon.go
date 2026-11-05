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

// Deleting a ca-rotation or key-rotation operation (hardware amendment). The finalizer holds an
// operation whose nodes sit between two states, because the stage is the only record of them. Before the
// point of no return (api RotationAbandonable) a delete is a request to abandon: the new CAs or keys leave
// the bundle Secret, the cluster lock is released, a node that was being rebooted is uncordoned and the
// finalizer goes, so the machine controller renders the old material again. Past it the delete is held, with
// a condition that says why, until the rotation is done.
package controller

import (
	"cmp"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
)

const (
	// conditionDeletionHeld is True on a rotation whose deletion was requested while it is past the point it can be
	// abandoned from; it ends with the rotation.
	conditionDeletionHeld = "DeletionHeld"

	reasonRotationAbandoned    = "RotationAbandoned"
	reasonRotationDeletionHeld = "PastPointOfNoReturn"
)

// rotationStage names the stage of a ca-rotation or key-rotation operation, for messages.
func rotationStage(op *ichorv1alpha1.TalosOperation) string {
	switch {
	case op.Status.CARotation != nil:
		return op.Status.CARotation.Stage
	case op.Status.KeyRotation != nil:
		return op.Status.KeyRotation.Stage
	}

	return ""
}

// rotationDeletion handles a deletion request on a rotation that is under way. handled is true when the
// operation was abandoned (the finalizer is gone); false means the reconcile goes on, with the rotation
// continuing and DeletionHeld set when it cannot be abandoned.
func (r *TalosOperationReconciler) rotationDeletion(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, finalizer, bundleKey string,
) (handled bool, err error) {
	if !ichorv1alpha1.RotationAbandonable(op) {
		if apimeta.IsStatusConditionTrue(op.Status.Conditions, conditionDeletionHeld) {
			return false, nil
		}

		apimeta.SetStatusCondition(&op.Status.Conditions, metav1.Condition{
			Type: conditionDeletionHeld, Status: metav1.ConditionTrue, Reason: reasonRotationDeletionHeld,
			Message: fmt.Sprintf("deleting this operation was requested, but the rotation is in stage %s, past the point it can be taken back from: "+
				"the nodes would be left between two states. It goes on and the operation is deleted when it is done", rotationStage(op)),
		})

		return false, r.Status().Update(ctx, op)
	}

	if err := r.abandonRotation(ctx, op, bundleKey); err != nil {
		return false, fmt.Errorf("abandoning the %s operation %q: %w", op.Spec.Kind, op.Name, err)
	}

	controllerutil.RemoveFinalizer(op, finalizer)

	return true, client.IgnoreNotFound(r.Update(ctx, op))
}

// abandonRotation gives the cluster back: the new material leaves the bundle Secret, the lock is released
// and a node cordoned for a reboot is uncordoned. Every step is idempotent and re-derived, so a failure is
// retried from the top.
func (r *TalosOperationReconciler) abandonRotation(ctx context.Context, op *ichorv1alpha1.TalosOperation, bundleKey string) error {
	var cluster ichorv1alpha1.TalosCluster

	err := r.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.ClusterRef.Name}, &cluster)
	if apierrors.IsNotFound(err) {
		return nil // the cluster is gone, and with it the bundle and the lock
	}

	if err != nil {
		return err
	}

	if err := r.undoEtcdReboot(ctx, op, &cluster); err != nil {
		return err
	}

	if err := r.removeBundleKey(ctx, &cluster, bundleKey); err != nil {
		return err
	}

	lock := clusterlock.New(r.Client, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0).WithReader(r.APIReader)
	if err := lock.Release(ctx, operationLockIdentity(op)); err != nil {
		return err
	}

	r.operationEvent(&cluster, corev1.EventTypeNormal, reasonRotationAbandoned, fmt.Sprintf(
		"the %s operation %s was deleted in stage %s and abandoned: the new material is removed and the machines are rendered from the old again",
		op.Spec.Kind, op.Name, rotationStage(op)))

	return nil
}

// removeBundleKey deletes key from the cluster's bundle Secret, the one object a rotation writes. A bundle
// without the key, or without the Secret, has nothing to remove.
func (r *TalosOperationReconciler) removeBundleKey(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, key string) error {
	var secret corev1.Secret

	name := types.NamespacedName{Namespace: r.ControllerNamespace, Name: cluster.Spec.SecretsRef.Name}
	if err := readerOrClient(r.APIReader, r.Client).Get(ctx, name, &secret); err != nil {
		return client.IgnoreNotFound(err)
	}

	if _, ok := secret.Data[key]; !ok {
		return nil
	}

	delete(secret.Data, key)

	return r.Update(ctx, &secret)
}

// undoEtcdReboot uncordons the control plane a ca-rotation was rebooting, so an abandoned rotation does not
// leave it unschedulable. The node is named by the machine (the node may be down, so it is not asked).
func (r *TalosOperationReconciler) undoEtcdReboot(ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster) error {
	if op.Spec.Kind != ichorv1alpha1.TalosOperationKindCARotation || op.Status.CARotation == nil || op.Status.CARotation.EtcdRebooting == "" {
		return nil
	}

	var m ichorv1alpha1.TalosMachine

	err := readerOrClient(r.APIReader, r.Client).Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Status.CARotation.EtcdRebooting}, &m)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	run := &caRun{r: r, op: op, cluster: cluster}

	return run.uncordon(ctx, &m, nodeNameFromMachine(&m))
}

// nodeNameFromMachine is the Kubernetes node name the machine's spec or last read gives, without asking the node.
func nodeNameFromMachine(m *ichorv1alpha1.TalosMachine) string {
	return cmp.Or(m.Spec.Hostname, m.Status.NodeName)
}
