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

// Package controller: this file implements the ordered removal operation
// docs/design/architecture.md's "Nodes" section describes and ADR 0010's
// "What this guard protects, and what it doesn't" leaves as the real fix
// for the webhook's admission-time quorum check: counting objects at
// admission time cannot see a concurrent delete of a sibling control
// plane, because admission is never serialized with the commit it
// gatekeeps. A finalizer turns "delete the control plane" into a step of
// this serialized operation instead, so the recount that decides safety
// happens under the per-cluster lock, where a second concurrent removal of
// a sibling has to wait its turn rather than racing.
//
// Deliberately not implemented here: removing the etcd member and the
// Talos reset itself. Both need TalosClient widened past the three methods
// its own doc comment calls "the one interface rule 0 allows" -- a design
// decision referred back, not made here. So a control plane that passes
// the safety check here is drained (k8s.io/kubectl's own helper, cordon
// then evict -- architecture.md: "Draining uses k8s.io/kubectl's own drain
// helper rather than a hand-written eviction loop") and then keeps its
// finalizer regardless of the verdict -- refused, or confirmed-safe and
// drained -- rather than letting deletion complete while the physical node
// may still be configured and still an etcd member.
package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"k8s.io/kubectl/pkg/drain"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// machineRemovalFinalizer is added to an approved control-plane TalosMachine
// so deleting it is intercepted by reconcileRemoval instead of completing
// immediately. Nothing else gets this finalizer: a worker or an unapproved
// machine was never an etcd member (ADR 0010), so there is nothing here to
// protect, and giving every TalosMachine a finalizer that always just
// removes itself again would be ceremony with no safety behind it.
const machineRemovalFinalizer = "ichor.maesh.dev/machine-removal"

// removalRenewInterval is how often reconcileRemoval renews the cluster
// lock while draining. One minute is comfortably inside
// clusterlock.DefaultDuration (five minutes), leaving room for a slow drain
// to renew late once and still not lose the lock.
const removalRenewInterval = time.Minute

const (
	conditionRemoval      = "Removal"
	reasonRemovalRefused  = "RemovalRefused"
	reasonDrainFailed     = "DrainFailed"
	reasonRemovalApproved = "RemovalApproved"
)

// reconcileRemoval runs when m.DeletionTimestamp is set. A machine without
// machineRemovalFinalizer was never intercepted (never approved as a
// control plane) and is left alone -- nothing to coordinate, the API
// server removes it once every finalizer clears, and this was never one of
// them.
func (r *TalosMachineReconciler) reconcileRemoval(ctx context.Context, m *ichorv1alpha1.TalosMachine) error {
	if !controllerutil.ContainsFinalizer(m, machineRemovalFinalizer) {
		return nil
	}

	log := logf.FromContext(ctx)

	clusterKey := types.NamespacedName{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}

	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, clusterKey, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			// Nothing left to coordinate against; let the machine go.
			controllerutil.RemoveFinalizer(m, machineRemovalFinalizer)
			return r.Update(ctx, m)
		}
		return err
	}

	lock := clusterlock.New(r.Client, clusterKey, 0)

	if err := lock.Acquire(ctx, controllerIdentity); err != nil {
		if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
			return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonClusterLockHeld,
				fmt.Sprintf("cluster lock held by %q; removal deferred", held.Holder))
		}
		return err
	}
	defer func() {
		if err := lock.Release(ctx, controllerIdentity); err != nil {
			log.Error(err, "releasing cluster lock")
		}
	}()

	// Recomputed under the lock: a sibling control plane's own removal,
	// running concurrently, has to go through this same lock to reach this
	// same point, so whichever of two concurrent removals gets here second
	// sees the first's finalizer (or its completed deletion) already
	// reflected -- the race the webhook's admission-time count cannot see.
	siblings, err := r.countApprovedControlPlanes(ctx, m)
	if err != nil {
		return err
	}

	if err := checkControlPlaneRemoval(siblings, int(cluster.Spec.ControlPlane.Count)); err != nil {
		return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonRemovalRefused, err.Error())
	}

	stop := renewWhile(ctx, lock, controllerIdentity, removalRenewInterval)
	drainErr := r.drainNode(ctx, m.Spec.Hostname)
	stop()

	if drainErr != nil {
		return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonDrainFailed, drainErr.Error())
	}

	return r.removalCondition(ctx, m, metav1.ConditionTrue, reasonRemovalApproved,
		"removal passed the quorum, last-control-plane and declared-count checks and the node was "+
			"drained; etcd member removal and reset are not implemented yet, so the finalizer is held "+
			"rather than letting deletion complete while the node may still be an etcd member")
}

// removalCondition records reconcileRemoval's verdict as the Removal
// condition (distinct from Ready, which design 5.6's configuration
// reconcile owns and which this path never writes), so the object's
// status says what reconcileRemoval last decided even after a crash and
// restart resumes it -- the "resumable from status" requirement
// architecture.md's chaos cases ask for.
func (r *TalosMachineReconciler) removalCondition(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, status metav1.ConditionStatus, reason, message string,
) error {
	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionRemoval, Status: status, Reason: reason, Message: message,
	})
	return r.Status().Update(ctx, m)
}

// checkControlPlaneRemoval runs every refusal docs/design/architecture.md's
// "Nodes" names for the etcd-member-removal step against currentControlPlanes
// control planes (including the one being removed), declared as
// desiredCount in spec.controlPlane.count: last-control-plane and quorum
// (deleteguard.CheckControlPlaneQuorum), then the declared count
// (deleteguard.CheckControlPlaneCount). Non-control-plane roles have
// nothing to check -- they were never counted toward any of this -- so this
// is only ever called for one.
func checkControlPlaneRemoval(currentControlPlanes, desiredCount int) error {
	if err := deleteguard.CheckControlPlaneQuorum(currentControlPlanes); err != nil {
		return err
	}
	return deleteguard.CheckControlPlaneCount(currentControlPlanes, desiredCount)
}

// countApprovedControlPlanes counts, the same way
// internal/webhook/v1alpha1/talosmachine_webhook.go's ValidateDelete does,
// every approved control-plane TalosMachine in m's namespace and cluster,
// including m itself.
func (r *TalosMachineReconciler) countApprovedControlPlanes(ctx context.Context, m *ichorv1alpha1.TalosMachine) (int, error) {
	var siblings ichorv1alpha1.TalosMachineList
	if err := r.List(ctx, &siblings, client.InNamespace(m.Namespace)); err != nil {
		return 0, err
	}

	count := 0
	for _, s := range siblings.Items {
		if s.Spec.Role == "controlplane" && s.Spec.IsApproved() && s.Spec.ClusterRef.Name == m.Spec.ClusterRef.Name {
			count++
		}
	}
	return count, nil
}

// drainNode cordons and drains the Kubernetes Node named hostname, the
// design's belt before the (not yet implemented) etcd-member removal and
// reset. hostname empty, or no Node by that name, is not an error: a
// control plane that was approved but never actually joined has nothing to
// drain, and Talos derives a Node's name when TalosMachineSpec.Hostname is
// left unset, which this code has no way to guess.
func (r *TalosMachineReconciler) drainNode(ctx context.Context, hostname string) error {
	if hostname == "" || r.KubeClient == nil {
		return nil
	}

	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: hostname}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	helper := &drain.Helper{
		Ctx: ctx, Client: r.KubeClient,
		GracePeriodSeconds: -1, IgnoreAllDaemonSets: true, DeleteEmptyDirData: true,
		Out: io.Discard, ErrOut: io.Discard,
	}

	if err := drain.RunCordonOrUncordon(helper, &node, true); err != nil {
		return fmt.Errorf("cordoning node %q: %w", hostname, err)
	}

	if err := drain.RunNodeDrain(helper, hostname); err != nil {
		return fmt.Errorf("draining node %q: %w", hostname, err)
	}

	return nil
}

// renewWhile calls lock.Renew every interval until stop is called. A
// failed renew is logged, not escalated: the caller's own next clusterlock
// call surfaces a lost lock as a HeldError exactly when it matters, so
// there is nothing more to do with the error here than record it. interval
// is a parameter rather than always removalRenewInterval only so tests can
// use a short one instead of waiting out a real minute.
func renewWhile(ctx context.Context, lock *clusterlock.Lock, identity string, interval time.Duration) (stop func()) {
	done := make(chan struct{})

	go func() {
		log := logf.FromContext(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := lock.Renew(ctx, identity); err != nil {
					log.Error(err, "renewing cluster lock")
				}
			case <-done:
				return
			}
		}
	}()

	return func() { close(done) }
}
