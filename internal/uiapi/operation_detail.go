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
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// The operation's own page: a sentence saying what it is, the spec's facts, who started it, and
// the Events recorded about it.

// eventSlack lets an Event recorded just after the operation finished (the completion Event is
// written after the status) still belong to it.
const eventSlack = time.Minute

// caRotationTargets are the CAs a rotation replaces: what its status recorded when it started, else
// what the spec asks for, else the default pair.
func caRotationTargets(op *ichorv1alpha1.TalosOperation) []string {
	if rs := op.Status.CARotation; rs != nil && len(rs.Certificates) > 0 {
		names := make([]string, 0, len(rs.Certificates))
		for _, c := range rs.Certificates {
			names = append(names, c.Name)
		}

		return names
	}

	if s := op.Spec.CARotation; s != nil && len(s.Targets) > 0 {
		return s.Targets
	}

	return ichorv1alpha1.DefaultCARotationTargets()
}

// newOperation is the TalosOperation of spec (its kind named, its cluster filled in) that cluster's caller starts.
func (s *Server) newOperation(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, spec ichorv1alpha1.TalosOperationSpec) *ichorv1alpha1.TalosOperation {
	spec.ClusterRef = corev1.LocalObjectReference{Name: cluster.Name}

	return &ichorv1alpha1.TalosOperation{
		GenerateName: spec.Kind + "-", Namespace: cluster.Namespace, Spec: spec,
		Annotations: map[string]string{ichorv1alpha1.OperationStartedByAnnotation: s.actorName(ctx)},
	}
}

func operationMachineToPB(op *ichorv1alpha1.TalosOperation, m ichorv1alpha1.TalosOperationMachineStatus) *pb.OperationMachine {
	out := &pb.OperationMachine{Name: m.Name, Phase: m.Phase}

	if m.StartedAt != nil {
		out.StartedAt = timestamppb.New(m.StartedAt.Time)
	}

	if m.FinishedAt != nil {
		out.FinishedAt = timestamppb.New(m.FinishedAt.Time)
	}

	if m.Phase == ichorv1alpha1.TalosOperationMachinePhaseInProgress {
		switch {
		case op.Status.Kubernetes != nil:
			out.Step = op.Status.Kubernetes.Stage
		case op.Status.CARotation != nil:
			out.Step = op.Status.CARotation.Stage
		case op.Status.KeyRotation != nil:
			out.Step = op.Status.KeyRotation.Stage
		case op.Status.EtcdRestore != nil:
			out.Step = op.Status.EtcdRestore.Step
		}

		if n := op.Status.Node; n != nil && op.Spec.Node != nil && op.Spec.Node.MachineRef.Name == m.Name {
			out.Message = n.Message
		}
	}

	return out
}

// enrichOperation adds what only the cluster (target versions) or the installation knows. A
// cluster or installation the caller cannot read only leaves the summary without versions.
func (s *Server) enrichOperation(ctx context.Context, out *pb.Operation, op *ichorv1alpha1.TalosOperation) {
	var cluster ichorv1alpha1.TalosCluster
	if err := s.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.ClusterRef.Name}, &cluster); err != nil {
		return
	}

	s.nameMachines(ctx, out, op.Namespace)

	var install *ichorv1alpha1.IchorInstallation

	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindIchor {
		install = &ichorv1alpha1.IchorInstallation{}
		if err := s.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, install); err != nil {
			install = nil
		}
	}

	out.Details, out.Summary = nil, ""
	describeOperation(out, op, &cluster, install)
}

// nameMachines fills each machine's display_name from its TalosMachine: hostname, else node name.
// A machine the caller cannot list keeps just its name.
func (s *Server) nameMachines(ctx context.Context, out *pb.Operation, namespace string) {
	if len(out.GetMachines()) == 0 {
		return
	}

	var list ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return
	}

	names := make(map[string]string, len(list.Items))
	for i := range list.Items {
		names[list.Items[i].Name] = cmp.Or(list.Items[i].Spec.Hostname, list.Items[i].Status.NodeName)
	}

	for _, m := range out.GetMachines() {
		m.DisplayName = names[m.GetName()]
	}
}

// fromTo is " from → to" when the starting version was recorded, else " to target".
func fromTo(from, target string) string {
	if from != "" && from != target {
		return " " + from + " → " + target
	}

	return " to " + target
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}

	return id
}

// describeOperation fills summary, details, started_by and finished_at. cluster and install are
// optional: without them the summary leaves the versions out rather than guessing.
//
//nolint:gocyclo // one switch over the kinds, each a few lines
func describeOperation(out *pb.Operation, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, install *ichorv1alpha1.IchorInstallation) {
	out.StartedBy = op.Annotations[ichorv1alpha1.OperationStartedByAnnotation]
	if op.Status.FinishedAt != nil {
		out.FinishedAt = timestamppb.New(op.Status.FinishedAt.Time)
	}

	add := func(k, v string) {
		if v != "" {
			out.Details = append(out.Details, &pb.OperationDetail{Key: k, Value: v})
		}
	}

	add("Cluster", op.Spec.ClusterRef.Name)
	add("Kind", op.Spec.Kind)

	switch op.Spec.Kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		out.Summary = "Upgrade Talos"

		if cluster != nil {
			out.Summary += fromTo(op.Annotations[ichorv1alpha1.OperationFromVersionAnnotation], cluster.Spec.TalosVersion)

			add("Target Talos version", cluster.Spec.TalosVersion)

			if cluster.Spec.Schematic != nil {
				out.Summary += " (schematic " + shortID(cluster.Spec.Schematic.ID) + ")"

				add("Schematic", cluster.Spec.Schematic.ID)
			}
		}
	case ichorv1alpha1.TalosOperationKindKubernetes:
		out.Summary = "Upgrade Kubernetes"

		if cluster != nil {
			out.Summary += fromTo(op.Annotations[ichorv1alpha1.OperationFromVersionAnnotation], cluster.Spec.KubernetesVersion)

			add("Target Kubernetes version", cluster.Spec.KubernetesVersion)
		}
	case ichorv1alpha1.TalosOperationKindIchor:
		out.Summary = "Upgrade Ichor"

		if install != nil {
			add("Target Ichor version", install.Spec.Version)

			// The stamped from-version outlives the upgrade; installedVersion only helps before it finishes.
			// The row says what runs now, so it is left out once the operation Completed.
			from := op.Annotations[ichorv1alpha1.OperationFromVersionAnnotation]
			if from == "" {
				from = install.Status.InstalledVersion
			}

			out.Summary += fromTo(from, install.Spec.Version)

			if from != "" && from != install.Spec.Version && op.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted {
				add("Installed Ichor version", from)
			}
		}
	case ichorv1alpha1.TalosOperationKindCNI:
		out.Summary = "Switch kube-proxy mode"

		if cluster != nil && cluster.Spec.CNI.KubeProxy != "" {
			out.Summary += " to " + cluster.Spec.CNI.KubeProxy

			add("kube-proxy mode", cluster.Spec.CNI.KubeProxy)
		}
	case ichorv1alpha1.TalosOperationKindShutdown:
		out.Summary = "Shut down the cluster"
	case ichorv1alpha1.TalosOperationKindCARotation:
		out.Summary = "Rotate the cluster CAs"

		add("CAs", strings.Join(caRotationTargets(op), ", "))

		if rs := op.Status.CARotation; rs != nil {
			add("control planes rebooted in this stage", strings.Join(rs.EtcdRestarted, ", "))
			add("control plane rebooting now", rs.EtcdRebooting)
		}
	case ichorv1alpha1.TalosOperationKindKeyRotation:
		describeKeyRotation(out, op, add)
	case ichorv1alpha1.TalosOperationKindConfig:
		out.Summary = "Apply configuration to the machines with pending changes"
		if machines := len(op.Status.Machines); machines > 0 {
			out.Summary = fmt.Sprintf("Apply configuration to %d machine%s", machines, plural(machines))
		}
	case ichorv1alpha1.TalosOperationKindNode:
		describeNodeOp(out, op, add)
	case ichorv1alpha1.TalosOperationKindEtcdRestore:
		out.Summary = "Restore etcd"

		if r := op.Spec.EtcdRestore; r != nil {
			out.Summary += " from " + r.Snapshot

			add("Backup", r.BackupRef.Name)
			add("Snapshot", r.Snapshot)
		}
	default:
		out.Summary = "Operation " + op.Name
	}

	if op.Spec.MaxUnavailable > 0 && slices.Contains([]string{
		ichorv1alpha1.TalosOperationKindTalos, ichorv1alpha1.TalosOperationKindKubernetes,
		ichorv1alpha1.TalosOperationKindCNI, ichorv1alpha1.TalosOperationKindConfig,
	}, op.Spec.Kind) {
		add("Max unavailable", fmt.Sprint(op.Spec.MaxUnavailable))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}

	return "s"
}

func describeNodeOp(out *pb.Operation, op *ichorv1alpha1.TalosOperation, add func(k, v string)) {
	n := op.Spec.Node
	if n == nil {
		out.Summary = "Node action"
		return
	}

	// The hostname when the machine list named it, else the machine's own name.
	m := n.MachineRef.Name

	for _, om := range out.GetMachines() {
		if om.GetName() == m && om.GetDisplayName() != "" {
			m = om.GetDisplayName()
		}
	}

	switch n.Action {
	case ichorv1alpha1.NodeActionReboot:
		out.Summary = "Reboot node " + m
	case ichorv1alpha1.NodeActionShutdown:
		out.Summary = "Shut down node " + m
	case ichorv1alpha1.NodeActionRestartService:
		out.Summary = fmt.Sprintf("Restart %s on node %s", n.Service, m)
	case ichorv1alpha1.NodeActionEtcdDefrag:
		out.Summary = "Defragment etcd on node " + m
	case ichorv1alpha1.NodeActionEtcdForfeitLeadership:
		out.Summary = "Forfeit etcd leadership on node " + m
	case ichorv1alpha1.NodeActionReset:
		out.Summary = "Reset node " + m
	default:
		out.Summary = fmt.Sprintf("%s node %s", n.Action, m)
	}

	add("Action", n.Action)
	add("Machine", m)
	add("Service", n.Service)
	add("Wipe mode", n.WipeMode)

	if n.AllowLeader {
		add("Allow leader", "true")
	}
}

// ListOperationEvents returns the Events whose involvedObject is the operation or one of its
// machines, recorded within the operation's lifetime, oldest first. Made as the caller.
func (s *Server) ListOperationEvents(
	ctx context.Context, req *connect.Request[pb.ListOperationEventsRequest],
) (*connect.Response[pb.ListOperationEventsResponse], error) {
	op, err := s.getOperation(ctx, req.Msg.GetOperation())
	if err != nil {
		return nil, err
	}

	var list corev1.EventList
	if err := s.List(ctx, &list, client.InNamespace(op.Namespace)); err != nil {
		return nil, mapListError("events", err)
	}

	machines := map[string]bool{}
	for _, m := range op.Status.Machines {
		machines[m.Name] = true
	}

	if op.Spec.Node != nil {
		machines[op.Spec.Node.MachineRef.Name] = true
	}

	from := op.CreationTimestamp.Add(-time.Second)
	until := s.now().Add(eventSlack)

	if op.Status.FinishedAt != nil {
		until = op.Status.FinishedAt.Add(eventSlack)
	}

	out := &pb.ListOperationEventsResponse{}

	for i := range list.Items {
		e := &list.Items[i]
		inv := e.InvolvedObject

		ours := (inv.Kind == "TalosOperation" && inv.Name == op.Name) || (inv.Kind == "TalosMachine" && machines[inv.Name])
		if !ours {
			continue
		}

		at := eventTime(e)
		if at.Before(from) || at.After(until) {
			continue
		}

		out.Events = append(out.Events, &pb.OperationEvent{
			Time: timestamppb.New(at), Type: e.Type, Reason: e.Reason, Message: e.Message,
			ObjectKind: inv.Kind, ObjectName: inv.Name,
		})
	}

	slices.SortStableFunc(out.Events, func(a, b *pb.OperationEvent) int { return a.GetTime().AsTime().Compare(b.GetTime().AsTime()) })

	return connect.NewResponse(out), nil
}

func eventTime(e *corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	case !e.FirstTimestamp.IsZero():
		return e.FirstTimestamp.Time
	}

	return e.CreationTimestamp.Time
}
