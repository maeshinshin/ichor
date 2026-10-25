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
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// objectRef builds the wire ObjectRef every message that names a
// TalosCluster or TalosMachine carries. The browser never sees more of the
// Kubernetes object than this (common.proto's own doc comment).
func objectRef(namespace, name string) *pb.ObjectRef {
	return &pb.ObjectRef{Namespace: namespace, Name: name}
}

// namespacedName turns a client-supplied ObjectRef back into the key a
// client.Client Get/List needs. Every ClusterService/MachineService/
// NodeService method that takes an ObjectRef request field starts here.
func namespacedName(ref *pb.ObjectRef) types.NamespacedName {
	return types.NamespacedName{Namespace: ref.GetNamespace(), Name: ref.GetName()}
}

// conditions converts a CR's kstatus-compatible conditions to the wire
// Condition slice common.proto defines, trimmed to what a screen displays
// (common.proto's own doc comment); nil in, nil out, so an empty
// Status.Conditions never turns into an empty-but-allocated slice on the
// wire.
func conditions(cs []metav1.Condition) []*pb.Condition {
	if cs == nil {
		return nil
	}

	out := make([]*pb.Condition, len(cs))
	for i, c := range cs {
		out[i] = &pb.Condition{
			Type:               c.Type,
			Status:             string(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: timestamppb.New(c.LastTransitionTime.Time),
		}
	}

	return out
}

// machineRoleToSpec converts the wire MachineRole enum to the CRD's own
// spelling (TalosMachineSpec.spec.role: "controlplane"/"worker"), the
// mapping ApproveMachine applies to the request and specRoleToMachineRole
// applies in reverse for every read.
func machineRoleToSpec(r pb.MachineRole) string {
	switch r {
	case pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE:
		return "controlplane"
	case pb.MachineRole_MACHINE_ROLE_WORKER:
		return "worker"
	default:
		return ""
	}
}

// specRoleToMachineRole is machineRoleToSpec's inverse, for every read of a
// TalosMachine.spec.role already stored (ListMachines, GetMachine,
// ApproveMachine's own response).
func specRoleToMachineRole(role string) pb.MachineRole {
	switch role {
	case "controlplane":
		return pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE
	case "worker":
		return pb.MachineRole_MACHINE_ROLE_WORKER
	default:
		return pb.MachineRole_MACHINE_ROLE_UNSPECIFIED
	}
}

// specApprovalStateToPB converts TalosMachineSpec.spec.approvalState to the
// wire enum. The CRD's own default ("pending") means an empty
// string here is read the same way the CRD itself reads it, not as
// UNSPECIFIED -- only a value outside the CRD's enum (impossible through
// the API server's own validation, but not through this function's type)
// falls through to UNSPECIFIED.
func specApprovalStateToPB(state string) pb.MachineApprovalState {
	switch state {
	case ichorv1alpha1.MachineApprovalStatePending, "":
		return pb.MachineApprovalState_MACHINE_APPROVAL_STATE_PENDING
	case ichorv1alpha1.MachineApprovalStateApproved:
		return pb.MachineApprovalState_MACHINE_APPROVAL_STATE_APPROVED
	case ichorv1alpha1.MachineApprovalStateRejected:
		return pb.MachineApprovalState_MACHINE_APPROVAL_STATE_REJECTED
	default:
		return pb.MachineApprovalState_MACHINE_APPROVAL_STATE_UNSPECIFIED
	}
}

// machineToPB converts a TalosMachine to the wire Machine message
// MachineService's four methods all return (machine.proto's doc comment:
// "One Machine message serves both" the list and the detail view).
func machineToPB(m *ichorv1alpha1.TalosMachine) *pb.Machine {
	return &pb.Machine{
		Ref:     objectRef(m.Namespace, m.Name),
		Cluster: objectRef(m.Namespace, m.Spec.ClusterRef.Name),
		Identity: &pb.MachineIdentity{
			Uuid:         m.Spec.Identity.UUID,
			Serial:       m.Spec.Identity.Serial,
			MacAddresses: m.Spec.Identity.MACAddresses,
		},
		Endpoint:      m.Spec.Endpoint,
		Role:          specRoleToMachineRole(m.Spec.Role),
		NodeLabels:    m.Spec.NodeLabels,
		Hostname:      m.Spec.Hostname,
		ApprovalState: specApprovalStateToPB(m.Spec.ApprovalState),
		Conditions:    conditions(m.Status.Conditions),
		Stage:         "", // Talos's own MachineStatus stage: not read by this phase (GetNode's job)
	}
}

// clusterToPB converts a TalosCluster to the wire Cluster message
// ClusterService's two methods return.
func clusterToPB(c *ichorv1alpha1.TalosCluster) *pb.Cluster {
	schedulable := c.Spec.ControlPlane.Schedulable == "true"

	return &pb.Cluster{
		Ref:                     objectRef(c.Namespace, c.Name),
		TalosVersion:            c.Spec.TalosVersion,
		KubernetesVersion:       c.Spec.KubernetesVersion,
		Endpoint:                c.Spec.Endpoint.Address,
		ControlPlaneCount:       c.Spec.ControlPlane.Count,
		ControlPlaneSchedulable: schedulable,
		Conditions:              conditions(c.Status.Conditions),
		// Rollout and Handovers stay unset: TalosClusterStatus carries
		// neither a rollout nor a hand-over state yet (roadmap phases 6 and
		// 4/8's controller-side work).
	}
}
