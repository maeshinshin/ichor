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

// Package controller: this file implements the ordered removal operation docs/design/architecture.md's
// "Nodes" section describes, the real fix for the webhook's admission-time quorum check
// (admission is never serialized with the commit, so it cannot see a concurrent delete of a sibling). A
// finalizer makes "delete the control plane" a step of this operation, whose recount runs under the
// per-cluster lock.
//
// A control plane that fails the safety check keeps its finalizer. One that passes is drained (kubectl's
// drain helper), has its etcd member removed (its own graceful EtcdLeaveCluster, else a
// survivor's EtcdRemoveMemberByID), then, only if its removal opted in, is reset to maintenance mode (STATE and
// EPHEMERAL, never the system disk). The finalizer clears only once every step that ran
// succeeded; a failure holds it while the node might still be configured, an etcd member or credentialed.
package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// drainRetryInterval is how long a removal waits before it tries a blocked drain again.
// Blocked means a pod (a bare pod, a PodDisruptionBudget) that someone has to act on, so the
// wait is for a person. It is shorter than the TalosOperation poll (operationRequeueInterval,
// 30s), so an operation waiting on the removal sees the resume at its next poll, and a retry is
// one Node read, one pod list and one cordon, cheap enough to repeat for hours.
const drainRetryInterval = 15 * time.Second

// removalSkipWaitSeconds is how long a pod may already be terminating before a removal's drain stops waiting for it.
const removalSkipWaitSeconds = 60

// drainBlockedError is reconcileRemoval's verdict when a pod holds the drain back. It is not a
// failure to report to the error backoff: Reconcile turns it into a requeue after drainRetryInterval.
type drainBlockedError struct {
	err error
}

func (e *drainBlockedError) Error() string { return e.err.Error() }

func (e *drainBlockedError) Unwrap() error { return e.err }

// drainBlocked records a drain that a pod holds back in the Removal condition (the message names the
// pod, as kubectl's drain does) and returns the wait verdict. The Warning is emitted when the blocking
// state starts, not on every retry.
func (r *TalosMachineReconciler) drainBlocked(ctx context.Context, m *ichorv1alpha1.TalosMachine, drainErr error) error {
	msg := drainErr.Error()
	prev := apimeta.FindStatusCondition(m.Status.Conditions, conditionRemoval)
	wasBlocked := prev != nil && prev.Reason == reasonDrainFailed && prev.Message == msg

	if err := r.removalCondition(ctx, m, metav1.ConditionFalse, reasonDrainFailed, msg); err != nil {
		return err
	}

	if !wasBlocked {
		r.event(m, corev1.EventTypeWarning, reasonDrainFailed, msg)
	}

	return &drainBlockedError{err: drainErr}
}

// reconcileDeletion handles a TalosMachine being deleted. A drain that a pod holds back is a wait:
// the machine is requeued after drainRetryInterval, not sent to the error backoff.
func (r *TalosMachineReconciler) reconcileDeletion(ctx context.Context, m *ichorv1alpha1.TalosMachine) (ctrl.Result, error) {
	err := r.reconcileRemoval(ctx, m)
	if _, waiting := errors.AsType[*removalWaitError](err); waiting {
		return ctrl.Result{RequeueAfter: nodeWaitInterval}, nil
	}

	if _, blocked := errors.AsType[*drainBlockedError](err); blocked {
		logf.FromContext(ctx).Info("drain blocked, retrying", "after", drainRetryInterval, "reason", err.Error())

		return ctrl.Result{RequeueAfter: drainRetryInterval}, nil
	}

	return ctrl.Result{}, err
}

// etcdDialTimeout bounds every dial and RPC of the etcd-member removal and the reset: a node
// that just left etcd can go unreachable moments later, and an unbounded context on it blocks for as long
// as Reconcile's ctx lives, wedging every other TalosMachine behind this one worker.
//
// A var so tests can shrink it; nothing else assigns it.
var etcdDialTimeout = 15 * time.Second

const (
	conditionRemoval        = "Removal"
	reasonRemovalRefused    = "RemovalRefused"
	reasonDeadMemberRemoved = "DeadMemberRemoved"
	reasonDeadMemberRemoval = "DeadMemberRemoval"
	reasonDrainFailed       = "DrainFailed"

	// nodeResetByIchorAnnotation marks the Kubernetes Node of a machine Ichor reset. The Node stays
	// (a delete could race the kubelet registering it again), and while it is not Ready discovery
	// no longer counts its address as a cluster member, so the wiped machine is found again.
	nodeResetByIchorAnnotation = "ichor.maesh.dev/reset-by-ichor"
	reasonEvacuationPending    = "StorageEvacuationPending"
	reasonEvacuationFailed     = "StorageEvacuationFailed"
	reasonRemovalApproved      = "RemovalApproved"
	reasonEtcdMemberRemoveFail = "EtcdMemberRemovalFailed"
	reasonResetRequested       = "ResetRequested"
	reasonResetFailed          = "ResetFailed"
	reasonNodeNameUnresolved   = "NodeNameUnresolved"
)

// resetOnDeleteAnnotation opts a TalosMachine's removal into also resetting its physical node,
// a per-object opt-in like deleteguard.AllowDeleteAnnotation, because Reset is irreversible. Without it
// removal stops there: etcd member removed, finalizer cleared, and
// NodeStillHasClusterCredentials said the node still holds the cluster's PKI.
const resetOnDeleteAnnotation = deleteguard.ResetOnDeleteAnnotation

// resetWipeModeAnnotation carries TalosOperation.spec.node.wipeMode of a reset, or the wipe mode of a remove, to the removal flow,
// which does the wipe: system-disk or all. Absent, the reset wipes STATE and EPHEMERAL.
const resetWipeModeAnnotation = deleteguard.ResetWipeModeAnnotation

// resetOnDeleteValue is the only resetOnDeleteAnnotation value that opts in.
const resetOnDeleteValue = "true"

// resetRequested reports whether m's removal opted into also resetting
// the physical node.
func resetRequested(m *ichorv1alpha1.TalosMachine) bool {
	return m.Annotations[resetOnDeleteAnnotation] == resetOnDeleteValue
}

// reasonNodeStillHasClusterCredentials is the Event reason emitted as machineRemovalFinalizer clears.
// The Removal condition goes with the object, so the Event, which outlives it, keeps the caveat visible.
const reasonNodeStillHasClusterCredentials = "NodeStillHasClusterCredentials"

// nodeStillHasClusterCredentialsMessage is explicit rather than folded into reasonRemovalApproved's message:
// until something resets it, a removed machine's physical node still holds the cluster's PKI.
const nodeStillHasClusterCredentialsMessage = "control plane %q was removed from etcd, but its physical node was never reset " +
	"and still holds this cluster's PKI; it cannot rejoin etcd, but do not repurpose or discard the hardware until it is reset. " +
	nodeObjectStaysNote

// workerStillHasClusterCredentialsMessage is the worker form: it was never an etcd member.
const workerStillHasClusterCredentialsMessage = "worker %q was removed, but its physical node was never reset " +
	"and still holds this cluster's PKI; do not repurpose or discard the hardware until it is reset. " +
	nodeObjectStaysNote

// nodeObjectStaysNote: the Kubernetes Node is not deleted, because the running kubelet would register it again.
const nodeObjectStaysNote = "The Kubernetes Node stays while the node runs; power it off or reset it, then delete the Node with kubectl delete node %[1]s"

// conditionTransitioning and its two reasons implement docs/design/architecture.md's "Nodes" section: even
// counts are allowed only transiently during a change (3->4->3, never 3->2), shown as "transitioning", while
// the cluster lock blocks other disruptive work. spec.controlPlane.count stays odd; the live count of approved
// control planes is what goes even, so the condition lives in TalosCluster.status.
const (
	conditionTransitioning        = "Transitioning"
	reasonEvenControlPlaneCount   = "EvenControlPlaneCount"
	reasonStableControlPlaneCount = "StableControlPlaneCount"
)

// transitioningControlPlanes reports whether the live count of control planes is even, evidence of an
// in-progress replacement. Zero is not: that is a brand-new cluster before its first approval.
func transitioningControlPlanes(controlPlanes int) bool {
	return controlPlanes > 0 && controlPlanes%2 == 0
}

// transitioningError is acquireDisruptiveLock's refusal when the live control-plane count is even and the
// caller is not the operation resolving that transition; distinct from clusterlock.HeldError for the reason.
type transitioningError struct {
	controlPlanes int
}

func (e *transitioningError) Error() string {
	return fmt.Sprintf("cluster has %d approved control planes (even -- a replacement is in progress)", e.controlPlanes)
}

// acquireDisruptiveLock is the only place in this package that calls clusterlock.Lock.Acquire (enforced by
// TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite), so the control-plane-transitioning refusal lives in
// one place instead of in each disruptive operation.
//
// The live control-plane count is recomputed here rather than read from the Transitioning condition, which
// could be one reconcile stale. If it is even, acquireDisruptiveLock refuses with a *transitioningError
// before calling Acquire, unless completesTransition is true: the removal that completes a 3->4->3
// replacement runs while the count is still even. Removal has its own, more precise checks once the lock is
// held; this is the generic gate.
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
	err := lock.Acquire(ctx, identity)
	// A holder that will never renew (a deleted machine or operation, a finished one) is not waited
	// for five minutes.
	if retry, rerr := releaseIfHolderStopped(ctx, readerOrClient(r.APIReader, r.Client), lock, err); rerr != nil {
		err = rerr
	} else if retry {
		err = lock.Acquire(ctx, identity)
	}

	if err != nil {
		return nil, err
	}

	return lock, nil
}

// updateTransitioning recomputes conditionTransitioning on cluster from controlPlanes and persists it. It is
// called wherever the control planes are counted anyway (reconcileRemoval, Reconcile's main path), so it
// costs no extra List.
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

// refreshTransitioning recomputes cluster's Transitioning condition from a fresh count of its approved
// control planes. Split out of Reconcile only to keep that function's complexity down.
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

	// completesTransition is true: this removal can bring the live count back to the declared one (3->4->3's
	// last step), so it must run while the count is still even.
	lock, err := r.acquireDisruptiveLock(ctx, &cluster, m, machineLockIdentity(m), true)
	if err != nil {
		if shuttingDown, ok := errors.AsType[*shuttingDownError](err); ok {
			return r.removalDeferred(ctx, m, shuttingDown.reason(), shuttingDown.Error()+"; removal deferred")
		}
		if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
			return r.removalDeferred(ctx, m, reasonClusterLockHeld, lockHeldBy(held)+"; removal deferred")
		}
		return err
	}
	defer func() {
		if err := lock.Release(ctx, machineLockIdentity(m)); err != nil {
			log.Error(err, "releasing cluster lock")
		}
	}()

	// Recomputed under the lock: of two concurrent removals the second sees the first's finalizer (or
	// completed deletion), the race the webhook's admission-time count cannot see.
	// A worker only reaches here when a reset gave it the finalizer; it was never an
	// etcd member and is not counted toward quorum, so only the drain and the reset apply to it.
	isControlPlane := m.Spec.Role == roleControlPlane
	deadMember := false

	// A removal whose Reset Talos already accepted has passed the count check and the etcd step, and the
	// member is gone from etcd now: counting again would read that as a removal below the declared count and
	// refuse it, leaving the finalizer on a node that is already wiped.
	resumed := resetRequested(m) && resetAccepted(m)

	if isControlPlane {
		siblings, err := r.countApprovedControlPlanes(ctx, m)
		if err != nil {
			return err
		}

		if err := r.updateTransitioning(ctx, &cluster, siblings); err != nil {
			return err
		}

		if !resumed {
			var stop bool

			deadMember, stop, err = r.checkRemovalCounts(ctx, &cluster, m, siblings)
			if stop || err != nil {
				return err
			}
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

	// Such a removal only waits for its Node: the drain and the etcd step are done, and a node that is
	// wiping cannot be drained.
	if resumed {
		return r.finishReset(ctx, m, nodeName, nodeStep{})
	}

	// Storage evacuation comes before the drain (architecture.md, "Nodes"), through the evacuateMachine the
	// Talos upgrade uses. Its progress lives in the StorageEvacuation condition; a timeout stops the removal
	// here. A dead control plane has no kubelet to evict pods or move replicas: neither would ever finish.
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
		r.event(m, corev1.EventTypeWarning, reasonEvacuationFailed, msg)
		return errors.New(msg)
	}

	var drainErr error

	if !deadMember {
		stop := renewWhile(ctx, lock, machineLockIdentity(m), removalRenewInterval)
		drainErr = r.drainNode(ctx, nodeName)

		stop()
	}

	if drainErr != nil {
		return r.drainBlocked(ctx, m, drainErr)
	}

	if isControlPlane {
		if err := r.removeEtcdMember(ctx, &cluster, m, nodeName); err != nil {
			log.Error(err, "removing etcd member")

			return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonEtcdMemberRemoveFail, err.Error())
		}
	}

	return r.resetAndFinish(ctx, &cluster, m, nodeName)
}

// removalDeferred records why the removal cannot go on yet and asks to be tried again after nodeWaitInterval.
// Nothing else wakes the machine when the operation it waits for ends: no watch covers the lock's Lease, and
// the resync is hours away.
func (r *TalosMachineReconciler) removalDeferred(ctx context.Context, m *ichorv1alpha1.TalosMachine, reason, msg string) error {
	if err := r.removalCondition(ctx, m, metav1.ConditionFalse, reason, msg); err != nil {
		return err
	}

	return &removalWaitError{msg: msg}
}

// resetAndFinish is the end of a removal: without the reset annotation the finalizer just goes; with it
// the node is wiped, then its Kubernetes Node is deleted.
func (r *TalosMachineReconciler) resetAndFinish(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, nodeName string,
) error {
	log := logf.FromContext(ctx)

	if !resetRequested(m) {
		return r.completeRemoval(ctx, m, false, nodeStep{})
	}

	step := nodeStep{}

	if err := r.resetNode(ctx, cluster, m); err != nil {
		// A node that is down cannot be wiped, and holding its removal would keep a dead machine in the
		// cluster for good: it is removed unwiped, and the Event says so. A node that is up and fails
		// to reset holds the removal.
		if !r.nodeDown(ctx, nodeName) {
			log.Error(err, "resetting node")

			return r.removalCondition(ctx, m, metav1.ConditionFalse, reasonResetFailed, err.Error())
		}

		step.unwiped = err.Error()
	}

	return r.finishReset(ctx, m, nodeName, step)
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

	r.event(m, corev1.EventTypeWarning, reasonDeadMemberRemoved, msg)

	// Recorded, so a round that resumes after the member is gone from etcd still knows why it proceeds.
	return true, false, r.removalCondition(ctx, m, metav1.ConditionFalse, reasonDeadMemberRemoval, msg)
}

// removalNodeName is resolveNodeName for the departing machine: spec.hostname when set, then the
// name recorded in status, otherwise its own report (dialed here, bounded by etcdDialTimeout, and
// only once the node there has shown it is this machine) or the Kubernetes Node at its endpoint
// when it cannot be dialed. The error is only fatal for a control plane (the caller decides).
func (r *TalosMachineReconciler) removalNodeName(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (string, error) {
	if m.Spec.Hostname != "" {
		return m.Spec.Hostname, nil
	}

	// The name the machine itself reported when it was last read as this machine: unlike an answer from its
	// address now, it cannot be another node's.
	if m.Status.NodeName != "" {
		return m.Status.NodeName, nil
	}

	var talos TalosClient

	ctx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	if r.TalosClient != nil {
		if bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster); err == nil {
			if c, err := r.TalosClient(ctx, cluster, m, bundle); err == nil {
				defer closeTalosClient(c)

				// A node that cannot show it is this machine must not name the Node and etcd member to remove,
				// and its address is no better a way to find them.
				if err := confirmMachine(ctx, c, m); err != nil {
					return "", err
				}

				talos = c
			}
		}
	}

	return resolveNodeName(ctx, r.Client, talos, m)
}

// completeRemoval records the Removal condition's terminal success message, then clears
// machineRemovalFinalizer, in that order: a crash between the two leaves the finalizer held, and the next
// reconcile (removeEtcdMember and resetNode are idempotent) comes straight back here.
//
// reset is true only when this removal opted into's Reset step and it succeeded: the Warning Event
// that a decommissioned node still holds the cluster's PKI is skipped then, because Reset wiped
// the STATE partition those credentials lived on.
func (r *TalosMachineReconciler) completeRemoval(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, reset bool, step nodeStep,
) error {
	message := "the node was drained and removed"
	if m.Spec.Role == roleControlPlane {
		message = "removal passed the quorum, last-control-plane and declared-count checks, the node was " +
			"drained, and its etcd member was removed"
	}

	if reset {
		message += "; " + resetOutcome(m, step)
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

	r.warnAfterReset(m, step)

	controllerutil.RemoveFinalizer(m, machineRemovalFinalizer)

	return r.Update(ctx, m)
}

// resetOutcome words what the reset did, for the Removal condition.
func resetOutcome(m *ichorv1alpha1.TalosMachine, step nodeStep) string {
	if step.unwiped != "" {
		return fmt.Sprintf("the node did not answer (%s), so it was not wiped; %s", step.unwiped, step.note)
	}

	if step.unconfirmed {
		return fmt.Sprintf("the reset was sent, but the node did not answer in maintenance mode with UUID %s within %s, "+
			"so the wipe is not confirmed; %s", m.Spec.Identity.UUID, nodeDownWait, step.note)
	}

	after := "is rebooting back to maintenance mode"
	if powerOffRequested(m) {
		after = "was powered off"
	}

	return fmt.Sprintf("the node's STATE and EPHEMERAL partitions were wiped and it %s; %s", after, step.note)
}

// warnAfterReset emits the Warning Events of a reset removal that did not go cleanly: a node that was not
// wiped still holds the cluster's PKI, and a Node that was left is the operator's to delete.
func (r *TalosMachineReconciler) warnAfterReset(m *ichorv1alpha1.TalosMachine, step nodeStep) {
	if r.Recorder == nil {
		return
	}

	if step.unwiped != "" {
		r.Recorder.Eventf(m, corev1.EventTypeWarning, reasonNodeStillHasClusterCredentials,
			"%s %q was removed but did not answer (%s), so its disk was not wiped and still holds this cluster's PKI; "+
				"reset it before you repurpose or discard the hardware. %s", m.Spec.Role, m.Name, step.unwiped, step.note)
	}

	if step.unconfirmed {
		r.Recorder.Eventf(m, corev1.EventTypeWarning, reasonNodeStillHasClusterCredentials,
			"%s %q was removed, but it did not answer in maintenance mode within %s, so its wipe is not confirmed "+
				"and its disk may still hold this cluster's PKI; check it before you repurpose the hardware.",
			m.Spec.Role, m.Name, nodeDownWait)
	}

	if step.left {
		r.Recorder.Eventf(m, corev1.EventTypeWarning, reasonNodeNotDeleted, "%s %q: %s", m.Spec.Role, m.Name, step.note)
	}
}

// resetNode asks m's own node to reset: wipe STATE and EPHEMERAL and reboot back to maintenance
// mode. Idempotent for a fire-and-forget RPC: conditionRemoval already True means Talos accepted the request
// on an earlier reconcile, so a resumed reconcile does not fire a second one. The record is written after
// Reset's RPC acknowledges the request (not when the wipe finishes), the earliest point at which "a reset
// was requested" is true.
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

	if err := confirmMachine(resetCtx, talos, m); err != nil {
		return err
	}

	if err := talos.Reset(resetCtx, m.Annotations[resetWipeModeAnnotation], powerOffRequested(m)); err != nil {
		return fmt.Errorf("reset request to %q failed: %w", m.Name, err)
	}

	return r.removalCondition(ctx, m, metav1.ConditionTrue, reasonResetRequested,
		"reset requested: the node's STATE and EPHEMERAL partitions are being wiped")
}

// removeEtcdMember removes m from etcd, preferring m's own graceful EtcdLeaveCluster (talosctl says to
// always prefer etcd leave) and falling back to EtcdRemoveMemberByID from a surviving sibling only when m
// cannot be dialed or cannot leave on its own (architecture.md's forceRemove case).
//
// Idempotent: both branches check the live member list first and return nil if m is already gone, which is
// also how it resumes after a crash between the removal and the finalizer clearing in completeRemoval.
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

// trySelfLeave attempts removeEtcdMember's first-choice path (self-dial, list, leave) and reports whether m
// is now confirmed gone from etcd, because it already was or EtcdLeaveCluster just succeeded. false covers
// every other outcome (unreachable, listed but leave failed): removeEtcdMember falls back to a survivor.
// etcdDialTimeout bounds the whole attempt.
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

	// Another node answering at m's address would list its own member and be told to leave etcd.
	if err := confirmMachine(selfCtx, self, m); err != nil {
		logf.FromContext(ctx).Info("not asking the node at the node's address to leave etcd", "reason", err.Error())

		return false
	}

	members, err := self.EtcdMemberList(selfCtx)
	if err != nil {
		return false
	}

	if !etcdMemberPresent(members, nodeName) {
		return true // already gone: the resumption case above
	}

	return self.EtcdLeaveCluster(selfCtx) == nil
}

// removeEtcdMemberFromSurvivor is removeEtcdMember's fallback: dial another approved control plane of the
// cluster and remove m by ID from its view of etcd. No survivor to find or dial is an error:
// reconcileRemoval holds the finalizer and retries rather than treating it as "already removed".
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

// findEtcdSurvivor finds another approved control plane of m's cluster to dial for EtcdRemoveMemberByID.
// It reads through r.APIReader like deleteguard.CountApprovedControlPlanes: a cached read could still show
// a sibling that started its own removal moments ago.
func (r *TalosMachineReconciler) findEtcdSurvivor(
	ctx context.Context, m *ichorv1alpha1.TalosMachine,
) (*ichorv1alpha1.TalosMachine, error) {
	siblings, err := deleteguard.ApprovedMachines(ctx, r.APIReader, m.Namespace, m.Spec.ClusterRef.Name,
		deleteguard.MachineFilter{Role: roleControlPlane})
	if err != nil {
		return nil, err
	}

	if i := slices.IndexFunc(siblings, func(s ichorv1alpha1.TalosMachine) bool { return s.Name != m.Name }); i >= 0 {
		return &siblings[i], nil
	}

	return nil, fmt.Errorf("no surviving control plane found to remove %q from etcd", m.Name)
}

// etcdMemberPresent reports whether hostname appears in members.
func etcdMemberPresent(members []EtcdMember, hostname string) bool {
	_, found := etcdMemberID(members, hostname)

	return found
}

// etcdMemberID finds the etcd member ID matching hostname, the Node's own hostname (an empty one never
// matches: nothing here can discover a name Talos derived, so the caller holds the finalizer and retries).
func etcdMemberID(members []EtcdMember, hostname string) (uint64, bool) {
	if hostname == "" {
		return 0, false
	}

	if i := slices.IndexFunc(members, func(e EtcdMember) bool { return e.Hostname == hostname }); i >= 0 {
		return members[i].ID, true
	}

	return 0, false
}

// closeTalosClient closes talos if it is an io.Closer (the test fake has nothing to close).
func closeTalosClient(talos TalosClient) {
	if closer, ok := talos.(io.Closer); ok {
		_ = closer.Close()
	}
}

// removalCondition records reconcileRemoval's verdict as the Removal condition (distinct from Ready, which
// the configuration reconcile owns), so a restart resumes from status (architecture.md's chaos cases).
func (r *TalosMachineReconciler) removalCondition(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, status metav1.ConditionStatus, reason, message string,
) error {
	return setMachineCondition(ctx, readerOrClient(r.APIReader, r.Client), r.Client, m, metav1.Condition{
		Type: conditionRemoval, Status: status, Reason: reason, Message: message,
	})
}

// checkControlPlaneRemoval runs every refusal architecture.md's "Nodes" names for the etcd-member-removal
// step against currentControlPlanes control planes (including the one being removed) and the declared
// desiredCount: last-control-plane and quorum, then the declared count. Only called for a control plane.
func checkControlPlaneRemoval(currentControlPlanes, desiredCount int) error {
	if err := deleteguard.CheckControlPlaneQuorum(currentControlPlanes); err != nil {
		return err
	}
	return deleteguard.CheckControlPlaneCount(currentControlPlanes, desiredCount)
}

// countApprovedControlPlanes counts, as the webhook's ValidateDelete does, every approved control plane of
// m's cluster, m itself included but not another one being deleted, through
// r.APIReader to avoid the cached-read trap deleteguard.CountApprovedControlPlanes documents.
func (r *TalosMachineReconciler) countApprovedControlPlanes(ctx context.Context, m *ichorv1alpha1.TalosMachine) (int, error) {
	cps, err := deleteguard.ApprovedMachines(ctx, r.APIReader, m.Namespace, m.Spec.ClusterRef.Name,
		deleteguard.MachineFilter{Role: roleControlPlane, KeepDeleting: m.Name})

	return len(cps), err
}

// drainNode cordons and drains the Kubernetes Node named hostname. An empty hostname, or no Node by that
// name, is not an error: a control plane that was approved but never joined has nothing to drain.
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

	// Mark the cordon as Ichor's own so it can be undone when the machine joins again (a Node an
	// administrator cordoned is left alone).
	if !node.Spec.Unschedulable {
		patch := client.MergeFrom(node.DeepCopy())
		metav1.SetMetaDataAnnotation(&node.ObjectMeta, cordonedByIchorAnnotation, "removal")

		if err := r.Patch(ctx, &node, patch); err != nil {
			return fmt.Errorf("marking the cordon of node %q: %w", hostname, err)
		}
	}

	// Bounded like a rollout's drain: an unbounded one holds the single worker and the cluster lock for as
	// long as a PodDisruptionBudget refuses an eviction. Past the bound the caller records DrainFailed and
	// retries after drainRetryInterval. A pod already terminating for a minute (its kubelet is gone) is not
	// waited for.
	h := newDrainHelper(ctx, r.KubeClient, drainStepTimeout)
	h.SkipWaitForDeleteTimeoutSeconds = removalSkipWaitSeconds

	return cordonAndDrain(h, &node)
}

// renewWhile calls lock.Renew every interval until stop is called. A failed renew is logged, not escalated:
// the caller's next clusterlock call surfaces a lost lock as a HeldError. interval is a parameter so tests
// can use a short one.
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

// markNodeReset annotates the Kubernetes Node of a machine that was just reset, so discovery does
// not keep treating its address as a member. Best effort: without it the Node is still deleted by
// hand as before, so a failure is logged, not returned.
func (r *TalosMachineReconciler) markNodeReset(ctx context.Context, nodeName string) {
	if nodeName == "" {
		return
	}

	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		return
	}

	if _, ok := node.Annotations[nodeResetByIchorAnnotation]; ok {
		return
	}

	patch := client.MergeFrom(node.DeepCopy())
	metav1.SetMetaDataAnnotation(&node.ObjectMeta, nodeResetByIchorAnnotation, time.Now().UTC().Format(time.RFC3339))

	if err := r.Patch(ctx, node, patch); err != nil {
		logf.FromContext(ctx).Error(err, "marking the reset node", "node", nodeName)
	}
}

// uncordonRejoinedNode undoes the cordon of a removal when the machine behind nodeName has joined
// again: a reset machine's Node is kept, still cordoned by the drain. Only a Ready Node that carries
// Ichor's reset annotation or Ichor's cordon marker is touched, so a cordon an administrator made
// stays. A missing Node is not an error (the kubelet has not registered yet).
func (r *TalosMachineReconciler) uncordonRejoinedNode(ctx context.Context, nodeName string) error {
	if nodeName == "" {
		return nil
	}

	var node corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		return client.IgnoreNotFound(err)
	}

	_, reset := node.Annotations[nodeResetByIchorAnnotation]
	_, cordoned := node.Annotations[cordonedByIchorAnnotation]

	if (!reset && !cordoned) || !nodeIsReady(&node) {
		return nil
	}

	// A rollout's own drain owns its marker while it runs (drain.go); leave that cordon to it.
	if _, draining := node.Annotations[drainStartedAnnotation]; draining {
		return nil
	}

	patch := client.MergeFrom(node.DeepCopy())
	node.Spec.Unschedulable = false
	delete(node.Annotations, nodeResetByIchorAnnotation)
	delete(node.Annotations, cordonedByIchorAnnotation)

	if err := r.Patch(ctx, &node, patch); err != nil {
		return fmt.Errorf("uncordoning node %q after its node joined again: %w", nodeName, err)
	}

	logf.FromContext(ctx).Info("uncordoned a node whose node joined again", "node", nodeName)

	return nil
}
