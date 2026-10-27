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
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// operationRequeueInterval is how often TalosOperationReconciler re-checks
// an in-progress machine step, or retries acquiring a held/transitioning
// lock. Thirty seconds, the same cadence CNIHandoverReconciler polls Argo
// CD at: an upgrade step is expected to finish in minutes, not seconds, so
// this is "come back soon enough that status doesn't look stalled," not a
// tight poll.
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
)

// AdvanceMachineFunc performs one disruptive step for a single machine
// within an in-progress TalosOperation and reports whether that machine's
// step is now done. Declared here, in the consumer, rather than in
// pkg/upgrade: ADR 0022 left pre-check/execution interfaces undeclared on
// purpose because the consumer didn't exist yet. This reconciler is that
// consumer; each upgrade kind's own implementation -- calling Talos's
// Upgrade API, driving a Kubernetes upgrade, launching Ichor's installer
// Job -- is separate, later work, wired in by setting
// TalosOperationReconciler.Advance (the machine is nil for kind ichor,
// which has none). The state machine here never branches
// on spec.kind itself (ADR 0026).
type AdvanceMachineFunc func(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation,
) (done bool, err error)

// TalosOperationReconciler implements the backbone every upgrade kind
// shares (docs/design/architecture.md, "Upgrades"): the cluster lock, the
// status-held state machine, pause-on-failure with no automatic
// cluster-wide rollback, spec.paused, maxUnavailable for workers against
// one-control-plane-at-a-time, the per-machine hold annotation, and
// label-based ordering. See ADR 0026 for why this holds no per-kind
// execution branching at all.
//
// A separate controller from TalosClusterReconciler and
// TalosMachineReconciler, for the same reason CNIHandoverReconciler is
// (ADR 0024): each controller gets its own work queue, so a slow,
// unrelated reconcile in one never delays this one.
type TalosOperationReconciler struct {
	client.Client

	// APIReader is used by acquireDisruptiveLock alone, for the
	// transitioning-control-plane count that gates whether this operation
	// is allowed to acquire the cluster lock at all -- the same
	// cached-read trap deleteguard.CountApprovedControlPlanes documents
	// (ADR 0010's appendix). That check is a safety gate on a disruptive
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

	// TalosClient, ControllerNamespace and NodeName serve the shutdown kind
	// alone (shutdown.go, ADR 0067). TalosClient opens the Talos API of one
	// machine; ControllerNamespace is where the secrets bundle lives; NodeName
	// is the Kubernetes node this controller runs on ("" when it runs outside
	// the cluster it manages), so its own node can be stopped last.
	TalosClient         func(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle) (TalosClient, error)
	ControllerNamespace string
	NodeName            string

	// NodesReady and Recorder serve the ca-rotation kind alone (ca_rotation.go, ADR 0063).
	// NodesReady reads which Kubernetes Nodes are not Ready; Recorder raises the BundleBackupStale
	// Event (nil skips the Event, not the condition).
	NodesReady NodesReadyFunc
	Recorder   record.EventRecorder

	// KubeClient, Evacuator serve the node kind alone (node_action.go, ADR 0085): KubeClient
	// cordons, drains and reads Node readiness live (nil skips the drain and reads the cache),
	// Evacuator is the storage hook run before a node is taken away (nil skips it).
	KubeClient kubernetes.Interface
	Evacuator  StorageEvacuator
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosoperations,verbs=get;list;watch;update;patch
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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if handled, err := r.caRotationFinalizerStep(ctx, &op); handled || err != nil {
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

	lock, err := r.acquireDisruptiveLock(ctx, &cluster, &op)
	if err != nil {
		if shuttingDown, ok := errors.AsType[*shuttingDownError](err); ok {
			return ctrl.Result{RequeueAfter: operationRequeueInterval},
				r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused, shuttingDown.reason(), shuttingDown.Error())
		}

		if transitioning, ok := errors.AsType[*transitioningError](err); ok {
			return ctrl.Result{RequeueAfter: operationRequeueInterval},
				r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused, reasonOperationTransitioning, transitioning.Error())
		}

		if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
			return ctrl.Result{RequeueAfter: operationRequeueInterval}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused,
				reasonOperationLockHeld, fmt.Sprintf("cluster lock held by %q", held.Holder))
		}

		return ctrl.Result{}, err
	}

	if op.Spec.Paused {
		return ctrl.Result{}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationPaused, "spec.paused is true")
	}

	// A step already failed: stay paused and never retry it automatically.
	// Nothing here undoes a machine already marked Completed either --
	// "pause-on-failure (no automatic cluster-wide rollback)".
	if op.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused && op.Status.FailureReason != "" {
		return ctrl.Result{}, nil
	}

	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindShutdown {
		return r.advanceShutdown(ctx, &op, &cluster, lock)
	}

	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindNode {
		return r.advanceNode(ctx, &op, &cluster, lock)
	}

	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindCARotation {
		return r.advanceCARotation(ctx, &op, &cluster, lock)
	}

	if r.Advance == nil {
		return ctrl.Result{}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationNoExecutor, "no machine executor is wired up for this controller")
	}

	// An Ichor self-upgrade touches no machine: its executor watches one installer Job, so the
	// operation carries no machine list (ADR 0059).
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindIchor {
		return r.advanceIchor(ctx, &op, &cluster, lock)
	}

	controlPlanes, workers, err := r.candidateMachines(ctx, &cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// A config rollout reboots nodes, so the machine this controller runs on goes last: its reboot
	// stops the controller, which resumes from the status and has nothing left to wait for (ADR 0080).
	var own []*ichorv1alpha1.TalosMachine
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindConfig {
		controlPlanes, workers, own = holdOwnNodeLast(controlPlanes, workers, r.NodeName)
	}

	// Control planes go one at a time, and only once every one of them is
	// done do workers start -- never interleaved with workers.
	var toAdvance []*ichorv1alpha1.TalosMachine
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindKubernetes {
		toAdvance = kubernetesBatch(&op, controlPlanes, workers)
	} else {
		toAdvance = nextBatch(controlPlanes, workers, own, op.Status.Machines, op.Spec.MaxUnavailable)
	}

	if len(toAdvance) == 0 {
		if err := lock.Release(ctx, controllerIdentity); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhaseCompleted,
			reasonOperationComplete, "every eligible machine finished")
	}

	// The long-operation half of "the lock": renewed once per batch, the
	// same way removal.go's renewWhile already renews repeatedly for a
	// drain, well inside clusterlock.DefaultDuration's five minutes.
	if err := lock.Renew(ctx, controllerIdentity); err != nil {
		return ctrl.Result{}, err
	}

	requeue := false

	for _, m := range toAdvance {
		done, err := r.Advance(ctx, &cluster, m, &op)
		if err != nil {
			// Pause on failure, without undoing anything already
			// Completed in op.Status.Machines above -- there is no
			// automatic cluster-wide rollback.
			setMachineStatus(&op, m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)

			return ctrl.Result{}, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhasePaused,
				reasonOperationStepFailed, fmt.Sprintf("machine %q: %v", m.Name, err))
		}

		phase := ichorv1alpha1.TalosOperationMachinePhaseInProgress
		if done {
			phase = ichorv1alpha1.TalosOperationMachinePhaseCompleted
		} else {
			requeue = true
		}

		setMachineStatus(&op, m.Name, phase)
	}

	result := ctrl.Result{}
	if requeue {
		result.RequeueAfter = operationRequeueInterval
	}

	return result, r.setPhase(ctx, &op, ichorv1alpha1.TalosOperationPhaseInProgress,
		reasonOperationAdvancing, advancingMessage(&op, len(toAdvance)))
}

func advancingMessage(op *ichorv1alpha1.TalosOperation, n int) string {
	if op.Status.Kubernetes != nil && op.Status.Kubernetes.Stage != "" {
		return fmt.Sprintf("stage %s: %d machine(s) advancing", op.Status.Kubernetes.Stage, n)
	}

	return fmt.Sprintf("%d machine(s) advancing", n)
}

// restingPhase handles the phases that hold no cluster lock. A shutdown that has asked every machine
// to stop only waits for the operator to power them on (shutdown.go, ADR 0067); so does a node
// shutdown, and a node reset waits for the removal flow, which takes the cluster lock itself (ADR 0085).
func (r *TalosOperationReconciler) restingPhase(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster,
) (res ctrl.Result, handled bool, err error) {
	switch op.Spec.Kind {
	case ichorv1alpha1.TalosOperationKindShutdown:
		if op.Status.Phase == ichorv1alpha1.TalosOperationPhaseShutDown {
			res, err = r.awaitPowerOn(ctx, op, cluster)

			return res, true, err
		}
	case ichorv1alpha1.TalosOperationKindNode:
		if op.Status.Phase == ichorv1alpha1.TalosOperationPhaseShutDown {
			res, err = r.awaitPowerOn(ctx, op, cluster)

			return res, true, err
		}

		if st := op.Status.Node; st != nil && st.Step == ichorv1alpha1.NodeStepRemoving && op.Status.FailureReason == "" {
			res, err = r.awaitRemoval(ctx, op)

			return res, true, err
		}
	}

	return ctrl.Result{}, false, nil
}

// setMachineStatus records name's phase in op.status.machines, replacing
// any existing entry for the same name rather than appending a duplicate.
func setMachineStatus(op *ichorv1alpha1.TalosOperation, name, phase string) {
	for i := range op.Status.Machines {
		if op.Status.Machines[i].Name == name {
			op.Status.Machines[i].Phase = phase
			return
		}
	}

	op.Status.Machines = append(op.Status.Machines, ichorv1alpha1.TalosOperationMachineStatus{Name: name, Phase: phase})
}

// setPhase persists op's phase, reason and message, then writes status.
func (r *TalosOperationReconciler) setPhase(ctx context.Context, op *ichorv1alpha1.TalosOperation, phase, reason, message string) error {
	op.Status.Phase = phase

	if reason == reasonOperationStepFailed {
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

	return r.Status().Update(ctx, op)
}

// candidateMachines lists every approved, not-being-deleted TalosMachine belonging to cluster
// (deleteguard.ApprovedMachines),
// excluding any carrying ichorv1alpha1.TalosMachineUpgradeHoldAnnotation
// (the design's "per-machine hold annotation" -- held means permanently
// excluded from this operation, not merely deferred), split by role and
// ordered within each tier by ichorv1alpha1.TalosMachineUpgradeOrderLabel
// (the design's "label-based ordering"; a missing label sorts last), with
// name as the deterministic tiebreak.
//
// Reads through r.Client (the manager's cache), not r.APIReader, on
// purpose: unlike acquireDisruptiveLock's control-plane count, nothing
// here gates whether a disruptive action is allowed at all -- it only
// picks which already-authorized machine goes next. A cache lag has two
// possible failure shapes, neither one of them the quorum problem's "an
// action proceeds that should have been refused": a machine deleted a
// moment ago can still appear here, which just means Advance is called on
// a machine that no longer exists (a visible, reportable error, not a
// silent wrong one -- the existing pause-on-failure path handles it the
// same as any other Advance failure); a machine approved a moment ago can
// be briefly absent, which only defers it to the next reconcile this
// level-triggered loop will run regardless. Either way nothing here can
// make "one control plane at a time" or "maxUnavailable workers" actually
// be violated, since Reconcile truncates the slice this returns itself --
// a stale or incomplete candidate list changes which machine is picked,
// never how many.
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
		case "worker":
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

// kubernetesBatch is nextBatch for a Kubernetes upgrade, which visits the machines four times
// (ADR 0087): the control planes for their apiservers, again for controller-manager and
// scheduler, again for kube-proxy and the bootstrap manifests, then the kubelets (control planes
// first, then workers within maxUnavailable). It records the stage in op.status.kubernetes and, when a stage has no pending
// machine left, moves to the next and clears status.machines (the progress of that stage), so
// the stage and the cleared list reach the API in the one status write that follows. Empty when
// the last stage is done.
func kubernetesBatch(op *ichorv1alpha1.TalosOperation, controlPlanes, workers []*ichorv1alpha1.TalosMachine) []*ichorv1alpha1.TalosMachine {
	if op.Status.Kubernetes == nil {
		op.Status.Kubernetes = &ichorv1alpha1.TalosOperationKubernetesStatus{Stage: ichorv1alpha1.KubernetesStageAPIServers}
	}

	next := map[string]string{
		ichorv1alpha1.KubernetesStageAPIServers:             ichorv1alpha1.KubernetesStageControlPlaneComponents,
		ichorv1alpha1.KubernetesStageControlPlaneComponents: ichorv1alpha1.KubernetesStageKubeProxy,
		ichorv1alpha1.KubernetesStageKubeProxy:              ichorv1alpha1.KubernetesStageKubelets,
	}

	for {
		ws := workers
		if op.Status.Kubernetes.Stage != ichorv1alpha1.KubernetesStageKubelets {
			ws = nil
		}

		if batch := nextBatch(controlPlanes, ws, nil, op.Status.Machines, op.Spec.MaxUnavailable); len(batch) > 0 {
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
func holdOwnNodeLast(
	controlPlanes, workers []*ichorv1alpha1.TalosMachine, nodeName string,
) (cps, ws, own []*ichorv1alpha1.TalosMachine) {
	if nodeName == "" {
		return controlPlanes, workers, nil
	}

	split := func(in []*ichorv1alpha1.TalosMachine) (rest []*ichorv1alpha1.TalosMachine) {
		for _, m := range in {
			if m.Spec.Hostname == nodeName {
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
	var pending []*ichorv1alpha1.TalosMachine

	for _, m := range candidates {
		completed := false

		for _, rec := range recorded {
			if rec.Name == m.Name && rec.Phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted {
				completed = true
				break
			}
		}

		if !completed {
			pending = append(pending, m)
		}
	}

	return pending
}

// acquireDisruptiveLock is the only place in this file that calls
// clusterlock.Lock.Acquire -- TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite
// checks every function with this exact name across the package, not just
// TalosMachineReconciler's own (internal/controller/removal.go), so this
// satisfies it the same way that one does. An upgrade must not start, or
// continue, while the cluster's live control-plane count is even -- a
// replacement is in progress -- which is exactly the generic gate every
// other disruptive operation already goes through; TalosOperation has no
// analogue of removal's own completesTransition exception, so this never
// bypasses the check.
func (r *TalosOperationReconciler) acquireDisruptiveLock(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, op *ichorv1alpha1.TalosOperation,
) (*clusterlock.Lock, error) {
	// A cluster being shut down accepts no other disruptive work (ADR 0067).
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

	lock := clusterlock.New(r.Client, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
	if err := lock.Acquire(ctx, controllerIdentity); err != nil {
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
