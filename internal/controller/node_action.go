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

// This file implements the TalosOperationKindNode state machine: one action on one
// machine. Like shutdown it is dispatched by TalosOperationReconciler after the cluster lock is
// taken, and every step is recorded in op.status before the next call, so a restarted controller
// (including one whose own node the action reboots) resumes where the status says it was and never
// sends the same action twice.
//
//	reboot           guards -> storage evacuation -> cordon and drain -> record boot ID -> Reboot ->
//	                 wait for a new boot ID, Ready and (control plane) etcd membership -> uncordon
//	shutdown         guards -> storage evacuation -> record boot ID -> Shutdown (Talos drains unless the
//	                 API goes away) -> wait until the node stops answering -> phase ShutDown
//	restart-service  allowlist -> ServiceRestart -> wait for the Node to be Ready
//	etcd-defrag      control plane, not the leader of a multi-member etcd -> Defragment -> no alarm left
//	etcd-forfeit-leadership  control plane that leads -> ForfeitLeadership -> a different leader
//	reset            hand the machine to the removal flow with the reset opt-in -> wait for it to go
package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/storage"
)

const (
	reasonNodeActionDone = "NodeActionDone"

	// nodeActionTimeout bounds the wait for an action's effect (a reboot to finish, a node to stop,
	// a leader to change). Past it the step fails and the operation pauses.
	nodeActionTimeout = 20 * time.Minute

	// defragTimeout bounds one etcd defragmentation. The lock is renewed while it runs.
	defragTimeout = 10 * time.Minute
)

// nodeActionServices is the allowlist of restart-service: the services whose restart cannot take
// the node's identity or the cluster's control away. etcd, apid, trustd and machined are
// deliberately absent.
var nodeActionServices = []string{serviceKubelet, "containerd", "cri"}

// nodeActionSettle is how long after a service restart the node's Ready condition is trusted
// again (the condition lags the restart). A variable only so tests can shorten it.
var nodeActionSettle = 15 * time.Second

// nodeRun is one reconcile of one node-kind operation.
type nodeRun struct {
	r       *TalosOperationReconciler
	op      *ichorv1alpha1.TalosOperation
	cluster *ichorv1alpha1.TalosCluster
	lock    *clusterlock.Lock
	m       *ichorv1alpha1.TalosMachine
	talos   TalosClient
	spec    *ichorv1alpha1.TalosOperationNodeSpec
	// dial bounds the short Talos calls of this reconcile.
	dial context.Context
}

// nodeFailed is the pause-on-failure of a node action.
func (r *TalosOperationReconciler) nodeFailed(ctx context.Context, op *ichorv1alpha1.TalosOperation, err error) (ctrl.Result, error) {
	return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused, reasonOperationStepFailed, err.Error())
}

// advanceNode runs one step of a node-kind operation. The caller holds the cluster lock.
func (r *TalosOperationReconciler) advanceNode(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) (ctrl.Result, error) {
	spec := op.Spec.Node
	if spec == nil {
		return r.nodeFailed(ctx, op, errors.New("a node operation has no spec.node"))
	}

	if r.TalosClient == nil {
		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationNoExecutor, "no Talos client is wired up for this controller")
	}

	m := &ichorv1alpha1.TalosMachine{}
	if err := readerOrClient(r.APIReader, r.Client).Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: spec.MachineRef.Name}, m); err != nil {
		return r.nodeFailed(ctx, op, fmt.Errorf("node %q: %w", spec.MachineRef.Name, err))
	}

	if m.Spec.ClusterRef.Name != cluster.Name || !m.Spec.IsApproved() {
		return r.nodeFailed(ctx, op, fmt.Errorf("node %q is not an approved node of cluster %q", m.Name, cluster.Name))
	}

	if err := lock.Renew(ctx, operationLockIdentity(op)); err != nil {
		return renewRefusal(ctx, err)
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading secrets bundle for %q: %w", m.Name, err)
	}

	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	n := &nodeRun{r: r, op: op, cluster: cluster, lock: lock, m: m, spec: spec, dial: dialCtx}
	waiting := op.Status.Node != nil && op.Status.Node.Step == ichorv1alpha1.NodeStepRequested

	n.talos, err = r.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		if waiting && spec.Action == ichorv1alpha1.NodeActionShutdown {
			return n.shutDown(ctx) // a node that no longer answers is the state a shutdown is after
		}

		if waiting {
			return n.wait(ctx, fmt.Sprintf("dialing %q: %v", m.Name, err))
		}

		return r.nodeFailed(ctx, op, fmt.Errorf("dialing %q: %w", m.Name, err))
	}
	defer closeTalosClient(n.talos)

	if err := confirmMachine(dialCtx, n.talos, m); err != nil {
		if waiting {
			return n.wait(ctx, err.Error())
		}

		return r.nodeFailed(ctx, op, err)
	}

	step := n.begin
	if waiting {
		step = n.await
	}

	res, err := step(ctx)
	if err != nil {
		return n.pause(ctx, err)
	}

	return res, nil
}

// nodeActionBegin checks the guards and sends the action; nodeActionAwait runs while a sent action
// has not shown its effect yet (etcd-defrag and reset finish in begin and have no entry).
var nodeActionBegin = map[string]func(*nodeRun, context.Context) (ctrl.Result, error){
	ichorv1alpha1.NodeActionReboot:                (*nodeRun).beginReboot,
	ichorv1alpha1.NodeActionShutdown:              (*nodeRun).beginShutdown,
	ichorv1alpha1.NodeActionRestartService:        (*nodeRun).beginRestartService,
	ichorv1alpha1.NodeActionEtcdDefrag:            (*nodeRun).beginDefrag,
	ichorv1alpha1.NodeActionEtcdForfeitLeadership: (*nodeRun).beginForfeit,
	ichorv1alpha1.NodeActionReset:                 (*nodeRun).beginReset,
}

var nodeActionAwait = map[string]func(*nodeRun, context.Context) (ctrl.Result, error){
	ichorv1alpha1.NodeActionReboot:                (*nodeRun).awaitReboot,
	ichorv1alpha1.NodeActionShutdown:              (*nodeRun).awaitShutdown,
	ichorv1alpha1.NodeActionRestartService:        (*nodeRun).awaitRestartService,
	ichorv1alpha1.NodeActionEtcdForfeitLeadership: (*nodeRun).awaitForfeit,
}

func (n *nodeRun) begin(ctx context.Context) (ctrl.Result, error) {
	if f, ok := nodeActionBegin[n.spec.Action]; ok {
		return f(n, ctx)
	}

	return ctrl.Result{}, fmt.Errorf("unknown node action %q", n.spec.Action)
}

func (n *nodeRun) await(ctx context.Context) (ctrl.Result, error) {
	if f, ok := nodeActionAwait[n.spec.Action]; ok {
		return f(n, ctx)
	}

	return ctrl.Result{}, fmt.Errorf("node action %q has nothing to wait for", n.spec.Action)
}

// pause pauses the operation with cause, after undoing the cordon and the storage evacuation. If
// that undo fails the operation is not paused: the error is returned so the reconcile is retried
// (and the same step fails and undoes again), because a paused operation is not reconciled and
// would leave the node unschedulable.
func (n *nodeRun) pause(ctx context.Context, cause error) (ctrl.Result, error) {
	if err := n.cleanup(ctx); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "undoing the cordon and storage evacuation of a failed node action; retrying",
			"machine", n.m.Name, "cause", cause.Error())

		return ctrl.Result{}, fmt.Errorf("undoing the cordon and storage evacuation of %q after %q: %w", n.m.Name, cause, err)
	}

	return n.r.nodeFailed(ctx, n.op, cause)
}

// cleanup undoes what a step that failed before sending anything left behind: the cordon and
// the storage evacuation. A node must not stay unschedulable because an operation paused.
func (n *nodeRun) cleanup(ctx context.Context) error {
	if n.spec.Action != ichorv1alpha1.NodeActionReboot && n.spec.Action != ichorv1alpha1.NodeActionShutdown {
		return nil
	}

	node, err := resolveNodeName(ctx, n.r.Client, n.talos, n.m)
	if err != nil {
		return nil // no node name, so the step that failed never reached a cordon or an evacuation
	}

	var uncordonErr error
	if n.r.KubeClient != nil {
		uncordonErr = uncordonAfterReboot(ctx, n.r.KubeClient, node)
	}

	// Decide from the machine as it is stored now, not the copy this reconcile read before the
	// evacuation began.
	fresh := &ichorv1alpha1.TalosMachine{}
	if err := readerOrClient(n.r.APIReader, n.r.Client).Get(ctx, client.ObjectKeyFromObject(n.m), fresh); err != nil {
		return errors.Join(uncordonErr, err)
	}

	return errors.Join(uncordonErr, endMachineEvacuation(ctx, n.r.Client, n.r.Evacuator, n.cluster, fresh, node))
}

// recordRequest persists, before the action is sent, the boot ID the node is leaving and the
// time, so a controller killed between the record and the call resumes into "waiting" and a
// call is never repeated.
func (n *nodeRun) recordRequest(ctx context.Context, message string) error {
	bootCtx, cancelBoot := dialBound(ctx)
	bootID, err := n.talos.BootID(bootCtx)

	cancelBoot()

	if err != nil {
		return fmt.Errorf("reading %q's boot ID before the action: %w", n.m.Name, err)
	}

	n.r.markMachine(n.op, n.m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)
	setMachineBootID(n.op, n.m.Name, bootID)

	now := metav1.Now()
	n.op.Status.Node = &ichorv1alpha1.TalosOperationNodeStatus{Step: ichorv1alpha1.NodeStepRequested, RequestedAt: &now, Message: message}
	n.op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
	n.op.Status.FailureReason = ""

	return n.r.Status().Update(ctx, n.op)
}

// wait keeps the operation InProgress while an action's effect has not shown, and fails it past
// nodeActionTimeout.
func (n *nodeRun) wait(ctx context.Context, why string) (ctrl.Result, error) {
	st := n.op.Status.Node
	if st != nil && st.RequestedAt != nil && time.Since(st.RequestedAt.Time) > nodeActionTimeout {
		return n.pause(ctx, fmt.Errorf("%q did not finish within %s: %s", n.m.Name, nodeActionTimeout, why))
	}

	if st != nil && st.Message != why {
		st.Message = why

		if err := n.r.Status().Update(ctx, n.op); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, nil
}

// complete ends the operation: the machine is Completed, the lock is released.
func (n *nodeRun) complete(ctx context.Context, message string) (ctrl.Result, error) {
	n.r.markMachine(n.op, n.m.Name, ichorv1alpha1.TalosOperationMachinePhaseCompleted)

	n.op.Status.Node.Message = message

	if err := n.lock.Release(ctx, operationLockIdentity(n.op)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, n.r.setPhase(ctx, n.op, ichorv1alpha1.TalosOperationPhaseCompleted, reasonNodeActionDone, message)
}

// controlPlaneGuard is the quorum guard of reboot and shutdown of a control plane: all
// members of etcd are present and, with more than one, at least three exist (stopping one of two
// loses quorum). It also reports whether m is the cluster's only control plane, in which case
// stopping it takes the Kubernetes API away rather than risking quorum.
func (n *nodeRun) controlPlaneGuard(ctx context.Context, doing string) (only bool, err error) {
	if n.m.Spec.Role != roleControlPlane {
		return false, nil
	}

	count, err := deleteguard.CountApprovedControlPlanes(ctx, readerOrClient(n.r.APIReader, n.r.Client), n.cluster.Namespace, n.cluster.Name)
	if err != nil {
		return false, err
	}

	if count == 1 {
		return true, nil
	}

	if count == 2 {
		return false, fmt.Errorf("cluster %q has two control planes: stopping %q would leave one member, below etcd quorum of two", n.cluster.Name, n.m.Name)
	}

	return false, etcdAllMembersPresent(ctx, readerOrClient(n.r.APIReader, n.r.Client), n.talos, n.cluster, n.m, doing)
}

// evacuate runs the storage hook before a node is taken away. done is false while replicas still
// move, and res is then the pending status to return.
func (n *nodeRun) evacuate(ctx context.Context, node, purpose string) (res ctrl.Result, done bool, err error) {
	progress, err := evacuateMachine(ctx, n.r.Client, n.r.Evacuator, n.cluster, n.m, node, purpose)
	if err != nil {
		return ctrl.Result{}, false, err
	}

	switch progress {
	case storage.EvacuationWaiting:
		res, err = n.pendingStatus(ctx, fmt.Sprintf("waiting for storage replicas to move off node %q", node))

		return res, false, err
	case storage.EvacuationPaused:
		return ctrl.Result{}, false, fmt.Errorf("evacuating %q timed out with storage replicas still remaining", node)
	}

	return ctrl.Result{}, true, nil
}

func (n *nodeRun) pendingStatus(ctx context.Context, message string) (ctrl.Result, error) {
	if n.op.Status.Node == nil || n.op.Status.Node.Message != message || n.op.Status.Phase != ichorv1alpha1.TalosOperationPhaseInProgress {
		n.op.Status.Node = &ichorv1alpha1.TalosOperationNodeStatus{Message: message}
		n.op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress

		if err := n.r.Status().Update(ctx, n.op); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, nil
}

// ---- reboot ----

func (n *nodeRun) beginReboot(ctx context.Context) (ctrl.Result, error) {
	node, err := resolveNodeName(ctx, n.r.Client, n.talos, n.m)
	if err != nil {
		return ctrl.Result{}, err
	}

	ready, err := nodeReadyByName(ctx, n.r.KubeClient, n.r.Client, node)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("checking %q's node readiness: %w", n.m.Name, err)
	}

	if !ready {
		return ctrl.Result{}, fmt.Errorf("node %q is not Ready; refusing to reboot it", node)
	}

	if _, err := n.controlPlaneGuard(ctx, "rebooting"); err != nil {
		return ctrl.Result{}, err
	}

	if err := n.lastWorkerGuard(ctx, node, "reboot"); err != nil {
		return ctrl.Result{}, err
	}

	if res, done, err := n.evacuate(ctx, node, "rebooting"); err != nil || !done {
		return res, err
	}

	// Talos 1.14.3's Reboot does not drain, so Ichor does, with the helper the config rollout uses.
	if n.r.KubeClient != nil {
		drained, err := drainForReboot(ctx, n.r.KubeClient, node)
		if err != nil {
			return ctrl.Result{}, err
		}

		if !drained {
			return n.pendingStatus(ctx, fmt.Sprintf("draining node %q (a PodDisruptionBudget may be holding pods back)", node))
		}
	}

	if err := n.recordRequest(ctx, "reboot requested; waiting for the node to come back"); err != nil {
		return ctrl.Result{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, rebootCallTimeout)
	defer cancel()

	if err := n.talos.Reboot(callCtx); err != nil {
		return ctrl.Result{}, fmt.Errorf("reboot API call to %q failed: %w", n.m.Name, err)
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, nil
}

func (n *nodeRun) awaitReboot(ctx context.Context) (ctrl.Result, error) {
	rec, _ := recorded(n.op, n.m.Name)

	bootID, err := n.talos.BootID(n.dial)
	if err != nil {
		return n.wait(ctx, "the node does not answer yet: "+err.Error())
	}

	if bootID == rec.BootID {
		return n.wait(ctx, "the node has not rebooted yet")
	}

	node, err := resolveNodeName(ctx, n.r.Client, n.talos, n.m)
	if err != nil {
		return n.wait(ctx, err.Error())
	}

	ready, err := nodeReadyByName(ctx, n.r.KubeClient, n.r.Client, node)
	if err != nil || !ready {
		return n.wait(ctx, "the node is back but not Ready yet")
	}

	if n.m.Spec.Role == roleControlPlane {
		listCtx, cancelList := dialBound(ctx)
		members, err := n.talos.EtcdMemberList(listCtx)

		cancelList()

		if err != nil {
			return n.wait(ctx, "listing etcd members: "+err.Error())
		}

		if !etcdMemberPresent(members, node) {
			return n.wait(ctx, "the node is not back in etcd")
		}
	}

	if n.r.KubeClient != nil {
		if err := uncordonAfterReboot(ctx, n.r.KubeClient, node); err != nil {
			return n.wait(ctx, "uncordoning the node: "+err.Error())
		}
	}

	if err := endMachineEvacuation(ctx, n.r.Client, n.r.Evacuator, n.cluster, n.m, node); err != nil {
		return ctrl.Result{}, err
	}

	return n.complete(ctx, "the node rebooted, is Ready again and accepts pods")
}

// ---- shutdown ----

func (n *nodeRun) beginShutdown(ctx context.Context) (ctrl.Result, error) {
	only, err := n.controlPlaneGuard(ctx, "stopping")
	if err != nil {
		return ctrl.Result{}, err
	}

	// force=false lets Talos cordon and drain first (it needs the Kubernetes API). The cluster's
	// only control plane takes the API with it, so there is nothing to drain for.
	force := only

	if !force {
		node, err := resolveNodeName(ctx, n.r.Client, n.talos, n.m)
		if err != nil {
			return ctrl.Result{}, err
		}

		if err := n.lastWorkerGuard(ctx, node, "shut down"); err != nil {
			return ctrl.Result{}, err
		}

		if res, done, err := n.evacuate(ctx, node, "shutting down"); err != nil || !done {
			return res, err
		}
	}

	if err := n.recordRequest(ctx, "shutdown requested; waiting for the node to stop"); err != nil {
		return ctrl.Result{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, rebootCallTimeout)
	defer cancel()

	if err := n.talos.Shutdown(callCtx, force); err != nil {
		return ctrl.Result{}, fmt.Errorf("shutdown API call to %q failed: %w", n.m.Name, err)
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, nil
}

// awaitShutdown is reached with a Talos client only while the node still answers; a node that
// no longer does is handled in advanceNode's dial failure, which calls wait. Both paths end
// here through stoppedOrWait.
func (n *nodeRun) awaitShutdown(ctx context.Context) (ctrl.Result, error) {
	rec, _ := recorded(n.op, n.m.Name)

	bootID, err := n.talos.BootID(n.dial)
	if err != nil {
		return n.shutDown(ctx)
	}

	if bootID != rec.BootID {
		return n.complete(ctx, "the node is back on a new boot")
	}

	return n.wait(ctx, "the node is still stopping (Talos is draining it)")
}

// shutDown records the resting state: the node stopped answering and stays off until someone
// powers it on. The lock is released; the operation completes when the node answers on a new
// boot (awaitPowerOn, shared with the cluster shutdown).
func (n *nodeRun) shutDown(ctx context.Context) (ctrl.Result, error) {
	if err := n.lock.Release(ctx, operationLockIdentity(n.op)); err != nil {
		return ctrl.Result{}, err
	}

	n.op.Status.Node.Message = "the node is shut down and stays off until it is powered on"

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, n.r.shutdownStatus(ctx, n.op,
		ichorv1alpha1.TalosOperationPhaseShutDown, reasonShutdownWaiting,
		fmt.Sprintf("node %q was asked to shut down and stopped answering; power it on, and this completes when it answers again", n.m.Name))
}

// ---- restart-service ----

func (n *nodeRun) beginRestartService(ctx context.Context) (ctrl.Result, error) {
	if !slices.Contains(nodeActionServices, n.spec.Service) {
		return ctrl.Result{}, fmt.Errorf("service %q may not be restarted here: only %v are allowed", n.spec.Service, nodeActionServices)
	}

	if err := n.recordRequest(ctx, fmt.Sprintf("restarting service %s", n.spec.Service)); err != nil {
		return ctrl.Result{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, rebootCallTimeout)
	defer cancel()

	if err := n.talos.ServiceRestart(callCtx, n.spec.Service); err != nil {
		return ctrl.Result{}, fmt.Errorf("restarting %s on %q failed: %w", n.spec.Service, n.m.Name, err)
	}

	return ctrl.Result{RequeueAfter: nodeActionSettle}, nil
}

func (n *nodeRun) awaitRestartService(ctx context.Context) (ctrl.Result, error) {
	if at := n.op.Status.Node.RequestedAt; at != nil && time.Since(at.Time) < nodeActionSettle {
		return ctrl.Result{RequeueAfter: nodeActionSettle}, nil
	}

	node, err := resolveNodeName(ctx, n.r.Client, n.talos, n.m)
	if err != nil {
		return n.wait(ctx, err.Error())
	}

	ready, err := nodeReadyByName(ctx, n.r.KubeClient, n.r.Client, node)
	if err != nil || !ready {
		return n.wait(ctx, "the node is not Ready yet")
	}

	if ok, why := n.serviceVerified(); !ok {
		return n.wait(ctx, why)
	}

	return n.complete(ctx, fmt.Sprintf("%s was restarted, is Running and the node is Ready", n.spec.Service))
}

// ---- etcd ----

// etcdMember returns the queried member's status and the member count of a control plane.
func (n *nodeRun) etcdMember() (EtcdStatus, int, error) {
	if n.m.Spec.Role != roleControlPlane {
		return EtcdStatus{}, 0, fmt.Errorf("node %q is a %s: only control planes run etcd", n.m.Name, n.m.Spec.Role)
	}

	st, err := n.talos.EtcdStatus(n.dial)
	if err != nil {
		return EtcdStatus{}, 0, fmt.Errorf("reading %q's etcd status: %w", n.m.Name, err)
	}

	members, err := n.talos.EtcdMemberList(n.dial)
	if err != nil {
		return EtcdStatus{}, 0, fmt.Errorf("listing %q's etcd members: %w", n.m.Name, err)
	}

	return st, len(members), nil
}

func (n *nodeRun) beginDefrag(ctx context.Context) (ctrl.Result, error) {
	st, members, err := n.etcdMember()
	if err != nil {
		return ctrl.Result{}, err
	}

	if members > 1 && st.IsLeader() && !n.spec.AllowLeader {
		return ctrl.Result{}, fmt.Errorf("%q leads etcd, and defragmenting the leader blocks writes for every member: "+
			"defragment a follower, hand leadership over first, or allow the leader explicitly", n.m.Name)
	}

	if members > 1 {
		if err := etcdAllMembersPresent(ctx, readerOrClient(n.r.APIReader, n.r.Client), n.talos, n.cluster, n.m, "defragmenting"); err != nil {
			return ctrl.Result{}, err
		}
	}

	if _, err := n.pendingStatus(ctx, "defragmenting etcd on "+n.m.Name); err != nil {
		return ctrl.Result{}, err
	}

	stop := renewWhile(ctx, n.lock, operationLockIdentity(n.op), removalRenewInterval)
	defer stop()

	callCtx, cancel := context.WithTimeout(ctx, defragTimeout)
	defer cancel()

	if err := n.talos.EtcdDefragment(callCtx); err != nil {
		return ctrl.Result{}, fmt.Errorf("defragmenting etcd on %q failed: %w", n.m.Name, err)
	}

	alarms, err := n.talos.EtcdAlarms(n.dial)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading etcd alarms after the defragmentation: %w", err)
	}

	if len(alarms) > 0 {
		return ctrl.Result{}, fmt.Errorf("etcd still reports alarm(s) %v after defragmenting %q: they are not cleared by Ichor", alarms, n.m.Name)
	}

	return n.complete(ctx, "etcd was defragmented and reports no alarm")
}

func (n *nodeRun) beginForfeit(ctx context.Context) (ctrl.Result, error) {
	st, members, err := n.etcdMember()
	if err != nil {
		return ctrl.Result{}, err
	}

	if members < 2 {
		return ctrl.Result{}, fmt.Errorf("etcd has a single member: there is no other member to take leadership from %q", n.m.Name)
	}

	if !st.IsLeader() {
		return ctrl.Result{}, fmt.Errorf("%q does not lead etcd (member %x does): nothing to hand over", n.m.Name, st.LeaderID)
	}

	if err := etcdAllMembersPresent(ctx, readerOrClient(n.r.APIReader, n.r.Client), n.talos, n.cluster, n.m, "handing over leadership of"); err != nil {
		return ctrl.Result{}, err
	}

	if err := n.recordRequest(ctx, "leadership handover requested"); err != nil {
		return ctrl.Result{}, err
	}

	if err := n.talos.EtcdForfeitLeadership(n.dial); err != nil {
		return ctrl.Result{}, fmt.Errorf("forfeiting etcd leadership on %q failed: %w", n.m.Name, err)
	}

	return ctrl.Result{RequeueAfter: nodeActionSettle}, nil
}

func (n *nodeRun) awaitForfeit(ctx context.Context) (ctrl.Result, error) {
	st, err := n.talos.EtcdStatus(n.dial)
	if err != nil {
		return n.wait(ctx, "reading etcd status: "+err.Error())
	}

	if st.IsLeader() || st.LeaderID == 0 {
		return n.wait(ctx, "no new leader yet")
	}

	return n.complete(ctx, fmt.Sprintf("etcd member %x now leads", st.LeaderID))
}

// ---- reset ----

// beginReset hands the machine to the removal flow with the reset opt-in: that
// flow already owns the drain, the storage evacuation, the etcd member and the Reset call, and a
// second path would have to repeat their guards. The operation holds the cluster lock the removal
// needs, so it releases it first and then only watches the machine go.
func (n *nodeRun) beginReset(ctx context.Context) (ctrl.Result, error) {
	if n.spec.Confirm != n.m.Name {
		return ctrl.Result{}, fmt.Errorf("reset needs confirm equal to the node name %q", n.m.Name)
	}

	if n.m.Spec.Role == roleControlPlane {
		count, err := deleteguard.CountApprovedControlPlanes(ctx, readerOrClient(n.r.APIReader, n.r.Client), n.cluster.Namespace, n.cluster.Name)
		if err != nil {
			return ctrl.Result{}, err
		}

		if err := checkControlPlaneRemoval(count, int(n.cluster.Spec.ControlPlane.Count)); err != nil {
			return ctrl.Result{}, err
		}
	}

	if n.m.Annotations == nil {
		n.m.Annotations = map[string]string{}
	}

	n.m.Annotations[resetOnDeleteAnnotation] = resetOnDeleteValue
	if n.spec.WipeMode != "" {
		n.m.Annotations[resetWipeModeAnnotation] = n.spec.WipeMode
	}
	if n.spec.PowerOff {
		n.m.Annotations[resetPowerOffAnnotation] = resetOnDeleteValue
	}
	n.m.Annotations[deleteguard.AllowDeleteAnnotation] = deleteguard.AllowDeleteValue

	// The finalizer is added here, not left to the machine reconciler, so the delete below cannot
	// win a race and skip the removal flow.
	controllerutil.AddFinalizer(n.m, machineRemovalFinalizer)

	if err := n.r.Update(ctx, n.m); err != nil {
		return ctrl.Result{}, fmt.Errorf("marking %q for removal with a reset: %w", n.m.Name, err)
	}

	n.r.markMachine(n.op, n.m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)
	n.op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
	n.op.Status.Node = &ichorv1alpha1.TalosOperationNodeStatus{
		Step: ichorv1alpha1.NodeStepRemoving, Message: "the node was handed to the removal flow with a reset",
		// The wait follows this object, not the name: discovery may recreate a pending machine with
		// the same name (the node's UUID) the moment the old one is gone.
		MachineUID: n.m.UID,
	}

	if err := n.r.Status().Update(ctx, n.op); err != nil {
		return ctrl.Result{}, err
	}

	if err := n.lock.Release(ctx, operationLockIdentity(n.op)); err != nil {
		return ctrl.Result{}, err
	}

	return n.r.awaitRemoval(ctx, n.op)
}

// awaitRemoval deletes the machine (once) and waits until the removal flow has finished with it.
// It runs without the cluster lock: the removal takes it. The machine it follows is the one the
// reset started on, by UID: gone, or replaced by an object with another UID (discovery recreates
// a pending machine under the same name within seconds), means the removal is done, and the new
// object is never touched.
func (r *TalosOperationReconciler) awaitRemoval(ctx context.Context, op *ichorv1alpha1.TalosOperation) (ctrl.Result, error) {
	m := &ichorv1alpha1.TalosMachine{}

	err := readerOrClient(r.APIReader, r.Client).Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.Node.MachineRef.Name}, m)

	// shortcut: an operation recorded before machineUID existed has none and follows the name.
	if uid := op.Status.Node.MachineUID; apierrors.IsNotFound(err) || (err == nil && uid != "" && m.UID != uid) {
		r.markMachine(op, op.Spec.Node.MachineRef.Name, ichorv1alpha1.TalosOperationMachinePhaseCompleted)
		op.Status.Node.Message = "the node was removed and its node reset"

		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhaseCompleted, reasonNodeActionDone, op.Status.Node.Message)
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	if m.DeletionTimestamp.IsZero() {
		if err := r.Delete(ctx, m, client.Preconditions{UID: &m.UID}); err != nil && !apierrors.IsNotFound(err) {
			return r.nodeFailed(ctx, op, fmt.Errorf("removing %q: %w", m.Name, err))
		}
	}

	msg := "waiting for the removal of the node"
	if c := apimeta.FindStatusCondition(m.Status.Conditions, conditionRemoval); c != nil && c.Message != "" {
		msg = "removal: " + c.Message
	}

	if op.Status.Node.Message != msg {
		op.Status.Node.Message = msg

		if err := r.Status().Update(ctx, op); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, nil
}
