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
// docs/design/architecture.md's "Nodes" section describes
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
// machine was never an etcd member, so there is nothing here to
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

// conditionTransitioning and its two reasons implement
// docs/design/architecture.md's "Nodes" section: "Even counts are allowed
// only transiently during a change and shown as 'transitioning', during
// which the cluster lock blocks other disruptive work." spec.controlPlane.count
// is the desired state and stays an odd enum (1, 3 or 5) throughout --
// nothing about that field ever becomes even. The *actual* count of
// approved control-plane TalosMachines does, for exactly as long as a
// replacement is being added before the one it replaces is removed
// (architecture.md: "3->4->3, never 3->2"), and that is a fact about the
// cluster's live state, not its spec -- TalosCluster.status is where it
// belongs (and this is a derived fact about TalosMachine
// objects that already exist, not a record of an imperative action).
const (
	conditionTransitioning        = "Transitioning"
	reasonEvenControlPlaneCount   = "EvenControlPlaneCount"
	reasonStableControlPlaneCount = "StableControlPlaneCount"
)

// transitioningControlPlanes reports whether controlPlanes control planes
// -- a live count, not spec.controlPlane.count's declared one -- is itself
// evidence of an in-progress control-plane replacement. Zero is not
// transitioning: that is "no control planes exist yet" (a brand-new
// cluster before its first approval), not a change in progress.
func transitioningControlPlanes(controlPlanes int) bool {
	return controlPlanes > 0 && controlPlanes%2 == 0
}

// transitioningError is acquireDisruptiveLock's refusal when the cluster's
// live control-plane count is even and the caller is not the operation
// resolving that transition. Distinguished from clusterlock.HeldError so
// callers can report the right condition reason for each.
type transitioningError struct {
	controlPlanes int
}

func (e *transitioningError) Error() string {
	return fmt.Sprintf("cluster has %d approved control planes (even -- a replacement is in progress)", e.controlPlanes)
}

// acquireDisruptiveLock is the only place in this package that calls
// clusterlock.Lock.Acquire -- enforced by TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite,
// a go/parser check in the same spirit as TestFixtureUUIDsAreUnique. Every
// disruptive operation (refuseReboot today; a future upgrade or CA
// rotation) goes through it, so the control-plane-transitioning refusal
// lives in exactly one place instead of being a safety net each new call
// site has to remember to add -- the same way the secrets RBAC marker
// regressed once already by not being centralized.
//
// The live control-plane count is recomputed fresh here, under m's
// cluster, rather than trusting TalosCluster.status's own Transitioning
// condition, which could be one reconcile stale. If it is even,
// acquireDisruptiveLock refuses with a *transitioningError before ever
// calling Acquire -- unless completesTransition is true, the one
// legitimate exception: the removal that completes a 3->4->3 replacement
// necessarily runs while the count is still even, and refusing it here
// would make that replacement impossible to ever finish. Removal still
// has its own, more precise quorum/last-control-plane/declared-count
// checks after the lock is held; this is only the generic gate every
// other disruptive operation needs.
func (r *TalosMachineReconciler) acquireDisruptiveLock(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	identity string, completesTransition bool,
) (*clusterlock.Lock, error) {
	if !completesTransition {
		controlPlanes, err := r.countApprovedControlPlanes(ctx, m)
		if err != nil {
			return nil, err
		}
		if transitioningControlPlanes(controlPlanes) {
			return nil, &transitioningError{controlPlanes: controlPlanes}
		}
	}

	lock := clusterlock.New(r.Client, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
	if err := lock.Acquire(ctx, identity); err != nil {
		return nil, err
	}

	return lock, nil
}

// updateTransitioning recomputes conditionTransitioning on cluster from
// controlPlanes and persists it. Called from wherever this file already
// counts control planes for another reason (reconcileRemoval, and
// TalosMachineReconciler.Reconcile's main path for every approved
// machine's reconcile), rather than on its own schedule, so it costs no
// extra List beyond what that caller already pays for.
func (r *TalosMachineReconciler) updateTransitioning(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, controlPlanes int) error {
	status, reason := metav1.ConditionFalse, reasonStableControlPlaneCount
	if transitioningControlPlanes(controlPlanes) {
		status, reason = metav1.ConditionTrue, reasonEvenControlPlaneCount
	}

	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: conditionTransitioning, Status: status, Reason: reason,
		Message: fmt.Sprintf("%d control planes approved right now", controlPlanes),
	})

	return r.Status().Update(ctx, cluster)
}

// refreshTransitioning counts cluster's approved control planes (the same
// List countApprovedControlPlanes always does) and updates its
// Transitioning condition from the result. Pulled out of Reconcile's main
// path as its own call, rather than inlined, purely to keep that
// function's branching down -- it has no other reason to exist separately
// from updateTransitioning.
func (r *TalosMachineReconciler) refreshTransitioning(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) error {
	controlPlanes, err := r.countApprovedControlPlanes(ctx, m)
	if err != nil {
		return err
	}

	return r.updateTransitioning(ctx, cluster, controlPlanes)
}

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

	// completesTransition is true: this is the removal that can bring the
	// live count back to the declared one (3->4->3's last step), so it
	// must be allowed to proceed while the count is still even -- refusing
	// it here would make that replacement impossible to ever finish.
	lock, err := r.acquireDisruptiveLock(ctx, &cluster, m, controllerIdentity, true)
	if err != nil {
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

	if err := r.updateTransitioning(ctx, &cluster, siblings); err != nil {
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
