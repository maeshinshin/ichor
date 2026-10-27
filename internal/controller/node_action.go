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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/storage"
)

const (
	reasonNodeActionWaiting = "WaitingForNode"
	reasonNodeActionDone    = "NodeActionDone"

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
		return r.nodeFailed(ctx, op, fmt.Errorf("machine %q: %w", spec.MachineRef.Name, err))
	}

	if m.Spec.ClusterRef.Name != cluster.Name || !m.Spec.IsApproved() {
		return r.nodeFailed(ctx, op, fmt.Errorf("machine %q is not an approved machine of cluster %q", m.Name, cluster.Name))
	}

	if err := lock.Renew(ctx, controllerIdentity); err != nil {
		return ctrl.Result{}, err
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

	if waiting {
		res, err := n.await(ctx)
		if err != nil {
			return r.nodeFailed(ctx, op, err)
		}

		return res, nil
	}

	res, err := n.begin(ctx)
	if err != nil {
		n.cleanup(ctx)

		return r.nodeFailed(ctx, op, err)
	}

	return res, nil
}

// begin checks the guards and sends the action.
func (n *nodeRun) begin(ctx context.Context) (ctrl.Result, error) {
	switch n.spec.Action {
	case ichorv1alpha1.NodeActionReboot:
		return n.beginReboot(ctx)
	case ichorv1alpha1.NodeActionShutdown:
		return n.beginShutdown(ctx)
	case ichorv1alpha1.NodeActionRestartService:
		return n.beginRestartService(ctx)
	case ichorv1alpha1.NodeActionEtcdDefrag:
		return n.beginDefrag(ctx)
	case ichorv1alpha1.NodeActionEtcdForfeitLeadership:
		return n.beginForfeit(ctx)
	case ichorv1alpha1.NodeActionReset:
		return n.beginReset(ctx)
	}

	return ctrl.Result{}, fmt.Errorf("unknown node action %q", n.spec.Action)
}

// await runs while an action that was sent has not shown its effect yet.
func (n *nodeRun) await(ctx context.Context) (ctrl.Result, error) {
	switch n.spec.Action {
	case ichorv1alpha1.NodeActionReboot:
		return n.awaitReboot(ctx)
	case ichorv1alpha1.NodeActionShutdown:
		return n.awaitShutdown(ctx)
	case ichorv1alpha1.NodeActionRestartService:
		return n.awaitRestartService(ctx)
	case ichorv1alpha1.NodeActionEtcdForfeitLeadership:
		return n.awaitForfeit(ctx)
	}

	return ctrl.Result{}, fmt.Errorf("node action %q has nothing to wait for", n.spec.Action)
}

// cleanup undoes what a step that failed before sending anything left behind: the cordon and
// the storage evacuation. A node must not stay unschedulable because an operation paused.
func (n *nodeRun) cleanup(ctx context.Context) {
	if n.spec.Action != ichorv1alpha1.NodeActionReboot && n.spec.Action != ichorv1alpha1.NodeActionShutdown {
		return
	}

	node, err := resolveNodeName(ctx, n.r.Client, n.talos, n.m)
	if err != nil {
		return
	}

	if n.r.KubeClient != nil {
		_ = uncordonAfterReboot(ctx, n.r.KubeClient, node)
	}

	_ = endMachineEvacuation(ctx, n.r.Client, n.r.Evacuator, n.cluster, n.m, node)
}

// recordRequest persists, before the action is sent, the boot ID the node is leaving and the
// time, so a controller killed between the record and the call resumes into "waiting" and a
// call is never repeated.
func (n *nodeRun) recordRequest(ctx context.Context, message string) error {
	bootID, err := n.talos.BootID(n.dial)
	if err != nil {
		return fmt.Errorf("reading %q's boot ID before the action: %w", n.m.Name, err)
	}

	setMachineStatus(n.op, n.m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)
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
		return n.r.nodeFailed(ctx, n.op, fmt.Errorf("%q did not finish within %s: %s", n.m.Name, nodeActionTimeout, why))
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
	setMachineStatus(n.op, n.m.Name, ichorv1alpha1.TalosOperationMachinePhaseCompleted)

	if n.op.Status.Node == nil {
		n.op.Status.Node = &ichorv1alpha1.TalosOperationNodeStatus{}
	}

	n.op.Status.Node.Message = message

	if err := n.lock.Release(ctx, controllerIdentity); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, n.r.setPhase(ctx, n.op, ichorv1alpha1.TalosOperationPhaseCompleted, reasonNodeActionDone, message)
}

// nodeName is the Kubernetes node (and etcd member) name of the machine.
func (n *nodeRun) nodeName(ctx context.Context) (string, error) {
	return resolveNodeName(ctx, n.r.Client, n.talos, n.m)
}

// nodeReady reads the Node's Ready condition live when a clientset is wired, else from the cache.
func (n *nodeRun) nodeReady(ctx context.Context, node string) (bool, error) {
	if n.r.KubeClient != nil {
		got, err := n.r.KubeClient.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		return nodeIsReady(got), nil
	}

	var got corev1.Node
	if err := n.r.Get(ctx, types.NamespacedName{Name: node}, &got); err != nil {
		return false, err
	}

	return nodeIsReady(&got), nil
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

	return false, etcdAllMembersPresent(ctx, n.dial, readerOrClient(n.r.APIReader, n.r.Client), n.talos, n.cluster, n.m, doing)
}

// evacuate runs the storage hook before a node is taken away. done is false while replicas still
// move.
func (n *nodeRun) evacuate(ctx context.Context, node, purpose string) (done bool, err error) {
	progress, err := evacuateMachine(ctx, n.r.Client, n.r.Evacuator, n.cluster, n.m, node, purpose)
	if err != nil {
		return false, err
	}

	switch progress {
	case storage.EvacuationWaiting:
		return false, nil
	case storage.EvacuationPaused:
		return false, fmt.Errorf("evacuating %q timed out with storage replicas still remaining", node)
	}

	return true, nil
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
	node, err := n.nodeName(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	ready, err := n.nodeReady(ctx, node)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("checking %q's node readiness: %w", n.m.Name, err)
	}

	if !ready {
		return ctrl.Result{}, fmt.Errorf("node %q is not Ready; refusing to reboot it", node)
	}

	if _, err := n.controlPlaneGuard(ctx, "rebooting"); err != nil {
		return ctrl.Result{}, err
	}

	if done, err := n.evacuate(ctx, node, "rebooting"); err != nil || !done {
		if err != nil {
			return ctrl.Result{}, err
		}

		return n.pendingStatus(ctx, fmt.Sprintf("waiting for storage replicas to move off node %q", node))
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

	node, err := n.nodeName(ctx)
	if err != nil {
		return n.wait(ctx, err.Error())
	}

	ready, err := n.nodeReady(ctx, node)
	if err != nil || !ready {
		return n.wait(ctx, "the node is back but not Ready yet")
	}

	if n.m.Spec.Role == roleControlPlane {
		members, err := n.talos.EtcdMemberList(n.dial)
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
		node, err := n.nodeName(ctx)
		if err != nil {
			return ctrl.Result{}, err
		}

		if done, err := n.evacuate(ctx, node, "shutting down"); err != nil || !done {
			if err != nil {
				return ctrl.Result{}, err
			}

			return n.pendingStatus(ctx, fmt.Sprintf("waiting for storage replicas to move off node %q", node))
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
	if err := n.lock.Release(ctx, controllerIdentity); err != nil {
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

	node, err := n.nodeName(ctx)
	if err != nil {
		return n.wait(ctx, err.Error())
	}

	ready, err := n.nodeReady(ctx, node)
	if err != nil || !ready {
		return n.wait(ctx, "the node is not Ready yet")
	}

	return n.complete(ctx, fmt.Sprintf("%s was restarted and the node is Ready", n.spec.Service))
}

// ---- etcd ----

// etcdMember returns the queried member's status and the member count of a control plane.
func (n *nodeRun) etcdMember() (EtcdStatus, int, error) {
	if n.m.Spec.Role != roleControlPlane {
		return EtcdStatus{}, 0, fmt.Errorf("machine %q is a %s: only control planes run etcd", n.m.Name, n.m.Spec.Role)
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
		if err := etcdAllMembersPresent(ctx, n.dial, readerOrClient(n.r.APIReader, n.r.Client), n.talos, n.cluster, n.m, "defragmenting"); err != nil {
			return ctrl.Result{}, err
		}
	}

	if _, err := n.pendingStatus(ctx, "defragmenting etcd on "+n.m.Name); err != nil {
		return ctrl.Result{}, err
	}

	stop := renewWhile(ctx, n.lock, controllerIdentity, removalRenewInterval)
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

	if err := etcdAllMembersPresent(ctx, n.dial, readerOrClient(n.r.APIReader, n.r.Client), n.talos, n.cluster, n.m, "handing over leadership of"); err != nil {
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
		return ctrl.Result{}, fmt.Errorf("reset needs confirm equal to the machine name %q", n.m.Name)
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
	n.m.Annotations[deleteguard.AllowDeleteAnnotation] = deleteguard.AllowDeleteValue

	// The finalizer is added here, not left to the machine reconciler, so the delete below cannot
	// win a race and skip the removal flow.
	controllerutil.AddFinalizer(n.m, machineRemovalFinalizer)

	if err := n.r.Update(ctx, n.m); err != nil {
		return ctrl.Result{}, fmt.Errorf("marking %q for removal with a reset: %w", n.m.Name, err)
	}

	setMachineStatus(n.op, n.m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)
	n.op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
	n.op.Status.Node = &ichorv1alpha1.TalosOperationNodeStatus{
		Step: ichorv1alpha1.NodeStepRemoving, Message: "the machine was handed to the removal flow with a reset",
	}

	if err := n.r.Status().Update(ctx, n.op); err != nil {
		return ctrl.Result{}, err
	}

	if err := n.lock.Release(ctx, controllerIdentity); err != nil {
		return ctrl.Result{}, err
	}

	return n.r.awaitRemoval(ctx, n.op)
}

// awaitRemoval deletes the machine (once) and waits until the removal flow has finished with it.
// It runs without the cluster lock: the removal takes it.
func (r *TalosOperationReconciler) awaitRemoval(ctx context.Context, op *ichorv1alpha1.TalosOperation) (ctrl.Result, error) {
	m := &ichorv1alpha1.TalosMachine{}

	err := readerOrClient(r.APIReader, r.Client).Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.Node.MachineRef.Name}, m)
	if apierrors.IsNotFound(err) {
		setMachineStatus(op, op.Spec.Node.MachineRef.Name, ichorv1alpha1.TalosOperationMachinePhaseCompleted)
		op.Status.Node.Message = "the machine was removed and its node reset"

		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhaseCompleted, reasonNodeActionDone, op.Status.Node.Message)
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	if m.DeletionTimestamp.IsZero() {
		if err := r.Delete(ctx, m); err != nil && !apierrors.IsNotFound(err) {
			return r.nodeFailed(ctx, op, fmt.Errorf("removing %q: %w", m.Name, err))
		}
	}

	msg := "waiting for the removal of the machine"
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
