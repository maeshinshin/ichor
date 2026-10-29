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
// A control plane that fails the safety check above keeps its finalizer
// and nothing else happens. One that passes is drained (k8s.io/kubectl's
// own helper, cordon then evict -- architecture.md: "Draining uses
// k8s.io/kubectl's own drain helper rather than a hand-written eviction
// loop"), then has its etcd member removed (the departing
// machine's own graceful EtcdLeaveCluster first, a surviving sibling's
// EtcdRemoveMemberByID as fallback), then -- only if its removal opted in,
// since Reset is irreversible -- reset back to maintenance mode (STATE and EPHEMERAL by label, never the whole
// system disk). Only
// once every step that ran actually succeeded does the finalizer clear;
// any failure along the way holds it instead of letting deletion complete
// while the physical node might still be configured, still an etcd
// member, or still credentialed.
package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// machineRemovalFinalizer is added to an approved control-plane TalosMachine
// so deleting it is intercepted by reconcileRemoval instead of completing
// immediately. Nothing else gets this finalizer: a worker or an unapproved
// machine was never an etcd member, so there is nothing here to
// protect, and giving every TalosMachine a finalizer that always just
// removes itself again would be ceremony with no safety behind it.
const machineRemovalFinalizer = ichorv1alpha1.TalosMachineRemovalFinalizer

// removalRenewInterval is how often reconcileRemoval renews the cluster
// lock while draining. One minute is comfortably inside
// clusterlock.DefaultDuration (five minutes), leaving room for a slow drain
// to renew late once and still not lose the lock.
const removalRenewInterval = time.Minute

// etcdDialTimeout bounds every dial and RPC removeEtcdMember/removeEtcdMemberFromSurvivor
// make. Confirmed necessary for real: a node that just left etcd can itself
// go unreachable moments later, and an unbounded context on an unreachable target blocks
// for as long as Reconcile's own ctx lives -- with one worker per controller
// (SetupWithManager's default), that wedges every other TalosMachine's reconcile behind
// it, not just this one's retry. 15s is generous for a healthy node's own API (normally
// sub-second) while still bounding a dead one's hang to something a human waits out
// rather than a human who gives up and kills the process not knowing it would have
// recovered on its own eventually.
//
// A var so tests can shrink it; nothing else assigns it.
var etcdDialTimeout = 15 * time.Second

const (
	conditionRemoval           = "Removal"
	reasonRemovalRefused       = "RemovalRefused"
	reasonDeadMemberRemoved    = "DeadMemberRemoved"
	reasonDeadMemberRemoval    = "DeadMemberRemoval"
	reasonDrainFailed          = "DrainFailed"
	reasonEvacuationPending    = "StorageEvacuationPending"
	reasonEvacuationFailed     = "StorageEvacuationFailed"
	reasonRemovalApproved      = "RemovalApproved"
	reasonEtcdMemberRemoveFail = "EtcdMemberRemovalFailed"
	reasonResetRequested       = "ResetRequested"
	reasonResetFailed          = "ResetFailed"
	reasonNodeNameUnresolved   = "NodeNameUnresolved"
)

// resetOnDeleteAnnotation opts a TalosMachine's removal into also
// resetting its physical node, the same shape
// deleteguard.AllowDeleteAnnotation already uses: a deliberate,
// explicit, per-object opt-in rather than a default, because Reset is
// irreversible and an operator may want the disk left alone to inspect
// or to reuse for something else. Its absence leaves removal exactly
// as it was: etcd member removed, finalizer cleared, and
// NodeStillHasClusterCredentials said the node still holds the cluster's
// PKI.
const resetOnDeleteAnnotation = "ichor.maesh.dev/reset-on-delete"

// resetOnDeleteValue is the only resetOnDeleteAnnotation value that opts in.
const resetOnDeleteValue = "true"

// resetRequested reports whether m's removal opted into also resetting
// the physical node.
func resetRequested(m *ichorv1alpha1.TalosMachine) bool {
	return m.Annotations[resetOnDeleteAnnotation] == resetOnDeleteValue
}

// reasonNodeStillHasClusterCredentials is the Event reason removal.go
// emits in the same breath as clearing machineRemovalFinalizer.
// The condition that would normally carry this message disappears with the
// object the moment the finalizer clears, so the Event -- a separate
// object with its own lifetime -- is what keeps the caveat visible to an
// operator afterward, instead of it passing silently.
const reasonNodeStillHasClusterCredentials = "NodeStillHasClusterCredentials"

// nodeStillHasClusterCredentialsMessage is deliberately explicit rather
// than folded into reasonRemovalApproved's own message: Reset is a separate unit (a partition-wipe argument it is
// not this
// code's job to get wrong), so a removed TalosMachine's physical node still
// holds this cluster's PKI until something resets it -- it cannot rejoin
// as a trusted etcd member, but it is not safe to repurpose or discard
// either.
const nodeStillHasClusterCredentialsMessage = "control plane %q was removed from etcd, but its physical node was never reset " +
	"and still holds this cluster's PKI; it cannot rejoin etcd, but do not repurpose or discard the hardware until it is reset. " +
	nodeObjectStaysNote

// workerStillHasClusterCredentialsMessage is the worker form: it was never an etcd member.
const workerStillHasClusterCredentialsMessage = "worker %q was removed, but its physical node was never reset " +
	"and still holds this cluster's PKI; do not repurpose or discard the hardware until it is reset. " +
	nodeObjectStaysNote

// nodeObjectStaysNote: the Kubernetes Node is not deleted, because the running kubelet would register it again.
const nodeObjectStaysNote = "The Kubernetes Node stays while the machine runs; power it off or reset it, then delete the Node with kubectl delete node %[1]s"

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
// disruptive operation (removal and the TalosOperation backbone today; a future upgrade or CA
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
	// A cluster being shut down accepts no other disruptive work, not even the
	// removal that would complete a replacement.
	if err := shutdownBlocker(ctx, readerOrClient(r.APIReader, r.Client), cluster.Namespace, cluster.Name, nil); err != nil {
		return nil, err
	}

	if !completesTransition {
		controlPlanes, err := r.countApprovedControlPlanes(ctx, m)
		if err != nil {
			return nil, err
		}
		if transitioningControlPlanes(controlPlanes) {
			return nil, &transitioningError{controlPlanes: controlPlanes}
		}
	}

	lock := clusterlock.New(r.Client, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0).WithReader(r.APIReader)
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
		Message: fmt.Sprintf("approved control planes right now: %d", controlPlanes),
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
		if shuttingDown, ok := errors.AsType[*shuttingDownError](err); ok {
			return r.removalCondition(ctx, m, metav1.ConditionFalse, shuttingDown.reason(),
				shuttingDown.Error()+"; removal deferred")
		}
		if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
			return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonClusterLockHeld,
				held.Error()+"; removal deferred")
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
	// A worker only reaches here when a reset gave it the finalizer; it was never an
	// etcd member and is not counted toward quorum, so only the drain and the reset apply to it.
	isControlPlane := m.Spec.Role == roleControlPlane
	deadMember := false

	if isControlPlane {
		siblings, err := r.countApprovedControlPlanes(ctx, m)
		if err != nil {
			return err
		}

		if err := r.updateTransitioning(ctx, &cluster, siblings); err != nil {
			return err
		}

		var stop bool

		deadMember, stop, err = r.checkRemovalCounts(ctx, &cluster, m, siblings)
		if stop || err != nil {
			return err
		}
	}

	// The drain, the evacuation and the etcd-member check all act on the Kubernetes node / etcd
	// member name, which spec.hostname only overrides (resolveNodeName, as everywhere else). A
	// control plane whose name cannot be told is refused: skipping its etcd member would delete
	// the machine and leave a dead member in the quorum.
	nodeName, err := r.removalNodeName(ctx, &cluster, m)
	if err != nil && isControlPlane {
		msg := fmt.Sprintf("cannot tell which Kubernetes node and etcd member control plane %q is, "+
			"so it is not removed (set spec.hostname or bring the node back): %v", m.Name, err)
		if cerr := r.removalCondition(ctx, m, metav1.ConditionFalse, reasonNodeNameUnresolved, msg); cerr != nil {
			return cerr
		}

		return errors.New(msg) // an error, so Reconcile requeues: the node may only be rebooting
	}

	// Storage evacuation comes before the drain (architecture.md, "Nodes":
	// evacuation hook, drain, etcd member, reset, Node delete), through the
	// same evacuateMachine the Talos upgrade uses. Its progress lives in
	// the StorageEvacuation condition, so a restarted controller resumes
	// from status; a timeout stops the removal here and never advances it.
	// A dead control plane has no kubelet to evict pods or move replicas: neither would ever finish.
	progress := storage.EvacuationDone
	if !deadMember {
		var err error

		progress, err = evacuateMachine(ctx, r.Client, r.Evacuator, &cluster, m, nodeName, "removal")
		if err != nil {
			return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonEvacuationFailed, err.Error())
		}
	}

	switch progress {
	case storage.EvacuationWaiting:
		msg := fmt.Sprintf("waiting for storage replicas to move off node %q before draining it", nodeName)
		if err := r.removalCondition(ctx, m, metav1.ConditionFalse, reasonEvacuationPending, msg); err != nil {
			return err
		}
		// An error, not nil: Reconcile requeues only on error (nothing
		// else changes on this object while a replica rebuilds elsewhere).
		return errors.New(msg)
	case storage.EvacuationPaused:
		msg := fmt.Sprintf("evacuating node %q timed out with storage replicas still on it; "+
			"removal is stopped and scheduling was re-enabled (retrying evacuation)", nodeName)
		if err := r.removalCondition(ctx, m, metav1.ConditionFalse, reasonEvacuationFailed, msg); err != nil {
			return err
		}
		if r.Recorder != nil {
			r.Recorder.Event(m, corev1.EventTypeWarning, reasonEvacuationFailed, msg)
		}
		return errors.New(msg)
	}

	var drainErr error

	if !deadMember {
		stop := renewWhile(ctx, lock, controllerIdentity, removalRenewInterval)
		drainErr = r.drainNode(ctx, nodeName)

		stop()
	}

	if drainErr != nil {
		return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonDrainFailed, drainErr.Error())
	}

	if isControlPlane {
		if err := r.removeEtcdMember(ctx, &cluster, m, nodeName); err != nil {
			log.Error(err, "removing etcd member")

			return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonEtcdMemberRemoveFail, err.Error())
		}
	}

	reset := resetRequested(m)
	if reset {
		if err := r.resetNode(ctx, &cluster, m); err != nil {
			log.Error(err, "resetting node")

			return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonResetFailed, err.Error())
		}
	}

	return r.completeRemoval(ctx, m, reset)
}

// checkRemovalCounts runs the control plane removal refusals that need no node: quorum and
// last-control-plane always hold. The declared count does not stop the removal of a control plane whose
// etcd member is dead, since replacing it is how the cluster gets back to the count. stop is
// true when the removal is refused (the Removal condition says why); dead when it goes on below the count.
func (r *TalosMachineReconciler) checkRemovalCounts(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, siblings int,
) (dead, stop bool, err error) {
	if err := deleteguard.CheckControlPlaneQuorum(siblings); err != nil {
		return false, true, r.removalCondition(ctx, m, metav1.ConditionFalse, reasonRemovalRefused, err.Error())
	}

	countErr := deleteguard.CheckControlPlaneCount(siblings, int(cluster.Spec.ControlPlane.Count))
	if countErr == nil {
		return false, false, nil
	}

	why, dead := r.deadEtcdMember(ctx, cluster, m)
	if !dead {
		return false, true, r.removalCondition(ctx, m, metav1.ConditionFalse, reasonRemovalRefused,
			countErr.Error()+"; its etcd member is not unhealthy, so this is not a replacement of a dead control plane ("+why+")")
	}

	msg := fmt.Sprintf("removing control plane %q below the declared count of %d: its etcd member is %s; "+
		"approve a replacement to get back to the declared count", m.Name, cluster.Spec.ControlPlane.Count, why)
	logf.FromContext(ctx).Info(msg)

	if r.Recorder != nil {
		r.Recorder.Event(m, corev1.EventTypeWarning, reasonDeadMemberRemoved, msg)
	}

	// Recorded, so a round that resumes after the member is gone from etcd still knows why it proceeds.
	return true, false, r.removalCondition(ctx, m, metav1.ConditionFalse, reasonDeadMemberRemoval, msg)
}

// removalNodeName is resolveNodeName for the departing machine: spec.hostname when set,
// otherwise its own report (dialed here, bounded by etcdDialTimeout) or the Kubernetes Node at
// its endpoint. The error is only fatal for a control plane (the caller decides).
func (r *TalosMachineReconciler) removalNodeName(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (string, error) {
	if m.Spec.Hostname != "" {
		return m.Spec.Hostname, nil
	}

	var talos TalosClient

	ctx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	if r.TalosClient != nil {
		if bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster); err == nil {
			if c, err := r.TalosClient(ctx, cluster, m, bundle); err == nil {
				defer closeTalosClient(c)

				talos = c
			}
		}
	}

	return resolveNodeName(ctx, r.Client, talos, m)
}

// completeRemoval records the Removal condition's terminal success message,
// then clears machineRemovalFinalizer -- in that order, so a crash between
// the two leaves the finalizer still held (removeEtcdMember's own
// idempotent check-then-act, and resetNode's own already-requested check,
// mean the next reconcile finds the earlier work already done and comes
// straight back here, not back to a redundant removal or reset attempt).
//
// reset is true only when this removal opted into's Reset step
// and it succeeded: the Warning Event -- the one that
// survives the object's own disappearance to say a decommissioned node
// still holds the cluster's PKI -- is skipped in that case, because it is
// no longer true: Reset wiped the STATE partition those credentials lived
// on. The event fires exactly when it did existed.
func (r *TalosMachineReconciler) completeRemoval(ctx context.Context, m *ichorv1alpha1.TalosMachine, reset bool) error {
	message := "the node was drained and removed"
	if m.Spec.Role == roleControlPlane {
		message = "removal passed the quorum, last-control-plane and declared-count checks, the node was " +
			"drained, and its etcd member was removed"
	}

	if reset {
		message += "; the node was reset and is rebooting back to maintenance mode"
	}

	if err := r.removalCondition(ctx, m, metav1.ConditionTrue, reasonRemovalApproved, message); err != nil {
		return err
	}

	if !reset && r.Recorder != nil {
		msg := nodeStillHasClusterCredentialsMessage
		if m.Spec.Role != roleControlPlane {
			// A worker was never an etcd member: do not claim it was removed from etcd.
			msg = workerStillHasClusterCredentialsMessage
		}

		// The Node is named after the host, not the machine object; Status.NodeName is the host's name.
		name := cmp.Or(m.Status.NodeName, m.Name)

		r.Recorder.Eventf(m, corev1.EventTypeWarning, reasonNodeStillHasClusterCredentials, msg, name)
	}

	controllerutil.RemoveFinalizer(m, machineRemovalFinalizer)

	return r.Update(ctx, m)
}

// resetNode asks m's own node to reset: wipe STATE and
// EPHEMERAL and reboot back to maintenance mode. Idempotent the only way
// a fire-and-forget RPC can be: conditionRemoval already carrying
// reasonResetRequested means Talos already accepted this request on an
// earlier reconcile, so this returns immediately without asking again --
// architecture.md's own fix for "a resumed reconcile fires a second
// concurrent reset at the same node." The record is written immediately
// after Reset's own RPC acknowledges the request, not before: Reset
// returns once Talos has accepted it and started the sequence in the
// background, not once the wipe finishes, so acknowledging is the
// earliest point at which "a reset was requested" is actually true.
func (r *TalosMachineReconciler) resetNode(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) error {
	if apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionRemoval) {
		return nil
	}

	if r.TalosClient == nil {
		return fmt.Errorf("no Talos API connectivity is configured for this controller")
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return fmt.Errorf("loading secrets bundle to reset %q: %w", m.Name, err)
	}

	resetCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	talos, err := r.TalosClient(resetCtx, cluster, m, bundle)
	if err != nil {
		return fmt.Errorf("dialing %q to reset it: %w", m.Name, err)
	}
	defer closeTalosClient(talos)

	if err := talos.Reset(resetCtx); err != nil {
		return fmt.Errorf("reset request to %q failed: %w", m.Name, err)
	}

	return r.removalCondition(ctx, m, metav1.ConditionTrue, reasonResetRequested,
		"reset requested: the node's STATE and EPHEMERAL partitions are being wiped and it will reboot to maintenance mode")
}

// removeEtcdMember removes m from etcd, preferring m's own graceful
// EtcdLeaveCluster -- talosctl's own "etcd leave" command says plainly to
// "always prefer etcd leave over" removing a member by ID -- and falling
// back to EtcdRemoveMemberByID from a surviving sibling only when m cannot
// be dialed or cannot leave on its own (architecture.md's forceRemove case:
// the node is dead or unreachable).
//
// Idempotent by construction, not by catching a particular error string:
// architecture.md records that "removing an already-removed etcd member
// reports not-found, which counts as success", so both branches check the
// live member list first and return nil immediately if m is already gone.
// That is also how this resumes correctly after a crash between a
// completed removal and the finalizer clearing in completeRemoval: the
// next reconcile re-lists rather than trusting any local memory of what it
// already did.
func (r *TalosMachineReconciler) removeEtcdMember(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, nodeName string,
) error {
	if r.TalosClient == nil {
		return fmt.Errorf("no Talos API connectivity is configured for this controller")
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return fmt.Errorf("loading secrets bundle for etcd member removal: %w", err)
	}

	if r.trySelfLeave(ctx, cluster, m, nodeName, bundle) {
		return nil
	}

	return r.removeEtcdMemberFromSurvivor(ctx, cluster, m, nodeName, bundle)
}

// trySelfLeave attempts removeEtcdMember's first-choice path (self-dial, list, leave) and
// reports whether m is now confirmed gone from etcd -- either because it already was, or
// because EtcdLeaveCluster just succeeded. false covers every other outcome (unreachable,
// listed but leave itself failed), for which removeEtcdMember falls back to a survivor.
//
// etcdDialTimeout bounds this whole attempt: confirmed for real that a node
// which just left etcd can itself go unreachable moments later (its own apiserver and
// Talos API both stopped answering), and grpc's lazy dial means the first RPC on an
// unreachable target blocks for as long as ctx allows -- which, without this, is however
// long Reconcile's own ctx happens to live, wedging this package's one worker on every
// other TalosMachine behind it.
func (r *TalosMachineReconciler) trySelfLeave(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, nodeName string, bundle *secrets.Bundle,
) bool {
	selfCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	self, err := r.TalosClient(selfCtx, cluster, m, bundle)
	if err != nil {
		return false
	}
	defer closeTalosClient(self)

	members, err := self.EtcdMemberList(selfCtx)
	if err != nil {
		return false
	}

	if !etcdMemberPresent(members, nodeName) {
		return true // already gone: the resumption case above
	}

	return self.EtcdLeaveCluster(selfCtx) == nil
}

// removeEtcdMemberFromSurvivor is removeEtcdMember's fallback: dial a
// different approved control-plane TalosMachine in the same cluster and
// use its view of etcd to remove m by ID. Returns a descriptive error if no
// survivor can be found or dialed at all -- reconcileRemoval holds the
// finalizer and retries later rather than treating that as "already
// removed".
func (r *TalosMachineReconciler) removeEtcdMemberFromSurvivor(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, nodeName string, bundle *secrets.Bundle,
) error {
	survivor, err := r.findEtcdSurvivor(ctx, m)
	if err != nil {
		return err
	}

	survivorCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	sc, err := r.TalosClient(survivorCtx, cluster, survivor, bundle)
	if err != nil {
		return fmt.Errorf("dialing a surviving control plane to remove %q from etcd: %w", m.Name, err)
	}
	defer closeTalosClient(sc)

	members, err := sc.EtcdMemberList(survivorCtx)
	if err != nil {
		return fmt.Errorf("listing etcd members from a surviving control plane: %w", err)
	}

	id, found := etcdMemberID(members, nodeName)
	if !found {
		return nil // already gone
	}

	return sc.EtcdRemoveMemberByID(survivorCtx, id)
}

// findEtcdSurvivor finds another approved control-plane TalosMachine in m's
// cluster that is not itself being deleted, to dial as the "a surviving
// node says remove this member" side of EtcdRemoveMemberByID. Uses
// r.APIReader, the same direct read deleteguard.CountApprovedControlPlanes
// requires, for the same reason: a cached read could still show a sibling
// that has, moments ago, started its own removal.
func (r *TalosMachineReconciler) findEtcdSurvivor(
	ctx context.Context, m *ichorv1alpha1.TalosMachine,
) (*ichorv1alpha1.TalosMachine, error) {
	siblings, err := deleteguard.ApprovedMachines(ctx, r.APIReader, m.Namespace, m.Spec.ClusterRef.Name,
		deleteguard.MachineFilter{Role: roleControlPlane})
	if err != nil {
		return nil, err
	}

	for i := range siblings {
		if siblings[i].Name != m.Name {
			return &siblings[i], nil
		}
	}

	return nil, fmt.Errorf("no surviving control plane found to remove %q from etcd", m.Name)
}

// etcdMemberPresent reports whether hostname appears in members.
func etcdMemberPresent(members []EtcdMember, hostname string) bool {
	_, found := etcdMemberID(members, hostname)

	return found
}

// etcdMemberID finds the etcd member ID matching hostname -- etcd reports
// the Node's own hostname, which is TalosMachineSpec.Hostname's job to
// carry (architecture.md: Talos derives one when it is left unset, but
// nothing here can discover that derived name, so an unset Hostname can
// never be found and removeEtcdMember's caller holds the finalizer and
// retries rather than guessing).
func etcdMemberID(members []EtcdMember, hostname string) (uint64, bool) {
	if hostname == "" {
		return 0, false
	}

	for _, member := range members {
		if member.Hostname == hostname {
			return member.ID, true
		}
	}

	return 0, false
}

// closeTalosClient closes talos if it is an io.Closer, the same optional
// capability check talosmachine_controller.go's main path already uses --
// the fake test double has nothing to close, so the interface itself stays
// at the methods design 5.6 actually need.
func closeTalosClient(talos TalosClient) {
	if closer, ok := talos.(io.Closer); ok {
		_ = closer.Close()
	}
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
	return setMachineCondition(ctx, readerOrClient(r.APIReader, r.Client), r.Client, m, metav1.Condition{
		Type: conditionRemoval, Status: status, Reason: reason, Message: message,
	})
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
// including m itself (which is being deleted) but not another control plane
// that is also being deleted's addendum -- via r.APIReader, not r.Client, so this stays
// correct under the same cached-read trap deleteguard.CountApprovedControlPlanes
// documents.
func (r *TalosMachineReconciler) countApprovedControlPlanes(ctx context.Context, m *ichorv1alpha1.TalosMachine) (int, error) {
	cps, err := deleteguard.ApprovedMachines(ctx, r.APIReader, m.Namespace, m.Spec.ClusterRef.Name,
		deleteguard.MachineFilter{Role: roleControlPlane, KeepDeleting: m.Name})

	return len(cps), err
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

	// Unbounded, as before: removal is one blocking call under its own lock renewal (renewWhile).
	return cordonAndDrain(newDrainHelper(ctx, r.KubeClient, 0), &node)
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
