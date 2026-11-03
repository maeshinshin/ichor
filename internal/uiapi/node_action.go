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

func nodeActionToSpec(a pb.NodeAction) (string, error) {
	switch a {
	case pb.NodeAction_NODE_ACTION_REBOOT:
		return ichorv1alpha1.NodeActionReboot, nil
	case pb.NodeAction_NODE_ACTION_SHUTDOWN:
		return ichorv1alpha1.NodeActionShutdown, nil
	case pb.NodeAction_NODE_ACTION_RESTART_SERVICE:
		return ichorv1alpha1.NodeActionRestartService, nil
	case pb.NodeAction_NODE_ACTION_ETCD_DEFRAG:
		return ichorv1alpha1.NodeActionEtcdDefrag, nil
	case pb.NodeAction_NODE_ACTION_ETCD_FORFEIT_LEADERSHIP:
		return ichorv1alpha1.NodeActionEtcdForfeitLeadership, nil
	case pb.NodeAction_NODE_ACTION_RESET:
		return ichorv1alpha1.NodeActionReset, nil
	case pb.NodeAction_NODE_ACTION_REMOVE:
		return nodeActionRemove, nil
	case pb.NodeAction_NODE_ACTION_UNSPECIFIED:
	}

	return "", connect.NewError(connect.CodeInvalidArgument, errors.New("node action is required"))
}

// nodeActionRemove is the planning-only action; it never reaches a TalosOperation.
const nodeActionRemove = "remove"

func nodeActionToPB(a string) pb.NodeAction {
	switch a {
	case ichorv1alpha1.NodeActionReboot:
		return pb.NodeAction_NODE_ACTION_REBOOT
	case ichorv1alpha1.NodeActionShutdown:
		return pb.NodeAction_NODE_ACTION_SHUTDOWN
	case ichorv1alpha1.NodeActionRestartService:
		return pb.NodeAction_NODE_ACTION_RESTART_SERVICE
	case ichorv1alpha1.NodeActionEtcdDefrag:
		return pb.NodeAction_NODE_ACTION_ETCD_DEFRAG
	case ichorv1alpha1.NodeActionEtcdForfeitLeadership:
		return pb.NodeAction_NODE_ACTION_ETCD_FORFEIT_LEADERSHIP
	case ichorv1alpha1.NodeActionReset:
		return pb.NodeAction_NODE_ACTION_RESET
	case nodeActionRemove:
		return pb.NodeAction_NODE_ACTION_REMOVE
	}

	return pb.NodeAction_NODE_ACTION_UNSPECIFIED
}

// nodePlan is what planNode learned, kept for CreateOperation.
type nodePlan struct {
	spec    ichorv1alpha1.TalosOperationNodeSpec
	action  string
	machine *ichorv1alpha1.TalosMachine
	bg      *pb.Override
	// asked is the name the caller used for the machine: its own, its hostname or its Node name.
	asked string
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
		return nil, invalid("a node operation needs the machine")
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

	np := &nodePlan{
		action: action, machine: m, bg: req.GetOverride(), asked: req.GetMachine().GetName(),
		spec: ichorv1alpha1.TalosOperationNodeSpec{
			MachineRef: corev1.LocalObjectReference{Name: m.Name}, Action: action,
			Service: req.GetService(), AllowLeader: req.GetAllowLeader(), Confirm: req.GetConfirm(),
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
		return 0, mapListError("machines", err)
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
	case ichorv1alpha1.NodeActionReset:
		resp.Steps = removalSteps(isCP, "Wipe the node's STATE and EPHEMERAL partitions and reboot it into maintenance mode.")
		resp.Warning = "This cannot be undone. The node leaves the cluster and its data is wiped. Ichor deletes this machine; the node shows up as a new pending machine."
		resp.ConfirmName, resp.OverrideRequired = name, true
	case nodeActionRemove:
		resp.Steps = append(removalSteps(isCP, ""), nodeStaysStep)
		resp.Warning = "The node leaves the cluster but is not wiped: it still holds the cluster's certificates until you reset it."
		resp.ConfirmName = name
	}
}

// nodeStaysStep is the last line of a remove plan: the kubelet keeps the cluster's
// credentials and would re-register a deleted Node, so the Node object is the operator's to delete
// once the machine is off.
const nodeStaysStep = "The Kubernetes Node stays while the machine runs (its kubelet keeps the cluster's credentials). " +
	"Power it off or reset it (`ichor node reset`, override), then delete the Node: `kubectl delete node <name>`."

func removalSteps(isCP bool, wipe string) []string {
	steps := []string{stepEvacuate, "Cordon the node and drain it."}
	if isCP {
		steps = append(steps, "Remove the node's etcd member.")
	}

	if wipe != "" {
		steps = append(steps, wipe)
	}

	return append(steps, "Delete the machine from Ichor.")
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
		return fmt.Sprintf("machine %q is not approved, so it is not a node of the cluster yet", m.Name), nil
	case !m.DeletionTimestamp.IsZero():
		return fmt.Sprintf("machine %q is being removed", m.Name), nil
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

	return s.unfinishedOperation(ctx, cluster)
}

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
	// it overrides the controller's last-worker guard. The reason is optional.
	override := np.bg != nil && (np.action == ichorv1alpha1.NodeActionReset ||
		np.action == ichorv1alpha1.NodeActionReboot || np.action == ichorv1alpha1.NodeActionShutdown)

	if np.action == ichorv1alpha1.NodeActionReset {
		if np.spec.Confirm != np.machine.Name && np.spec.Confirm != np.asked {
			return nil, invalid("type the machine name %q to confirm the reset", np.machine.Name)
		}

		// The user may have named the node by its hostname; the CRD compares confirm with machineRef.name.
		np.spec.Confirm = np.machine.Name

		if !override {
			return nil, invalid("a reset needs the override: it is checked against the override permission on the machine")
		}
	}

	var reason string

	if override {
		if err := s.authorizeOverride(ctx, np.machine); err != nil {
			return nil, err
		}

		reason = strings.TrimSpace(np.bg.GetReason())
		np.spec.Override, np.spec.OverrideReason = true, reason
	}

	cluster := p.cluster
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: p.kind + "-", Namespace: cluster.Namespace, Annotations: s.startedByAnnotation(ctx),
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: p.kind, Node: &np.spec,
		},
	}

	if err := s.createOperation(ctx, cluster, op); err != nil {
		return nil, err
	}

	audit := s.opAudit(ctx, "create", objectRef(op.Namespace, op.Name))

	if override {
		why := overrideReason(reason)
		audit.Action += "-override"
		audit.Reason = why
		msg := fmt.Sprintf("%s of %s requested by %s under override: %s", np.action, np.machine.Name, audit.Actor, why)

		if err := s.emitWarningEvent(ctx, np.machine, kindTalosMachine, "Override", msg); err != nil {
			audit.Reason += " (the Kubernetes Event could not be recorded)"
		}
	}

	return &pb.CreateOperationResponse{Operation: operationToPB(op), Audit: audit}, nil
}

// RemoveMachine deletes an approved machine, which starts the removal flow of the controller: drain, etcd member
// removal for a control plane, then the TalosMachine goes. The
// annotation that lets the delete through is set here, for this one delete.
func (s *Server) RemoveMachine(
	ctx context.Context, req *connect.Request[pb.RemoveMachineRequest],
) (*connect.Response[pb.RemoveMachineResponse], error) {
	ref := req.Msg.GetMachine()

	p, err := s.planNode(ctx, &pb.NodeActionRequest{Machine: ref, Action: pb.NodeAction_NODE_ACTION_REMOVE})
	if err != nil {
		return nil, err
	}

	if p.resp.GetBlocked() != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(p.resp.GetBlocked()))
	}

	m := p.node.machine
	if c := req.Msg.GetConfirm(); c != m.Name && c != p.node.asked {
		return nil, invalid("type the machine name %q to confirm the removal", m.Name)
	}

	if m.Annotations == nil {
		m.Annotations = map[string]string{}
	}

	m.Annotations[deleteguard.AllowDeleteAnnotation] = deleteguard.AllowDeleteValue

	// A worker has no finalizer of its own; with it the controller drains the node before the
	// machine goes (a control plane already has it).
	controllerutil.AddFinalizer(m, ichorv1alpha1.TalosMachineRemovalFinalizer)

	if err := s.Update(ctx, m, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("allowing the removal of", namespacedName(ref), err)
	}

	if err := s.Delete(ctx, m); err != nil {
		return nil, mapOperationWriteError("removing machine", namespacedName(ref), err)
	}

	return connect.NewResponse(&pb.RemoveMachineResponse{Audit: s.opAudit(ctx, "remove", objectRef(m.Namespace, m.Name))}), nil
}
