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

// This file implements the TalosOperationKindShutdown state machine (ADR
// 0065): a planned stop of every machine of a cluster. It is not a rollout
// and does not go through AdvanceMachineFunc; TalosOperationReconciler
// dispatches to it after taking the cluster lock.
//
// Stages, each recorded in op.status before the Talos API is called, so a
// restarted controller never stops a machine twice:
//
//  1. Workers, except the node this controller runs on, are asked to stop
//     with Talos's cordon-and-drain (Shutdown force=false). The operation
//     waits until each has stopped answering.
//  2. Everything left -- every control plane and the controller's own node --
//     is asked in one burst with force=true (no drain: the Kubernetes API is
//     about to go), the controller's own node last. Etcd loses quorum only
//     here. The stage is a burst, not a sequence, because a controller that
//     lives in the cluster it stops dies with the first control plane that
//     takes its pod down; the requests are all sent before that can happen.
//  3. Phase ShutDown: nothing left to do but wait for the operator to power
//     the machines on. The operation completes, and the mark that refuses
//     other disruptive work goes away, once every machine answers on a new boot.
package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

const (
	reasonOperationShuttingDown = "ClusterShuttingDown"

	// conditionShutdown is the shutdown operation's own progress line.
	conditionShutdown = "Shutdown"

	reasonShutdownStopping  = "StoppingWorkers"
	reasonShutdownRequested = "Requested"
	reasonShutdownWaiting   = "WaitingForPowerOn"
	reasonShutdownReturned  = "AllMachinesBack"
)

// shuttingDownError is the refusal of every disruptive operation while a
// shutdown of the cluster has not completed. Distinguished from
// clusterlock.HeldError and transitioningError so each gets its own reason.
type shuttingDownError struct {
	operation string
	// rotation marks the refusal made for a CA rotation in progress (ca_rotation.go), which
	// refuses the same work for the same reason: the nodes are not in one settled state.
	rotation bool
}

func (e *shuttingDownError) Error() string {
	if e.rotation {
		return fmt.Sprintf("cluster CA rotation is in progress (TalosOperation %q); disruptive work is refused until it completes", e.operation)
	}

	return fmt.Sprintf("cluster is shutting down (TalosOperation %q); disruptive work is refused until it completes", e.operation)
}

func (e *shuttingDownError) reason() string {
	if e.rotation {
		return reasonOperationCARotating
	}

	return reasonOperationShuttingDown
}

// shutdownBlocker returns a *shuttingDownError when a shutdown of the cluster
// is under way and self is not the one running it. The mark is derived, not
// stored: it is the existence of a shutdown TalosOperation that has not
// Completed, so deleting that operation lifts it and nothing can leave it
// stale. Read through reader (the API reader), because it gates a
// disruptive action.
//
// self is the operation asking, nil for the machine controller. A shutdown
// operation is blocked only by another that has already stopped something;
// two that are both still Pending are reconciled one after the other, so the
// first moves on and the second finds it.
func shutdownBlocker(ctx context.Context, reader client.Reader, namespace, cluster string, self *ichorv1alpha1.TalosOperation) error {
	var ops ichorv1alpha1.TalosOperationList
	if err := reader.List(ctx, &ops, client.InNamespace(namespace)); err != nil {
		return err
	}

	for i := range ops.Items {
		other := &ops.Items[i]
		if other.Spec.ClusterRef.Name == cluster && caRotationUnderway(other) && (self == nil || self.Name != other.Name) {
			return &shuttingDownError{operation: other.Name, rotation: true}
		}

		if other.Spec.ClusterRef.Name != cluster || other.Spec.Kind != ichorv1alpha1.TalosOperationKindShutdown ||
			other.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted || !other.DeletionTimestamp.IsZero() {
			continue
		}

		if self != nil && other.Name == self.Name {
			continue
		}

		if self != nil && self.Spec.Kind == ichorv1alpha1.TalosOperationKindShutdown && len(other.Status.Machines) == 0 {
			continue
		}

		return &shuttingDownError{operation: other.Name}
	}

	return nil
}

// readerOrClient is APIReader, or the cached client in a process that wired
// none (tests).
func readerOrClient(reader client.Reader, c client.Client) client.Reader {
	if reader != nil {
		return reader
	}

	return c
}

// recorded returns op.status.machines' entry for name.
func recorded(op *ichorv1alpha1.TalosOperation, name string) (ichorv1alpha1.TalosOperationMachineStatus, bool) {
	for _, rec := range op.Status.Machines {
		if rec.Name == name {
			return rec, true
		}
	}

	return ichorv1alpha1.TalosOperationMachineStatus{}, false
}

// shutdownPlan splits the cluster's approved machines into the workers to
// drain first and everything else, which is stopped last in one burst. The
// controller's own node (r.NodeName) moves from the first group to the last,
// and is asked last within it. It selects machines to stop; it does not count
// control planes for any quorum decision (that is acquireDisruptiveLock's
// canonical count, taken before this runs). Upgrade holds are not honoured: they exclude
// a machine from a rollout, not from a planned stop of the whole cluster.
func (r *TalosOperationReconciler) shutdownPlan(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster,
) (workers, last []*ichorv1alpha1.TalosMachine, err error) {
	// IncludeDeleting: a stop of the whole cluster must not leave a running node
	// behind just because its removal has begun.
	list, err := deleteguard.ApprovedMachines(ctx, readerOrClient(r.APIReader, r.Client), cluster.Namespace, cluster.Name,
		deleteguard.MachineFilter{IncludeDeleting: true})
	if err != nil {
		return nil, nil, err
	}

	var own []*ichorv1alpha1.TalosMachine

	for i := range list {
		m := &list[i]

		switch {
		case r.NodeName != "" && m.Spec.Hostname == r.NodeName:
			own = append(own, m)
		case m.Spec.Role == roleControlPlane:
			last = append(last, m)
		case m.Spec.Role == "worker":
			workers = append(workers, m)
		}
	}

	upgrade.SortByOrder(workers)
	upgrade.SortByOrder(last)

	return workers, append(last, own...), nil
}

// probe dials m and reads its boot ID. An error means the machine does not
// answer, which for a shutdown is the state being sought, not a failure.
func (r *TalosOperationReconciler) probe(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (TalosClient, string, error) {
	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	talos, err := r.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		return nil, "", err
	}

	id, err := talos.BootID(dialCtx)
	if err != nil {
		closeTalosClient(talos)

		return nil, "", err
	}

	return talos, id, nil
}

// requestStop asks every machine of group without a record to stop. Each is
// recorded (with its boot ID) in op.status first and asked after, so that a
// controller that dies in between leaves a record and never a second request;
// if a request itself fails, that machine's record and the records of those
// not yet asked are dropped again so the next reconcile retries them.
func (r *TalosOperationReconciler) requestStop(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster,
	bundle *secrets.Bundle, group []*ichorv1alpha1.TalosMachine, force bool,
) error {
	type target struct {
		m *ichorv1alpha1.TalosMachine
		c TalosClient
	}

	var targets []target

	defer func() {
		for _, t := range targets {
			closeTalosClient(t.c)
		}
	}()

	changed := false

	for _, m := range group {
		if _, ok := recorded(op, m.Name); ok {
			continue
		}

		changed = true

		setMachineStatus(op, m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)

		talos, id, err := r.probe(ctx, cluster, m, bundle)
		if err != nil {
			// Not answering: already stopped (or never started). Nothing to ask.
			continue
		}

		setMachineBootID(op, m.Name, id)

		targets = append(targets, target{m: m, c: talos})
	}

	if !changed {
		return nil
	}

	op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
	if err := r.Status().Update(ctx, op); err != nil {
		return err
	}

	for i, t := range targets {
		callCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
		err := t.c.Shutdown(callCtx, force)

		cancel()

		if err != nil {
			for _, unsent := range targets[i:] {
				op.Status.Machines = slices.DeleteFunc(op.Status.Machines, func(rec ichorv1alpha1.TalosOperationMachineStatus) bool {
					return rec.Name == unsent.m.Name
				})
			}

			if uerr := r.Status().Update(ctx, op); uerr != nil {
				return errors.Join(err, uerr)
			}

			return fmt.Errorf("asking %q to shut down: %w", t.m.Name, err)
		}
	}

	return nil
}

func setMachineBootID(op *ichorv1alpha1.TalosOperation, name, bootID string) {
	for i := range op.Status.Machines {
		if op.Status.Machines[i].Name == name {
			op.Status.Machines[i].BootID = bootID
		}
	}
}

// stopped reports whether m no longer runs the boot it was asked to leave:
// it does not answer, or answers on a different boot. A machine that was
// already unreachable when the operation looked (no boot ID recorded) counts
// as stopped even if it has since come up -- it was never asked to stop, and
// a worker left running is harmless once the control plane is gone.
func (r *TalosOperationReconciler) stopped(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster,
	bundle *secrets.Bundle, m *ichorv1alpha1.TalosMachine,
) bool {
	rec, ok := recorded(op, m.Name)
	if !ok {
		return false
	}

	talos, id, err := r.probe(ctx, cluster, m, bundle)
	if err != nil {
		return true
	}
	defer closeTalosClient(talos)

	return rec.BootID == "" || id != rec.BootID
}

// advanceShutdown runs stages 1 and 2. The caller holds the cluster lock.
func (r *TalosOperationReconciler) advanceShutdown(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) (ctrl.Result, error) {
	if r.TalosClient == nil {
		return ctrl.Result{}, r.shutdownStatus(ctx, op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationNoExecutor, "no Talos client is wired up for this controller")
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading secrets bundle to shut down %q: %w", cluster.Name, err)
	}

	workers, last, err := r.shutdownPlan(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := lock.Renew(ctx, controllerIdentity); err != nil {
		return ctrl.Result{}, err
	}

	// Stage 1: drain and stop the workers; wait until they are down.
	if err := r.requestStop(ctx, op, cluster, bundle, workers, false); err != nil {
		return ctrl.Result{}, err
	}

	waiting := 0

	for _, m := range workers {
		if !r.stopped(ctx, op, cluster, bundle, m) {
			waiting++
		}
	}

	if waiting > 0 {
		return ctrl.Result{RequeueAfter: operationRequeueInterval}, r.shutdownStatus(ctx, op,
			ichorv1alpha1.TalosOperationPhaseInProgress, reasonShutdownStopping,
			fmt.Sprintf("waiting for %d of %d worker(s) to stop", waiting, len(workers)))
	}

	// Stage 2: the rest in one burst. Quorum is lost here and only here.
	if err := r.requestStop(ctx, op, cluster, bundle, last, true); err != nil {
		return ctrl.Result{}, err
	}

	if err := lock.Release(ctx, controllerIdentity); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, r.shutdownStatus(ctx, op,
		ichorv1alpha1.TalosOperationPhaseShutDown, reasonShutdownWaiting,
		fmt.Sprintf("every machine was asked to shut down; power them on, and this completes when all %d answer again",
			len(op.Status.Machines)))
}

// awaitPowerOn runs in phase ShutDown, without the cluster lock (nothing is in
// flight; the mark is what refuses other work). A machine is back when it
// answers on a boot other than the one recorded, or, if it was already down
// when the operation looked, when it answers at all. All of them back
// completes the operation, which lifts the mark. Each recorded name is read by Get, only as a
// lookup to its Talos endpoint, never a count.
func (r *TalosOperationReconciler) awaitPowerOn(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster,
) (ctrl.Result, error) {
	if r.TalosClient == nil {
		return ctrl.Result{}, nil
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading secrets bundle for %q: %w", cluster.Name, err)
	}

	reader := readerOrClient(r.APIReader, r.Client)
	pending := 0

	for i := range op.Status.Machines {
		rec := &op.Status.Machines[i]
		if rec.Phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted {
			continue
		}

		m := &ichorv1alpha1.TalosMachine{}

		err := reader.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: rec.Name}, m)
		// A machine deleted meanwhile is not waited for.
		if apierrors.IsNotFound(err) {
			rec.Phase = ichorv1alpha1.TalosOperationMachinePhaseCompleted

			continue
		}

		if err != nil {
			return ctrl.Result{}, err
		}

		talos, id, err := r.probe(ctx, cluster, m, bundle)
		if err != nil {
			pending++

			continue
		}

		closeTalosClient(talos)

		if rec.BootID == "" || id != rec.BootID {
			rec.Phase = ichorv1alpha1.TalosOperationMachinePhaseCompleted
		} else {
			pending++
		}
	}

	if pending > 0 {
		return ctrl.Result{RequeueAfter: operationRequeueInterval}, r.shutdownStatus(ctx, op,
			ichorv1alpha1.TalosOperationPhaseShutDown, reasonShutdownWaiting,
			fmt.Sprintf("%d of %d machine(s) still to come back", pending, len(op.Status.Machines)))
	}

	return ctrl.Result{}, r.shutdownStatus(ctx, op, ichorv1alpha1.TalosOperationPhaseCompleted,
		reasonShutdownReturned, "every machine is back; the cluster accepts disruptive operations again")
}

// shutdownStatus writes the phase and the Shutdown condition in one status update.
func (r *TalosOperationReconciler) shutdownStatus(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, phase, reason, message string,
) error {
	status := metav1.ConditionTrue
	if phase == ichorv1alpha1.TalosOperationPhaseCompleted {
		status = metav1.ConditionFalse
	}

	op.Status.Phase = phase
	apimeta.SetStatusCondition(&op.Status.Conditions, metav1.Condition{
		Type: conditionShutdown, Status: status, Reason: reason, Message: message,
	})

	return r.Status().Update(ctx, op)
}
