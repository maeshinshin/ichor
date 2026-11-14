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

package uiapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// Node actions: one TalosOperation of kind node per action, planned and created
// through OperationService like every other disruptive operation. This file only describes and
// writes; the controller is the only writer to the node.

// nodeServices is the restart-service allowlist, the same as the controller's and the CRD's enum.
var nodeServices = []string{"kubelet", "containerd", "cri"}

const stepEvacuate = "Move storage replicas off the node, if a storage system is set up."

// nodeActions pairs each wire action with its spec name; remove is planning-only and never reaches a
// TalosOperation.
var nodeActions = []struct {
	wire pb.NodeAction
	spec string
}{
	{pb.NodeAction_NODE_ACTION_REBOOT, ichorv1alpha1.NodeActionReboot},
	{pb.NodeAction_NODE_ACTION_SHUTDOWN, ichorv1alpha1.NodeActionShutdown},
	{pb.NodeAction_NODE_ACTION_RESTART_SERVICE, ichorv1alpha1.NodeActionRestartService},
	{pb.NodeAction_NODE_ACTION_ETCD_DEFRAG, ichorv1alpha1.NodeActionEtcdDefrag},
	{pb.NodeAction_NODE_ACTION_ETCD_FORFEIT_LEADERSHIP, ichorv1alpha1.NodeActionEtcdForfeitLeadership},
	{pb.NodeAction_NODE_ACTION_RESET, ichorv1alpha1.NodeActionReset},
	{pb.NodeAction_NODE_ACTION_REMOVE, nodeActionRemove},
}

const nodeActionRemove = "remove"

func nodeActionToSpec(a pb.NodeAction) (string, error) {
	for _, n := range nodeActions {
		if n.wire == a {
			return n.spec, nil
		}
	}

	return "", connect.NewError(connect.CodeInvalidArgument, errors.New("node action is required"))
}

func nodeActionToPB(a string) pb.NodeAction {
	for _, n := range nodeActions {
		if n.spec == a {
			return n.wire
		}
	}

	return pb.NodeAction_NODE_ACTION_UNSPECIFIED
}

// nodePlan is what planNode learned, kept for CreateOperation.
type nodePlan struct {
	spec    ichorv1alpha1.TalosOperationNodeSpec
	action  string
	machine *ichorv1alpha1.TalosMachine
	bg      *pb.Override
	// belowCount is set by nodeBlocked when the removal is allowed below the declared count because
	// the control plane is unreachable.
	belowCount bool
}

// machineUnreachable reports whether m's Ready says its node has stopped answering.
func machineUnreachable(m *ichorv1alpha1.TalosMachine) bool {
	c := apimeta.FindStatusCondition(m.Status.Conditions, "Ready")

	return c != nil && c.Status == metav1.ConditionFalse && c.Reason == ichorv1alpha1.TalosMachineReasonNodeUnreachable
}

func invalid(format string, args ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, args...))
}

//nolint:gocyclo // one switch over the actions, each a few lines
func (s *Server) planNode(ctx context.Context, req *pb.NodeActionRequest) (*opPlan, error) {
	if req == nil || req.GetMachine().GetName() == "" {
		return nil, invalid("a node operation needs the node")
	}

	action, err := nodeActionToSpec(req.GetAction())
	if err != nil {
		return nil, err
	}

	m, err := s.resolveMachine(ctx, req.GetMachine())
	if err != nil {
		return nil, err
	}

	cluster, err := s.loadCluster(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name})
	if err != nil {
		return nil, err
	}

	if req.GetService() != "" && action != ichorv1alpha1.NodeActionRestartService {
		return nil, invalid("a service only goes with restart-service")
	}

	if req.GetAllowLeader() && action != ichorv1alpha1.NodeActionEtcdDefrag {
		return nil, invalid("allow leader only goes with etcd defragmentation")
	}

	if wm := req.GetWipeMode(); wm != "" {
		if action != ichorv1alpha1.NodeActionReset && action != nodeActionRemove {
			return nil, invalid("a wipe mode only goes with reset and remove")
		}

		if wm != ichorv1alpha1.WipeModeSystemDisk && wm != ichorv1alpha1.WipeModeAll {
			return nil, invalid("wipe mode %q is not one of %s, %s", wm, ichorv1alpha1.WipeModeSystemDisk, ichorv1alpha1.WipeModeAll)
		}
	}

	if req.GetReboot() && action != nodeActionRemove {
		return nil, invalid("reboot only goes with remove: a reset always reboots")
	}

	np := &nodePlan{
		action: action, machine: m, bg: req.GetOverride(),
		spec: ichorv1alpha1.TalosOperationNodeSpec{
			MachineRef: corev1.LocalObjectReference{Name: m.Name}, Action: action,
			Service: req.GetService(), AllowLeader: req.GetAllowLeader(), Confirm: req.GetConfirm(), WipeMode: req.GetWipeMode(),
			PowerOff: action == nodeActionRemove && !req.GetReboot(),
		},
	}
	p := &opPlan{
		kind: ichorv1alpha1.TalosOperationKindNode, cluster: cluster, node: np,
		resp: &pb.PlanOperationResponse{
			MaxUnavailable: 1, Effect: pb.OperationEffect_OPERATION_EFFECT_NONE,
			Machines: []*pb.PlannedMachine{{Name: m.Name, Role: m.Spec.Role, Step: 1}},
		},
	}

	cps, err := s.approvedControlPlanes(ctx, cluster, m)
	if err != nil {
		return nil, err
	}

	describeNode(p.resp, m, action, np.spec, cps)

	p.resp.Blocked, err = s.nodeBlocked(ctx, np, cluster, cps)
	if err != nil {
		return nil, err
	}

	if np.belowCount {
		p.resp.Warning = fmt.Sprintf("Control plane %q does not answer, so it is removed although that leaves fewer control planes than the declared %d. "+
			"The controller first checks that its etcd member is unhealthy and refuses otherwise. Approve a replacement afterwards to get back to %d.",
			m.Name, cluster.Spec.ControlPlane.Count, cluster.Spec.ControlPlane.Count)
	}

	return p, nil
}

// approvedControlPlanes counts the approved control planes of the cluster that are not being
// deleted, including m itself when it is one.
func (s *Server) approvedControlPlanes(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine) (int, error) {
	var list ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &list, client.InNamespace(cluster.Namespace)); err != nil {
		return 0, mapListError("nodes", err)
	}

	n := 0

	for i := range list.Items {
		c := &list.Items[i]
		if c.Spec.ClusterRef.Name == cluster.Name && c.Spec.IsApproved() && c.Spec.Role == opRoleControlPlane &&
			(c.DeletionTimestamp.IsZero() || c.Name == m.Name) {
			n++
		}
	}

	return n, nil
}

// describeNode fills the plain-English plan of an action.
//
//nolint:gocyclo // one switch over the actions
func describeNode(resp *pb.PlanOperationResponse, m *ichorv1alpha1.TalosMachine, action string, spec ichorv1alpha1.TalosOperationNodeSpec, cps int) {
	isCP := m.Spec.Role == opRoleControlPlane
	only := isCP && cps == 1
	name := m.Name

	check := "Check that the node is Ready."
	if isCP {
		check = "Check that the node is Ready and that etcd lists every control plane, so the cluster keeps quorum."
	}

	switch action {
	case ichorv1alpha1.NodeActionReboot:
		resp.Steps = []string{
			check,
			stepEvacuate,
			"Cordon the node and drain it, respecting PodDisruptionBudgets.",
			"Restart the node gracefully.",
			"Wait until it has booted again, is Ready" + map[bool]string{true: " and is back in etcd", false: ""}[isCP] + ".",
			"Let pods run on it again.",
		}
		resp.Effect = pb.OperationEffect_OPERATION_EFFECT_REBOOT
		resp.Warning = "Pods on this node are evicted and start again elsewhere. If Ichor's controller runs on this node, it restarts too and carries on afterwards."
		resp.KubernetesApiUnavailable = only

		if only {
			resp.Warning += " This is the only control plane: the Kubernetes API is unavailable until the node is back."
		}
	case ichorv1alpha1.NodeActionShutdown:
		resp.Steps = []string{
			"Check that stopping the node keeps etcd quorum.",
			stepEvacuate,
			"Ask the node to drain and power off.",
			"Wait until it stops answering.",
			"The node stays off until you power it on; this operation then finishes.",
		}
		resp.Effect = pb.OperationEffect_OPERATION_EFFECT_STOP
		resp.Warning = "The node stays off until someone powers it on outside Ichor. Pods on it are evicted."
		resp.ConfirmName = name
		resp.KubernetesApiUnavailable = only

		if only {
			resp.Steps[2] = "Ask the node to power off. Nothing is drained: the Kubernetes API goes away with it."
			resp.Warning = "This is the only control plane. The Kubernetes API goes away and stays away until you power the node on."
		}
	case ichorv1alpha1.NodeActionRestartService:
		resp.Steps = []string{
			fmt.Sprintf("Restart the Talos service %q on the node.", spec.Service),
			"Wait until the node is Ready.",
		}
		resp.Effect = pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS
		resp.Warning = fmt.Sprintf("%s is unavailable on this node for a moment. Pods keep running.", spec.Service)
	case ichorv1alpha1.NodeActionEtcdDefrag:
		resp.Steps = []string{
			"Check that this node is a control plane and not the etcd leader.",
			"Defragment this node's etcd member.",
			"Check that etcd reports no alarm afterwards.",
		}
		resp.Warning = "This member is slow or unavailable while it defragments. Run it on one node at a time."

		if spec.AllowLeader {
			resp.Steps[0] = "Check that this node is a control plane."
			resp.Warning = "If this node leads etcd, writes pause for the whole cluster while it defragments."
		}
	case ichorv1alpha1.NodeActionEtcdForfeitLeadership:
		resp.Steps = []string{
			"Check that this node leads etcd and that another member can take over.",
			"Ask the member to hand leadership over.",
			"Check that a different member leads.",
		}
		resp.Warning = "Writes pause for a moment while a new leader is elected."
	case ichorv1alpha1.NodeActionReset, nodeActionRemove:
		// Remove and reset are one flow: a reset only differs in always rebooting afterwards.
		powerOff := action == nodeActionRemove && spec.PowerOff
		after := "reboot it into maintenance mode"
		if powerOff {
			after = "power it off"
		}

		wipe := "Wipe the node's STATE and EPHEMERAL partitions and " + after + "."

		switch spec.WipeMode {
		case ichorv1alpha1.WipeModeSystemDisk:
			wipe = "Wipe the node's whole system disk (boot and EFI included) and " + after + "; it starts from its boot image again, so the disk is installed afresh."
		case ichorv1alpha1.WipeModeAll:
			wipe = "Wipe the node's whole system disk (boot and EFI included), and " + after + "; it starts from its boot image again. User disks are not wiped."
		}

		resp.Steps = removalSteps(isCP, wipe)
		resp.Warning = removeWarning(powerOff, spec.WipeMode)
		resp.ConfirmName, resp.OverrideRequired = name, true
	}
}

// removeWarning is the text of the remove plan: the node's STATE and EPHEMERAL partitions are
// wiped, so the data is gone, and what the node does afterwards depends on the chosen end state.
func removeWarning(powerOff bool, wipeMode string) string {
	wiped := "The node's STATE and EPHEMERAL partitions are wiped; user disks are not touched."
	switch wipeMode {
	case ichorv1alpha1.WipeModeSystemDisk:
		wiped = "The node's whole system disk is wiped; user disks are not touched."
	case ichorv1alpha1.WipeModeAll:
		wiped = "The node's whole system disk is wiped; user disks are not touched."
	}

	after := "It reboots into maintenance mode and shows up as a new pending node, so you can add it again."
	if powerOff {
		after = "It is powered off. To add it again, power it on: it boots into maintenance mode and shows up as a new pending node."
	}

	return "This cannot be undone. The node leaves the cluster, its Kubernetes Node is deleted and Ichor deletes this machine. " + wiped + " " + after
}

func removalSteps(isCP bool, wipe string) []string {
	steps := []string{stepEvacuate, "Cordon the node and drain it."}
	if isCP {
		steps = append(steps, "Remove the node's etcd member.")
	}

	return append(steps, wipe, "Delete the Kubernetes Node.", "Delete the node from Ichor.")
}

// nodeBlocked is the first reason the action cannot start now, or "". The checks that need the
// node itself (is it the etcd leader, are all members present) are the controller's; the plan only
// says what it can know from the API.
//
//nolint:gocyclo // one list of independent refusals
func (s *Server) nodeBlocked(ctx context.Context, np *nodePlan, cluster *ichorv1alpha1.TalosCluster, cps int) (string, error) {
	m, action := np.machine, np.action
	isCP := m.Spec.Role == opRoleControlPlane

	switch {
	case !m.Spec.IsApproved():
		return fmt.Sprintf("node %q is not approved, so it is not a node of the cluster yet", m.Name), nil
	case !m.DeletionTimestamp.IsZero():
		return fmt.Sprintf("node %q is being removed", m.Name), nil
	case action == ichorv1alpha1.NodeActionRestartService && np.spec.Service == "":
		return "choose the service to restart: " + strings.Join(nodeServices, ", "), nil
	case action == ichorv1alpha1.NodeActionRestartService && !slices.Contains(nodeServices, np.spec.Service):
		return fmt.Sprintf("service %q cannot be restarted here: only %s are allowed", np.spec.Service, strings.Join(nodeServices, ", ")), nil
	case (action == ichorv1alpha1.NodeActionEtcdDefrag || action == ichorv1alpha1.NodeActionEtcdForfeitLeadership) && !isCP:
		return fmt.Sprintf("%q is a worker: only control planes run etcd", m.Name), nil
	case (action == ichorv1alpha1.NodeActionReboot || action == ichorv1alpha1.NodeActionShutdown) && isCP && cps == 2:
		return "this cluster has two control planes: stopping one leaves a single etcd member, below quorum", nil
	case (action == ichorv1alpha1.NodeActionReset || action == nodeActionRemove) && isCP:
		if err := deleteguard.CheckControlPlaneQuorum(cps); err != nil {
			return strings.TrimPrefix(err.Error(), "delete refused: "), nil
		}

		if err := deleteguard.CheckControlPlaneCount(cps, int(cluster.Spec.ControlPlane.Count)); err != nil {
			// A control plane that does not answer is the one to replace: removing it below the
			// declared count is how the replacement's etcd can join. The controller
			// checks the etcd member itself before it removes anything.
			if action != nodeActionRemove || !machineUnreachable(m) {
				return strings.TrimPrefix(err.Error(), "delete refused: "), nil
			}

			np.belowCount = true
		}
	}

	// The removal of a control plane ends an even count, so an operation parked by that count
	// does not block it; a reset keeps the controller's pause and is pointed at the removal instead.
	if action == ichorv1alpha1.NodeActionReset && isCP && transitioningCount(cps) {
		return fmt.Sprintf("the cluster has %d approved control planes (even): a replacement is in progress. Remove the node instead, which ends it", cps), nil
	}

	return s.unfinishedOperationFor(ctx, cluster, action == nodeActionRemove && isCP)
}

func transitioningCount(cps int) bool { return cps > 0 && cps%2 == 0 }

// createOperation creates op for cluster after a dry run, so a spec the API refuses is refused before anything else is written.
func (s *Server) createOperation(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, op *ichorv1alpha1.TalosOperation) error {
	key := client.ObjectKeyFromObject(cluster)

	if err := s.Create(ctx, op.DeepCopy(), client.DryRunAll, strictFieldValidation); err != nil {
		return mapOperationWriteError("creating operation for", key, err)
	}

	if err := s.Create(ctx, op, strictFieldValidation); err != nil {
		return mapOperationWriteError("creating operation for", key, err)
	}

	return nil
}

// createNodeOperation checks what the plan could not and creates the TalosOperation.
func (s *Server) createNodeOperation(ctx context.Context, p *opPlan) (*pb.CreateOperationResponse, error) {
	np := p.node

	if np.action == nodeActionRemove {
		return nil, invalid("removing a node is not an operation: use RemoveMachine")
	}

	// The right to start a reset or a shutdown, then override on top for the reset.
	if err := s.authorizeStart(ctx, &ichorv1alpha1.TalosOperationSpec{Kind: p.kind, Node: &np.spec}, p.cluster.Namespace); err != nil {
		return nil, err
	}

	// An override (a non-nil request field) is honoured for a reset, and for a reboot or shutdown, where
	// it overrides the controller's last-worker guard. No reason is asked for.
	override := np.bg != nil && (np.action == ichorv1alpha1.NodeActionReset ||
		np.action == ichorv1alpha1.NodeActionReboot || np.action == ichorv1alpha1.NodeActionShutdown)

	if np.action == ichorv1alpha1.NodeActionReset {
		if np.spec.Confirm != np.machine.Name {
			return nil, invalid("type the node name %q to confirm the reset", np.machine.Name)
		}

		if !override {
			return nil, invalid("a reset needs the override: it is checked against the override permission on the node")
		}
	}

	if override {
		if err := s.authorizeOverride(ctx, np.machine); err != nil {
			return nil, err
		}

		np.spec.Override = true
	}

	cluster := p.cluster
	op := s.newOperation(ctx, cluster, ichorv1alpha1.TalosOperationSpec{Kind: p.kind, Node: &np.spec})

	if err := s.createOperation(ctx, cluster, op); err != nil {
		return nil, err
	}

	audit := s.opAudit(ctx, "create", objectRef(op.Namespace, op.Name))

	if override {
		audit.Action += "-override"
		msg := fmt.Sprintf("%s of %s requested by %s under override", np.action, np.machine.Name, audit.Actor)

		if err := s.emitWarningEvent(ctx, np.machine, kindTalosMachine, "Override", msg); err != nil {
			audit.Reason = "the Kubernetes Event could not be recorded"
		}
	}

	return &pb.CreateOperationResponse{Operation: operationToPB(op), Audit: audit}, nil
}

// RemoveMachine deletes an approved machine, which starts the removal flow of the controller: drain, etcd member
// removal for a control plane, the reset (STATE and EPHEMERAL wiped, or more with a wipe mode,
// then power off or reboot), the Kubernetes Node, then the TalosMachine goes. The annotations that let the
// delete through and ask for the reset are set here, for this one delete.
func (s *Server) RemoveMachine(
	ctx context.Context, req *connect.Request[pb.RemoveMachineRequest],
) (*connect.Response[pb.RemoveMachineResponse], error) {
	ref := req.Msg.GetMachine()

	p, err := s.planNode(ctx, &pb.NodeActionRequest{
		Machine: ref, Action: pb.NodeAction_NODE_ACTION_REMOVE, Reboot: req.Msg.GetReboot(), Override: req.Msg.GetOverride(), WipeMode: req.Msg.GetWipeMode(),
	})
	if err != nil {
		return nil, err
	}

	if p.resp.GetBlocked() != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(p.resp.GetBlocked()))
	}

	m := p.node.machine
	if c := req.Msg.GetConfirm(); c != m.Name {
		return nil, invalid("type the node name %q to confirm the removal", m.Name)
	}

	// The remove wipes the node, so it needs the override like a reset.
	if req.Msg.GetOverride() == nil {
		return nil, invalid("a remove wipes the node: it needs the override, checked against the override permission on the node")
	}

	if err := s.authorizeOverride(ctx, m); err != nil {
		return nil, err
	}

	metav1.SetMetaDataAnnotation(&m.ObjectMeta, deleteguard.AllowDeleteAnnotation, deleteguard.AllowDeleteValue)
	metav1.SetMetaDataAnnotation(&m.ObjectMeta, deleteguard.ResetOnDeleteAnnotation, deleteguard.AllowDeleteValue)

	if wm := req.Msg.GetWipeMode(); wm != "" {
		metav1.SetMetaDataAnnotation(&m.ObjectMeta, deleteguard.ResetWipeModeAnnotation, wm)
	}

	if !req.Msg.GetReboot() {
		metav1.SetMetaDataAnnotation(&m.ObjectMeta, deleteguard.ResetPowerOffAnnotation, deleteguard.AllowDeleteValue)
	}

	// A worker has no finalizer of its own; with it the controller drains the node before the
	// machine goes (a control plane already has it).
	controllerutil.AddFinalizer(m, ichorv1alpha1.TalosMachineRemovalFinalizer)

	if err := s.Update(ctx, m, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("allowing the removal of", namespacedName(ref), err)
	}

	if err := s.Delete(ctx, m, client.Preconditions{UID: &m.UID}); err != nil {
		return nil, mapOperationWriteError("removing node", namespacedName(ref), err)
	}

	return connect.NewResponse(&pb.RemoveMachineResponse{Audit: s.opAudit(ctx, "remove", objectRef(m.Namespace, m.Name))}), nil
}
