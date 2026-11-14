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
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// operationRequeueInterval is how often TalosOperationReconciler re-checks an in-progress machine
// step, or retries acquiring a held/transitioning lock: steps take minutes, so this is "soon enough
// that status doesn't look stalled," not a tight poll.
const operationRequeueInterval = 30 * time.Second

// conditionOperationPaused is the TalosOperation condition that says why setPhase paused it.
const conditionOperationPaused = "Paused"

const (
	reasonOperationNoExecutor    = "NoExecutor"
	reasonOperationTransitioning = "ClusterTransitioning"
	reasonOperationLockHeld      = "ClusterLockHeld"
	reasonOperationPaused        = "OperatorPaused"
	reasonOperationStepFailed    = "StepFailed"
	reasonOperationAdvancing     = "Advancing"
	reasonOperationComplete      = "Complete"
	reasonMachineStarted         = "MachineStarted"
	reasonMachineFinished        = "MachineFinished"
	reasonOperationResumed       = "OperationResumed"
)

// AdvanceMachineFunc performs one disruptive step for a single machine
// within an in-progress TalosOperation and reports whether that machine's
// step is now done. Declared here, in the consumer, rather than in
// pkg/upgrade: pre-check/execution interfaces stay undeclared on
// purpose because the consumer didn't exist yet. This reconciler is that
// consumer; each upgrade kind's own implementation -- calling Talos's
// Upgrade API, driving a Kubernetes upgrade, launching Ichor's installer
// Job -- is separate, later work, wired in by setting
// TalosOperationReconciler.Advance (the machine is nil for kind ichor,
// which has none). The state machine here never branches
// on spec.kind itself.
type AdvanceMachineFunc func(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation,
) (done bool, err error)

// TalosOperationReconciler implements the backbone every upgrade kind
// shares (docs/design/architecture.md, "Upgrades"): the cluster lock, the
// status-held state machine, pause-on-failure with no automatic
// cluster-wide rollback, spec.paused, maxUnavailable for workers against
// one-control-plane-at-a-time, the per-machine hold annotation, and
// label-based ordering.
//
// A separate controller from TalosClusterReconciler and
// TalosMachineReconciler, for the same reason CNIHandoverReconciler is: each controller gets its own work queue, so
// a slow,
// unrelated reconcile in one never delays this one.
type TalosOperationReconciler struct {
	client.Client

	// APIReader is used by acquireDisruptiveLock alone, for the
	// transitioning-control-plane count that gates whether this operation
	// is allowed to acquire the cluster lock at all -- the same
	// cached-read trap deleteguard.CountApprovedControlPlanes documents. That check is a safety gate on a disruptive
	// action, so it has to see the live count, not a stale one. Wired
	// from mgr.GetAPIReader() in cmd/main.go.
	//
	// candidateMachines below deliberately does NOT use this field --
	// see its own doc comment for why a cached read is fine there.
	APIReader client.Reader

	// Advance performs one disruptive step for a single machine. nil (the
	// zero value) means no execution is wired up yet in this process --
	// Reconcile pauses with reasonOperationNoExecutor rather than
	// panicking, since each upgrade kind's own executor is later,
	// separate work.
	Advance AdvanceMachineFunc

	// lockDuration and renewInterval are the cluster lock's lease length and how often the Advance loop
	// renews it. Zero means clusterlock.DefaultDuration and operationRenewInterval; tests shorten them.
	lockDuration  time.Duration
	renewInterval time.Duration

	// TalosClient, ControllerNamespace and NodeName serve the shutdown kind
	// alone (shutdown.go). TalosClient opens the Talos API of one
	// machine; ControllerNamespace is where the secrets bundle lives; NodeName
	// is the Kubernetes node this controller runs on ("" when it runs outside
	// the cluster it manages), so its own node can be stopped last.
	TalosClient         func(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle) (TalosClient, error)
	ControllerNamespace string
	NodeName            string

	// NodesReady and Recorder serve the ca-rotation kind alone (ca_rotation.go).
	// NodesReady reads which Kubernetes Nodes are not Ready; Recorder raises the BundleBackupStale
	// Event (nil skips the Event, not the condition).
	NodesReady NodesReadyFunc
	Recorder   record.EventRecorder
	// RootCAPublished and ClientSettleDelay keep the Dropping stage from starting while in-cluster
	// clients still hold the old CA (ca_rotation.go): the first reads which namespaces' kube-root-ca.crt
	// lacks the new Kubernetes CA (nil skips the check), the second is how long pods then get to
	// pick up the projected file (0 means caClientSettleDelay).
	RootCAPublished   RootCAPublishedFunc
	ClientSettleDelay time.Duration
	// AggregatorCAPublished reads whether kube-apiserver publishes the new aggregator CA to extension
	// API servers before the front-proxy certificate changes CA (ca_rotation_targets.go);
	// nil skips the check. ClientSettleDelay is the wait after it.
	AggregatorCAPublished AggregatorCAPublishedFunc
	// AggregatorAuthReset makes kube-apiserver drop the old aggregator CA from extension-apiserver-authentication
	// when the rotation ends (ca_rotation_targets.go); nil skips it.
	AggregatorAuthReset AggregatorAuthResetFunc

	// Reencrypt and LegacyTokens serve the key-rotation kind alone (key_rotation.go): the first
	// stores every Secret under the current secretbox key, the second finds token Secrets the old
	// service-account key signed. Both reach the managed cluster through an admin kubeconfig a Talos node issues.
	Reencrypt    ReencryptSecretsFunc
	LegacyTokens LegacyTokensFunc

	// KubeClient, Evacuator serve the node kind alone (node_action.go): KubeClient
	// cordons, drains and reads Node readiness live (nil skips the drain and reads the cache),
	// Evacuator is the storage hook run before a node is taken away (nil skips it).
	KubeClient kubernetes.Interface
	Evacuator  StorageEvacuator

	// NewStore builds the object store an etcd restore reads its snapshot from (etcd_restore.go); nil means
	// etcdbackup.NewS3Store.
	NewStore func(etcdbackup.S3Config) (etcdbackup.Store, error)

	// launchTail replaces the goroutine that runs the destructive part of an etcd restore; tests set it.
	launchTail func(*restoreJob)
}

// finalizerSteps runs the finalizer logic of the kinds that hold one: the two rotations and the etcd restore.
// handled is true when the reconcile has nothing more to do.
func (r *TalosOperationReconciler) finalizerSteps(ctx context.Context, op *ichorv1alpha1.TalosOperation) (handled bool, err error) {
	for _, step := range []func(context.Context, *ichorv1alpha1.TalosOperation) (bool, error){
		r.caRotationFinalizerStep, r.keyRotationFinalizerStep, r.etcdRestoreFinalizerStep,
	} {
		if handled, err := step(ctx, op); handled || err != nil {
			return handled, err
		}
	}

	return false, nil
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosoperations,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosetcdbackups,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosoperations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives op's state machine one step. Every step of its own
// progress is read from, and written back to, op.status (never kept in
// memory) specifically so a killed and restarted controller resumes from
// exactly where op.status says it was, with no separate recovery path
// (docs/design/architecture.md, "Upgrades": "the status-held state
// machine").
func (r *TalosOperationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var op ichorv1alpha1.TalosOperation
	if err := r.Get(ctx, req.NamespacedName, &op); err != nil {
		if apierrors.IsNotFound(err) {
			// An etcd restore this process started has rolled the API back past its own operation.
			r.resumeRolledBack(ctx, req.NamespacedName)
		}

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if handled, err := r.finalizerSteps(ctx, &op); handled || err != nil {
		return ctrl.Result{}, err
	}

	if err := r.dropStaleRetry(ctx, &op); err != nil {
		return ctrl.Result{}, err
	}

	if op.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
		return ctrl.Result{}, nil // done; the cluster lock was already released when it got here
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.ClusterRef.Name}, &cluster); err != nil {
		return ctrl.Result{}, err
	}

	// Phases that hold no cluster lock are handled before it is taken.
	if res, handled, err := r.restingPhase(ctx, &op, &cluster); handled {
		return res, err
	}

	// An etcd restore that has started is mostly driven without the cluster lock: the lock is a
	// Kubernetes object, and the restore takes the Kubernetes API away and rolls it back.
	if etcdRestoreStarted(&op) {
		return r.advanceEtcdRestore(ctx, &op, &cluster)
	}

	lock, err := r.acquireDisruptiveLock(ctx, &cluster, &op)
	if err != nil {
		return r.lockUnavailable(ctx, &op, err)
	}

	if op.Spec.Paused {
		return ctrl.Result{}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationPaused, "spec.paused is true")
	}

	// A step already failed: stay paused and never retry it automatically; only an explicit
	// retry request runs it again. Nothing here undoes a machine already marked
	// Completed either -- "pause-on-failure (no automatic cluster-wide rollback)".
	if op.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused && op.Status.FailureReason != "" {
		if retried, err := r.consumeRetry(ctx, &op); err != nil || !retried {
			return ctrl.Result{}, err
		}
	}

	if res, handled, err := r.advanceOwnStateMachine(ctx, &op, &cluster, lock); handled {
		return res, err
	}

	if r.Advance == nil {
		return ctrl.Result{}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationNoExecutor, "no node executor is wired up for this controller")
	}

	// An Ichor self-upgrade touches no machine: its executor watches one installer Job, so the
	// operation carries no machine list. A Cilium chart upgrade is the same shape.
	if machineless(&op) {
		return r.advanceIchor(ctx, &op, &cluster, lock)
	}

	controlPlanes, workers, err := r.candidateMachines(ctx, &cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// A config rollout visits only machines with a pending change (the same selection the plan uses), plus
	// any it already started: a machine whose change was applied no longer lists paths but is still ours.
	controlPlanes, workers = configSelection(&op, controlPlanes, workers)

	// A config rollout reboots nodes, so the machine this controller runs on goes last: its reboot
	// stops the controller, which resumes from the status and has nothing left to wait for.
	// A Kubernetes upgrade does the same in its Kubelets stage, where the node is drained first.
	var own []*ichorv1alpha1.TalosMachine
	heldCPs, heldWorkers := controlPlanes, workers
	if slices.Contains([]string{ichorv1alpha1.TalosOperationKindConfig, ichorv1alpha1.TalosOperationKindKubernetes}, op.Spec.Kind) {
		heldCPs, heldWorkers, own = r.holdOwnNodeLast(ctx, &cluster, controlPlanes, workers)
	}

	// Control planes go one at a time, and only once every one of them is
	// done do workers start -- never interleaved with workers.
	var toAdvance []*ichorv1alpha1.TalosMachine
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindKubernetes {
		toAdvance = kubernetesBatch(&op, controlPlanes, heldCPs, heldWorkers, own)
	} else {
		toAdvance = nextBatch(heldCPs, heldWorkers, own, op.Status.Machines, op.Spec.MaxUnavailable)
	}

	if len(toAdvance) == 0 {
		if err := lock.Release(ctx, operationLockIdentity(&op)); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhaseCompleted,
			reasonOperationComplete, "every eligible node finished")
	}

	// The lock is renewed for as long as this loop runs: one Upgrade call can outlast the lease.
	// stepRenewUntil bounds the renewal per machine, so a stuck step lets the lease lapse instead of
	// holding the cluster for ever.
	identity := operationLockIdentity(&op)
	if err := lock.Renew(ctx, identity); err != nil {
		return renewRefusal(ctx, err)
	}

	loopCtx, cancelLoop := context.WithCancelCause(ctx)
	defer cancelLoop(nil)

	var renewUntil atomic.Int64

	stopRenew := renewLockWhile(loopCtx, cancelLoop, lock, identity, r.renewEvery(), &renewUntil)
	defer stopRenew()

	requeue := false

	for _, m := range toAdvance {
		renewUntil.Store(stepRenewUntil(&op, m).UnixNano())

		done, err := r.Advance(loopCtx, &cluster, m, &op)
		if held := lockLost(loopCtx); held != nil {
			return renewRefusal(ctx, held)
		}

		if err != nil {
			// Pause on failure, without undoing anything already
			// Completed in op.Status.Machines above -- there is no
			// automatic cluster-wide rollback.
			r.markMachine(&op, m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)

			return ctrl.Result{}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused,
				reasonOperationStepFailed, fmt.Sprintf("node %q: %v", m.Name, err))
		}

		phase := ichorv1alpha1.TalosOperationMachinePhaseInProgress
		if done {
			phase = ichorv1alpha1.TalosOperationMachinePhaseCompleted
		} else {
			requeue = true
		}

		r.markMachine(&op, m.Name, phase)
	}

	result := ctrl.Result{}
	if requeue {
		result.RequeueAfter = operationRequeueInterval
	}

	return result, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhaseInProgress,
		reasonOperationAdvancing, advancingMessage(&op, len(toAdvance)))
}

// renewEvery is the renewal cadence of the Advance loop's lock renewal.
func (r *TalosOperationReconciler) renewEvery() time.Duration {
	if r.renewInterval != 0 {
		return r.renewInterval
	}

	return operationRenewInterval
}

// operationRenewInterval is how often the Advance loop renews the cluster lock. One minute is well inside
// clusterlock.DefaultDuration, so a renewal may be late once and the lock still holds.
const operationRenewInterval = time.Minute

// operationRenewCeiling bounds the renewal of a step that has no step clock yet: the evacuation, drain and
// Upgrade call before the node is acknowledged. Each has its own timeout (10 minutes by default), and the
// ceiling is their sum.
// shortcut: a TalosStorage evacuationTimeout above its 10m default outlasts this ceiling, so the lease lapses
// mid-evacuation on such a cluster; drop when the ceiling follows the evacuation's own clock.
const operationRenewCeiling = 30 * time.Minute

// stepRenewUntil is the last moment the lock is renewed for m's step. A step the node has acknowledged
// (its clock condition is True) is renewed only until that clock passes its verify timeout, the same
// bound that fails the step; a step with no clock yet gets operationRenewCeiling from now.
func stepRenewUntil(op *ichorv1alpha1.TalosOperation, m *ichorv1alpha1.TalosMachine) time.Time {
	clock, verify := "", time.Duration(0)

	switch op.Spec.Kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		clock, verify = conditionTalosUpgrade, talosUpgradeVerifyTimeout
	case ichorv1alpha1.TalosOperationKindKubernetes:
		clock, verify = conditionKubernetesUpgrade, kubernetesVerifyTimeout
	}

	if clock != "" && apimeta.IsStatusConditionTrue(m.Status.Conditions, clock) {
		return apimeta.FindStatusCondition(m.Status.Conditions, clock).LastTransitionTime.Add(verify)
	}

	return time.Now().Add(operationRenewCeiling)
}

// lockLost returns the *clusterlock.HeldError that cancelled the Advance loop's context, or nil.
func lockLost(loopCtx context.Context) error {
	if held, ok := errors.AsType[*clusterlock.HeldError](context.Cause(loopCtx)); ok {
		return held
	}

	return nil
}

// renewLockWhile renews lock every interval until stop is called, but only while the clock is before
// *until: past that bound the renewal is skipped, so a stuck step lets the lease lapse. A renewal refused
// with a *clusterlock.HeldError means another holder has the lock, so cancel is called with it and the loop
// stops before it touches another node. Any other renewal error is logged and retried on the next tick,
// while the lease still has time.
func renewLockWhile(
	ctx context.Context, cancel context.CancelCauseFunc, lock *clusterlock.Lock, identity string,
	interval time.Duration, until *atomic.Int64,
) (stop func()) {
	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if time.Now().UnixNano() >= until.Load() {
					continue
				}

				err := lock.Renew(ctx, identity)
				if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
					cancel(held)

					return
				}

				if err != nil {
					logf.FromContext(ctx).Error(err, "renewing cluster lock")
				}
			}
		}
	}()

	return func() { close(done) }
}

// consumeRetry acts on a retry request (OperationRetryAnnotation set to "true") for a failed
// operation: it records the request in status.lastRetry, clears failureReason and puts the operation
// back to InProgress, so the caller runs the failed machine's step again (that machine is still
// InProgress in status.machines), then removes the annotation, which is what makes the next "true" a
// new request. It reports whether it acted; any other value is ignored. If the removal fails the
// operation is already InProgress, so dropStaleRetry removes the leftover on the next reconcile.
func (r *TalosOperationReconciler) consumeRetry(ctx context.Context, op *ichorv1alpha1.TalosOperation) (bool, error) {
	if op.Annotations[ichorv1alpha1.OperationRetryAnnotation] != ichorv1alpha1.OperationRetryRequested {
		return false, nil
	}

	now := metav1.Now()
	op.Status.LastRetry = &now

	if err := r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhaseInProgress,
		reasonOperationAdvancing, "retrying the failed step"); err != nil {
		return false, err
	}

	return true, r.removeRetryAnnotation(ctx, op)
}

// dropStaleRetry removes a retry request ("true") from an operation that is not waiting on a failed
// step: it has nothing to retry, and a request left behind would rerun the next failure unasked. An
// operation paused by spec.paused after a failure keeps it; the request is acted on after the resume.
func (r *TalosOperationReconciler) dropStaleRetry(ctx context.Context, op *ichorv1alpha1.TalosOperation) error {
	failed := op.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused && op.Status.FailureReason != ""
	if failed || !op.DeletionTimestamp.IsZero() ||
		op.Annotations[ichorv1alpha1.OperationRetryAnnotation] != ichorv1alpha1.OperationRetryRequested {
		return nil
	}

	return r.removeRetryAnnotation(ctx, op)
}

// removeRetryAnnotation deletes the request with a metadata patch that fails on a concurrent edit.
func (r *TalosOperationReconciler) removeRetryAnnotation(ctx context.Context, op *ichorv1alpha1.TalosOperation) error {
	patch := client.MergeFromWithOptions(op.DeepCopy(), client.MergeFromWithOptimisticLock{})
	delete(op.Annotations, ichorv1alpha1.OperationRetryAnnotation)

	return client.IgnoreNotFound(r.Patch(ctx, op, patch))
}

// lockUnavailable turns a refusal of acquireDisruptiveLock into the pause that explains it; the
// pause lifts itself when the cause does. Any other error is returned for a retry.
func (r *TalosOperationReconciler) lockUnavailable(ctx context.Context, op *ichorv1alpha1.TalosOperation, err error) (ctrl.Result, error) {
	if shuttingDown, ok := errors.AsType[*shuttingDownError](err); ok {
		return ctrl.Result{RequeueAfter: operationRequeueInterval},
			r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused, shuttingDown.reason(), shuttingDown.Error())
	}

	if transitioning, ok := errors.AsType[*transitioningError](err); ok {
		return ctrl.Result{RequeueAfter: operationRequeueInterval},
			r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused, reasonOperationTransitioning, transitioning.Error())
	}

	if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
		return ctrl.Result{RequeueAfter: operationRequeueInterval}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationLockHeld, lockHeldMessage(held))
	}

	return ctrl.Result{}, err
}

// operationLockIdentityPrefix starts the cluster lock identity of every TalosOperation.
const operationLockIdentityPrefix = controllerIdentity + "/op/"

// operationLockIdentity is the identity op takes the cluster lock under. It is derived from the
// operation's namespace and name, so it is the same across reconciles and controller restarts (the
// holder re-enters its own lock, which resumption needs) and different for every other operation,
// which therefore waits (clusterlock.Acquire lets only the same identity back in). The UID is not
// used: an etcd restore rolls the API back, and the restored operation must find its own lock.
func operationLockIdentity(op *ichorv1alpha1.TalosOperation) string {
	return operationLockIdentityPrefix + op.Namespace + "/" + op.Name
}

// lockHeldMessage words a refused Acquire; a lock held by a TalosOperation or a TalosMachine names it.
func lockHeldMessage(held *clusterlock.HeldError) string {
	if who := describeLockHolder(held.Holder); who != "" {
		return fmt.Sprintf("the cluster lock is held by %s; this operation starts when it finishes", who)
	}

	return held.Error()
}

// renewRefusal turns the failure of a lock.Renew into a reconcile result. Losing the lock to
// another reconcile of this controller is expected contention, not a failure: it is logged once at
// V(1) and the operation is requeued, where acquireDisruptiveLock reports the holder in the Paused
// condition. Any other error is returned for a retry.
func renewRefusal(ctx context.Context, err error) (ctrl.Result, error) {
	if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
		logf.FromContext(ctx).V(1).Info("cluster lock not held by this reconcile; requeueing", "holder", held.Holder)
		return ctrl.Result{RequeueAfter: operationRequeueInterval}, nil
	}

	return ctrl.Result{}, err
}

// advanceOwnStateMachine runs the kinds that are not a rollout over the machines but a state machine of
// their own: shutdown, node, etcd-restore, ca-rotation and key-rotation. handled is false for every other kind.
func (r *TalosOperationReconciler) advanceOwnStateMachine(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) (res ctrl.Result, handled bool, err error) {
	switch op.Spec.Kind {
	case ichorv1alpha1.TalosOperationKindShutdown:
		res, err = r.advanceShutdown(ctx, op, cluster, lock)
	case ichorv1alpha1.TalosOperationKindNode:
		res, err = r.advanceNode(ctx, op, cluster, lock)
	case ichorv1alpha1.TalosOperationKindEtcdRestore:
		res, err = r.beginEtcdRestore(ctx, op, cluster, lock)
	case ichorv1alpha1.TalosOperationKindCARotation:
		res, err = r.advanceCARotation(ctx, op, cluster, lock)
	case ichorv1alpha1.TalosOperationKindKeyRotation:
		res, err = r.advanceKeyRotation(ctx, op, cluster, lock)
	default:
		return ctrl.Result{}, false, nil
	}

	return res, true, err
}

func advancingMessage(op *ichorv1alpha1.TalosOperation, n int) string {
	if op.Status.Kubernetes != nil && op.Status.Kubernetes.Stage != "" {
		return fmt.Sprintf("stage %s: %d node(s) advancing", op.Status.Kubernetes.Stage, n)
	}

	return fmt.Sprintf("%d node(s) advancing", n)
}

// restingPhase handles the phases that hold no cluster lock. A shutdown that has asked every machine
// to stop only waits for the operator to power them on (shutdown.go); so does a node
// shutdown, and a node reset waits for the removal flow, which takes the cluster lock itself.
func (r *TalosOperationReconciler) restingPhase(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster,
) (res ctrl.Result, handled bool, err error) {
	kind := op.Spec.Kind
	if (kind == ichorv1alpha1.TalosOperationKindShutdown || kind == ichorv1alpha1.TalosOperationKindNode) &&
		op.Status.Phase == ichorv1alpha1.TalosOperationPhaseShutDown {
		res, err = r.awaitPowerOn(ctx, op, cluster)

		return res, true, err
	}

	if st := op.Status.Node; kind == ichorv1alpha1.TalosOperationKindNode && st != nil &&
		st.Step == ichorv1alpha1.NodeStepRemoving && op.Status.FailureReason == "" {
		res, err = r.awaitRemoval(ctx, op)

		return res, true, err
	}

	return ctrl.Result{}, false, nil
}

// setMachineStatus records name's phase in op.status.machines, replacing
// any existing entry for the same name rather than appending a duplicate. It stamps startedAt
// when the machine first appears and finishedAt when it reaches Completed, and reports whether
// the entry is new or its phase changed.
func setMachineStatus(op *ichorv1alpha1.TalosOperation, name, phase string) bool {
	now := metav1.Now()

	for i := range op.Status.Machines {
		m := &op.Status.Machines[i]
		if m.Name != name {
			continue
		}

		if m.Phase == phase {
			return false
		}

		m.Phase = phase
		if phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted {
			m.FinishedAt = &now
		}

		return true
	}

	entry := ichorv1alpha1.TalosOperationMachineStatus{Name: name, Phase: phase, StartedAt: &now}
	if phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted {
		entry.FinishedAt = &now
	}

	op.Status.Machines = append(op.Status.Machines, entry)

	return true
}

// markMachine is setMachineStatus plus an Event on the operation when the machine starts or
// finishes its step, so the operation's activity log shows it.
func (r *TalosOperationReconciler) markMachine(op *ichorv1alpha1.TalosOperation, name, phase string) {
	if !setMachineStatus(op, name, phase) {
		return
	}

	if phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted {
		r.operationEvent(op, corev1.EventTypeNormal, reasonMachineFinished, "node "+name+" finished")
	} else {
		r.operationEvent(op, corev1.EventTypeNormal, reasonMachineStarted, "node "+name+" started")
	}
}

// operationEvent records an Event on op when a Recorder is wired. Messages carry names and
// reasons only, never secret material.
func (r *TalosOperationReconciler) operationEvent(op client.Object, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(op, eventType, reason, message)
	}
}

// setPhase persists op's phase, reason and message (status.message while InProgress), then writes status.
func (r *TalosOperationReconciler) setPhase(ctx context.Context, op *ichorv1alpha1.TalosOperation, phase, reason, message string) error {
	previous := op.Status.Phase
	previousPauseReason := ""

	if c := apimeta.FindStatusCondition(op.Status.Conditions, conditionOperationPaused); c != nil {
		previousPauseReason = c.Reason
	}

	op.Status.Phase = phase

	if phase == ichorv1alpha1.TalosOperationPhaseCompleted && op.Status.FinishedAt == nil {
		now := metav1.Now()
		op.Status.FinishedAt = &now
	}

	// status.message is what an InProgress operation is doing now; a pause says why elsewhere, and a
	// finished operation says nothing more. An unchanged value makes the update below a no-op.
	op.Status.Message = ""
	if phase == ichorv1alpha1.TalosOperationPhaseInProgress {
		op.Status.Message = message
	}

	if isStepFailure(reason) {
		op.Status.FailureReason = message
	} else if phase != ichorv1alpha1.TalosOperationPhasePaused {
		op.Status.FailureReason = ""
	}

	// Every pause says why. failureReason cannot carry the transient causes (a transitioning
	// cluster, a held lock, spec.paused): it also means "a step failed, never retry" to this
	// controller, the CLI and the UI, and would turn a self-resuming pause into a stuck one.
	if phase == ichorv1alpha1.TalosOperationPhasePaused {
		apimeta.SetStatusCondition(&op.Status.Conditions, metav1.Condition{
			Type: conditionOperationPaused, Status: metav1.ConditionTrue, Reason: reason, Message: message,
		})
	} else {
		apimeta.RemoveStatusCondition(&op.Status.Conditions, conditionOperationPaused)
	}

	if err := r.Status().Update(ctx, op); err != nil {
		return err
	}

	r.phaseEvent(op, previous, previousPauseReason, phase, reason, message)

	return nil
}

// isStepFailure is true for the pause reasons that mean "a step failed, never retry automatically": they set
// status.failureReason, which the CLI, the UI and the retry request key on. The installer image
// refusals are failures of the Ichor upgrade's first step.
func isStepFailure(reason string) bool {
	return reason == reasonOperationStepFailed || reason == reasonInstallerImageUnresolved || reason == reasonInstallerImageUnverified
}

// phaseEvent records the Event for a phase transition: a pause (once per reason), a resume out of a
// pause, and completion. The steady InProgress/Advancing reconcile records nothing.
func (r *TalosOperationReconciler) phaseEvent(op *ichorv1alpha1.TalosOperation, previous, previousPauseReason, phase, reason, message string) {
	switch {
	case phase == ichorv1alpha1.TalosOperationPhasePaused && (previous != phase || previousPauseReason != reason):
		eventType := corev1.EventTypeNormal
		if isStepFailure(reason) {
			eventType = corev1.EventTypeWarning
		}

		r.operationEvent(op, eventType, reason, "paused: "+message)
	case phase == ichorv1alpha1.TalosOperationPhaseInProgress && previous == ichorv1alpha1.TalosOperationPhasePaused:
		r.operationEvent(op, corev1.EventTypeNormal, reasonOperationResumed, "resumed")
	case phase == ichorv1alpha1.TalosOperationPhaseCompleted && previous != phase:
		r.operationEvent(op, corev1.EventTypeNormal, reason, message)
	}
}

// candidateMachines lists every approved, not-being-deleted TalosMachine of cluster
// (deleteguard.ApprovedMachines) without TalosMachineUpgradeHoldAnnotation (held means permanently
// excluded from this operation, not merely deferred), split by role and ordered within each tier by
// TalosMachineUpgradeOrderLabel (a missing label sorts last), with name as the tiebreak.
//
// Reads through r.Client (the cache), not r.APIReader, on purpose: unlike acquireDisruptiveLock's
// control-plane count, nothing here gates whether a disruptive action is allowed; it only picks which
// already-authorized machine goes next. A machine deleted a moment ago can still appear (Advance fails
// on it and the operation pauses, visibly); one approved a moment ago can be briefly absent (deferred
// to the next reconcile). A stale list changes which machine is picked, never how many, so "one
// control plane at a time" and maxUnavailable hold.
func (r *TalosOperationReconciler) candidateMachines(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster,
) (controlPlanes, workers []*ichorv1alpha1.TalosMachine, err error) {
	list, err := deleteguard.ApprovedMachines(ctx, r.Client, cluster.Namespace, cluster.Name, deleteguard.MachineFilter{})
	if err != nil {
		return nil, nil, err
	}

	for i := range list {
		m := &list[i]
		if m.Annotations[ichorv1alpha1.TalosMachineUpgradeHoldAnnotation] != "" {
			continue
		}

		switch m.Spec.Role {
		case roleControlPlane:
			controlPlanes = append(controlPlanes, m)
		case workerRole:
			workers = append(workers, m)
		}
	}

	upgrade.SortByOrder(controlPlanes)
	upgrade.SortByOrder(workers)

	return controlPlanes, workers, nil
}

// nextBatch picks the machines to advance now: one pending control plane, else up to
// maxUnavailable pending workers, else one pending machine of own; empty when all are done.
func nextBatch(
	controlPlanes, workers, own []*ichorv1alpha1.TalosMachine, recorded []ichorv1alpha1.TalosOperationMachineStatus,
	maxUnavailable int32,
) []*ichorv1alpha1.TalosMachine {
	if pending := pendingMachines(controlPlanes, recorded); len(pending) > 0 {
		return pending[:1]
	}

	if pending := pendingMachines(workers, recorded); len(pending) > 0 {
		maxUnavailable = max(maxUnavailable, 1)

		return pending[:min(int32(len(pending)), maxUnavailable)]
	}

	if pending := pendingMachines(own, recorded); len(pending) > 0 {
		return pending[:1]
	}

	return nil
}

// kubernetesBatch is nextBatch for a Kubernetes upgrade, which visits the machines four times: the control planes
// for their apiservers, again for controller-manager and
// scheduler, again for kube-proxy and the bootstrap manifests, then the kubelets (control planes
// first, then workers within maxUnavailable). It records the stage in op.status.kubernetes and, when a stage has no pending
// machine left, moves to the next and clears status.machines (the progress of that stage), so
// the stage and the cleared list reach the API in the one status write that follows. Empty when
// the last stage is done. The controller's own machine (own, already taken out of heldCPs and
// heldWorkers) is only held back to last in the Kubelets stage, where its node is drained and the
// controller restarts; the other stages use the full controlPlanes list.
func kubernetesBatch(
	op *ichorv1alpha1.TalosOperation, controlPlanes, heldCPs, heldWorkers, own []*ichorv1alpha1.TalosMachine,
) []*ichorv1alpha1.TalosMachine {
	if op.Status.Kubernetes == nil {
		op.Status.Kubernetes = &ichorv1alpha1.TalosOperationKubernetesStatus{Stage: ichorv1alpha1.KubernetesStageAPIServers}
	}

	next := map[string]string{
		ichorv1alpha1.KubernetesStageAPIServers:             ichorv1alpha1.KubernetesStageControlPlaneComponents,
		ichorv1alpha1.KubernetesStageControlPlaneComponents: ichorv1alpha1.KubernetesStageKubeProxy,
		ichorv1alpha1.KubernetesStageKubeProxy:              ichorv1alpha1.KubernetesStageKubelets,
	}

	for {
		cps, ws, o := controlPlanes, []*ichorv1alpha1.TalosMachine(nil), []*ichorv1alpha1.TalosMachine(nil)
		if op.Status.Kubernetes.Stage == ichorv1alpha1.KubernetesStageKubelets {
			cps, ws, o = heldCPs, heldWorkers, own
		}

		if batch := nextBatch(cps, ws, o, op.Status.Machines, op.Spec.MaxUnavailable); len(batch) > 0 {
			return batch
		}

		stage, ok := next[op.Status.Kubernetes.Stage]
		if !ok {
			return nil
		}

		op.Status.Kubernetes.Stage = stage
		op.Status.Machines = nil
	}
}

// holdOwnNodeLast takes the machine whose Kubernetes node is nodeName (the one this controller
// runs on) out of controlPlanes and workers; it is advanced after both. Empty nodeName (the
// controller runs outside the cluster) changes nothing.
// The node names are resolved as everywhere else (spec.hostname is empty on the first control plane).
func (r *TalosOperationReconciler) holdOwnNodeLast(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, controlPlanes, workers []*ichorv1alpha1.TalosMachine,
) (cps, ws, own []*ichorv1alpha1.TalosMachine) {
	nodeName := r.NodeName
	if nodeName == "" {
		return controlPlanes, workers, nil
	}

	names, _ := r.nodeNames(ctx, cluster, nil, slices.Concat(controlPlanes, workers)) // an unnamed machine is not the controller's

	split := func(in []*ichorv1alpha1.TalosMachine) (rest []*ichorv1alpha1.TalosMachine) {
		for _, m := range in {
			if names[m.Name] == nodeName {
				own = append(own, m)

				continue
			}

			rest = append(rest, m)
		}

		return rest
	}

	cps = split(controlPlanes)
	ws = split(workers)

	return cps, ws, own
}

// pendingMachines returns every machine in candidates that has not reached
// ichorv1alpha1.TalosOperationMachinePhaseCompleted in recorded, in
// candidates' own (already sorted) order.
func pendingMachines(
	candidates []*ichorv1alpha1.TalosMachine, recorded []ichorv1alpha1.TalosOperationMachineStatus,
) []*ichorv1alpha1.TalosMachine {
	return slices.DeleteFunc(slices.Clone(candidates), func(m *ichorv1alpha1.TalosMachine) bool {
		return slices.ContainsFunc(recorded, func(rec ichorv1alpha1.TalosOperationMachineStatus) bool {
			return rec.Name == m.Name && rec.Phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted
		})
	})
}

// acquireDisruptiveLock is the only place in this file that calls clusterlock.Lock.Acquire
// (TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite checks every function of this name in the
// package). An upgrade must not start or continue while the live control-plane count is even (a
// replacement is in progress), the generic gate every disruptive operation goes through; unlike
// removal there is no completesTransition exception.
func (r *TalosOperationReconciler) acquireDisruptiveLock(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, op *ichorv1alpha1.TalosOperation,
) (*clusterlock.Lock, error) {
	// A cluster being shut down accepts no other disruptive work.
	if err := shutdownBlocker(ctx, readerOrClient(r.APIReader, r.Client), cluster.Namespace, cluster.Name, op); err != nil {
		return nil, err
	}

	controlPlanes, err := deleteguard.CountApprovedControlPlanes(ctx, r.APIReader, cluster.Namespace, cluster.Name)
	if err != nil {
		return nil, err
	}
	if transitioningControlPlanes(controlPlanes) {
		return nil, &transitioningError{controlPlanes: controlPlanes}
	}

	lock := clusterlock.New(r.Client, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, r.lockDuration).WithReader(r.APIReader)
	err = lock.Acquire(ctx, operationLockIdentity(op))
	// A holder that will never renew is not waited for five minutes.
	if retry, rerr := releaseIfHolderStopped(ctx, readerOrClient(r.APIReader, r.Client), lock, err); rerr != nil {
		err = rerr
	} else if retry {
		err = lock.Acquire(ctx, operationLockIdentity(op))
	}

	if err != nil {
		return nil, err
	}

	return lock, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TalosOperationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosOperation{}).
		Named("talosoperation").
		Complete(r)
}

// configSelection narrows a config operation's candidates to the machines with a pending change (the
// plan's selection) and those it already started; other kinds pass through.
func configSelection(
	op *ichorv1alpha1.TalosOperation, controlPlanes, workers []*ichorv1alpha1.TalosMachine,
) (cps, ws []*ichorv1alpha1.TalosMachine) {
	if op.Spec.Kind != ichorv1alpha1.TalosOperationKindConfig {
		return controlPlanes, workers
	}

	drop := func(m *ichorv1alpha1.TalosMachine) bool {
		_, started := recorded(op, m.Name)

		return !started && !upgrade.ConfigPending(m)
	}

	return slices.DeleteFunc(controlPlanes, drop), slices.DeleteFunc(workers, drop)
}
