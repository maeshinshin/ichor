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

	"connectrpc.com/connect"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// actor is recorded on every AuditRecord this package returns. There is no
// per-caller identity yet -- OIDC login and Kubernetes impersonation are
// roadmap phase 9 ("CLI and UI": "OIDC login with Kubernetes impersonation
// comes later so that RBAC and audit apply per user") -- so "who" is
// honestly the backend's own identity, not a person, until that lands.
const actor = "ichor-ui"

// ListMachines returns every TalosMachine the backend can list, pending
// machines included unconditionally (machine.proto's own doc comment),
// optionally narrowed to one cluster.
func (s *Server) ListMachines(
	ctx context.Context, req *connect.Request[pb.ListMachinesRequest],
) (*connect.Response[pb.ListMachinesResponse], error) {
	var list ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &list); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetCluster()

	machines := make([]*pb.Machine, 0, len(list.Items))

	for i := range list.Items {
		m := &list.Items[i]
		if filter != nil && (m.Namespace != filter.GetNamespace() || m.Spec.ClusterRef.Name != filter.GetName()) {
			continue
		}

		machines = append(machines, machineToPB(m))
	}

	return connect.NewResponse(&pb.ListMachinesResponse{Machines: machines}), nil
}

// GetMachine returns one TalosMachine.
func (s *Server) GetMachine(
	ctx context.Context, req *connect.Request[pb.GetMachineRequest],
) (*connect.Response[pb.GetMachineResponse], error) {
	m, err := s.getMachine(ctx, req.Msg.GetMachine())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.GetMachineResponse{Machine: machineToPB(m)}), nil
}

func (s *Server) getMachine(ctx context.Context, ref *pb.ObjectRef) (*ichorv1alpha1.TalosMachine, error) {
	var m ichorv1alpha1.TalosMachine

	key := namespacedName(ref)
	if err := s.Get(ctx, key, &m); err != nil {
		return nil, mapGetError(fmt.Sprintf("machine %q", key), err)
	}

	return &m, nil
}

// ApproveMachine is how a person decides a discovered machine joins the
// cluster (architecture.md "Nodes"): once it returns, the controller starts
// rendering and applying configuration to the node. Four things this
// method refuses before it ever writes anything (docs/adr/0018):
//
//  1. A weak identity (missing UUID, serial or every permanent MAC) is
//     never approved -- ADR 0011's own reason discovery never
//     auto-registers one applies just as much to a human clicking
//     "approve" on it.
//  2. role is required: the wire enum's zero value, MACHINE_ROLE_UNSPECIFIED,
//     is refused rather than silently stored as an empty
//     TalosMachineSpec.spec.role, which pkg/render's machine.ParseType would
//     otherwise be left to reject (or not) on the controller's own
//     reconcile, far from the approval action that caused it.
//  3. An already-approved machine refuses a second approval outright
//     (CodeFailedPrecondition, no mutation at all) rather than silently
//     re-applying whatever role/labels/hostname this call carries --
//     "approving an already-approved machine must not cause unintended
//     reconfiguration" is satisfied by never touching the object a second
//     time, not by trying to detect which edits are "intended."
//     Re-configuring an approved machine is config-editing, a different,
//     later unit (roadmap phase 6).
//  4. (RejectMachine, not this method) a rejected machine is never deleted,
//     so it can never be rediscovered as a fresh pending machine.
func (s *Server) ApproveMachine(
	ctx context.Context, req *connect.Request[pb.ApproveMachineRequest],
) (*connect.Response[pb.ApproveMachineResponse], error) {
	ref := req.Msg.GetMachine()

	m, err := s.getMachine(ctx, ref)
	if err != nil {
		return nil, err
	}

	if m.Spec.Approved {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("machine %q is already approved; approving it again would risk an unintended "+
				"reconfiguration, which this API refuses -- edit its configuration instead", namespacedName(ref)))
	}

	if !strongIdentity(m.Spec.Identity) {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("machine %q has a weak identity (missing UUID, serial number or permanent MAC "+
				"address) and cannot be approved (docs/adr/0011)", namespacedName(ref)))
	}

	role := req.Msg.GetRole()
	if role == pb.MachineRole_MACHINE_ROLE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("role is required to approve a machine"))
	}

	m.Spec.Role = machineRoleToSpec(role)
	m.Spec.NodeLabels = req.Msg.GetNodeLabels()
	m.Spec.Hostname = req.Msg.GetHostname()
	m.Spec.Approved = true

	if err := s.Update(ctx, m); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("approving machine %q: %w", namespacedName(ref), err))
	}

	audit := &pb.AuditRecord{
		Actor: actor, At: timestamppb.New(s.now()), Action: "approve", Target: objectRef(m.Namespace, m.Name),
	}

	return connect.NewResponse(&pb.ApproveMachineResponse{Machine: machineToPB(m), Audit: audit}), nil
}

// RejectMachine records that a pending machine was turned away. It never
// mutates or deletes the TalosMachine: deleting it would let the next
// discovery pass recreate it as fresh and pending again (ADR 0011's "same
// machine returning" branch only short-circuits for an object that still
// exists), which would make the rejection meaningless the moment the node's
// own discovery loop runs again. Rejecting an already-approved machine is
// refused -- "reject" has no meaning once a machine is under management;
// removing one is a different, later operation (architecture.md "Nodes",
// the removal sequence).
func (s *Server) RejectMachine(
	ctx context.Context, req *connect.Request[pb.RejectMachineRequest],
) (*connect.Response[pb.RejectMachineResponse], error) {
	ref := req.Msg.GetMachine()

	m, err := s.getMachine(ctx, ref)
	if err != nil {
		return nil, err
	}

	if m.Spec.Approved {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("machine %q is already approved; reject only applies to a pending machine", namespacedName(ref)))
	}

	audit := &pb.AuditRecord{
		Actor: actor, At: timestamppb.New(s.now()), Action: "reject",
		Target: objectRef(m.Namespace, m.Name), Reason: req.Msg.GetReason(),
	}

	return connect.NewResponse(&pb.RejectMachineResponse{Audit: audit}), nil
}
