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
	"fmt"
	"strings"

	"connectrpc.com/connect"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/secureboot"
)

// actor is the AuditRecord actor when the caller has no authenticated identity
// (OIDC disabled); otherwise Identity.User is recorded.
const actor = "ichor-ui"

// strictFieldValidation is passed to every write this package makes
// (architecture.md, "Typed configuration": no admission webhook rejects an
// unknown field; instead "Ichor's own clients ask for strict field
// validation ... so the CLI and the UI backend report the field loudly").
// Without it, the API server's default behavior is to silently prune an
// unknown or duplicate field -- the object is still persisted, just
// without that field, logged only as a server-side warning the caller
// never sees. Strict turns that into a rejected write instead.
var strictFieldValidation = client.FieldValidation(metav1.FieldValidationStrict)

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

// getMachine reads ref's TalosMachine by its exact name, or, when no machine has that name, by a
// name prefix that only one machine has: `machine list` shows a UUID name shortened to eight
// characters, and that short form has to name the machine back.
func (s *Server) getMachine(ctx context.Context, ref *pb.ObjectRef) (*ichorv1alpha1.TalosMachine, error) {
	m, err := s.getMachineExact(ctx, ref)
	if connect.CodeOf(err) != connect.CodeNotFound {
		return m, err
	}

	return s.machineByPrefix(ctx, ref, err)
}

func (s *Server) getMachineExact(ctx context.Context, ref *pb.ObjectRef) (*ichorv1alpha1.TalosMachine, error) {
	var m ichorv1alpha1.TalosMachine

	key := namespacedName(ref)
	if err := s.Get(ctx, key, &m); err != nil {
		return nil, mapGetError(fmt.Sprintf("node %q", key), err)
	}

	return &m, nil
}

// resolveMachine is getMachine that also takes the machine's hostname or its Kubernetes Node name,
// for a caller who knows the node by that name and not by the TalosMachine's UUID. The exact name
// wins; only a name no machine has is looked up as a hostname or Node name, then as a name prefix,
// and more than one match is an error rather than a guess.
func (s *Server) resolveMachine(ctx context.Context, ref *pb.ObjectRef) (*ichorv1alpha1.TalosMachine, error) {
	m, err := s.getMachineExact(ctx, ref)
	if connect.CodeOf(err) != connect.CodeNotFound {
		return m, err
	}

	var list ichorv1alpha1.TalosMachineList
	if lerr := s.List(ctx, &list, client.InNamespace(ref.GetNamespace())); lerr != nil {
		return nil, err // the not found is the better answer
	}

	var found []*ichorv1alpha1.TalosMachine

	for i := range list.Items {
		if list.Items[i].Spec.Hostname == ref.GetName() {
			found = append(found, &list.Items[i])
		}
	}

	if len(found) == 0 {
		for i := range list.Items {
			if node, nerr := s.findKubernetesNode(ctx, &list.Items[i]); nerr == nil && node != nil && node.Name == ref.GetName() {
				found = append(found, &list.Items[i])
			}
		}
	}

	if len(found) == 0 {
		return s.machineByPrefix(ctx, ref, err)
	}

	return oneMachine(ref.GetName(), found)
}

// machineByPrefix is the TalosMachine in ref's namespace whose name starts with ref's name; notFound
// is the answer when there is none.
func (s *Server) machineByPrefix(ctx context.Context, ref *pb.ObjectRef, notFound error) (*ichorv1alpha1.TalosMachine, error) {
	if ref.GetName() == "" {
		return nil, notFound
	}

	var list ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &list, client.InNamespace(ref.GetNamespace())); err != nil {
		return nil, notFound // the not found is the better answer
	}

	var found []*ichorv1alpha1.TalosMachine

	for i := range list.Items {
		if strings.HasPrefix(list.Items[i].Name, ref.GetName()) {
			found = append(found, &list.Items[i])
		}
	}

	if len(found) == 0 {
		return nil, notFound
	}

	return oneMachine(ref.GetName(), found)
}

// oneMachine is the only machine in found, or an error naming them all.
func oneMachine(name string, found []*ichorv1alpha1.TalosMachine) (*ichorv1alpha1.TalosMachine, error) {
	if len(found) == 1 {
		return found[0], nil
	}

	names := make([]string, len(found))
	for i, f := range found {
		names[i] = f.Name
	}

	return nil, invalid("%q matches more than one node (%s): use the node's own name", name, strings.Join(names, ", "))
}

// ApproveMachine is how a person decides a discovered machine joins the
// cluster (architecture.md "Nodes"): once it returns, the controller starts
// rendering and applying configuration to the node. Four things this
// method refuses before it ever writes anything:
//
//  1. A weak identity (missing UUID, serial or every permanent MAC) is
//     never approved's own reason discovery never
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
//  4. (RejectMachine, not this method) a rejected machine is recorded as
//     rejected, never deleted, so it can never be rediscovered as a fresh
//     pending machine. Approving a rejected machine is allowed -- that is
//     how a rejection is reversed -- so this method's own refusal list
//     above has nothing to say about a REJECTED state; only APPROVED
//     blocks it.
func (s *Server) ApproveMachine(
	ctx context.Context, req *connect.Request[pb.ApproveMachineRequest],
) (*connect.Response[pb.ApproveMachineResponse], error) {
	ref := req.Msg.GetMachine()

	m, err := s.getMachine(ctx, ref)
	if err != nil {
		return nil, err
	}

	if m.Spec.IsApproved() {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("node %q is already approved; approving it again would risk an unintended "+
				"reconfiguration, which this API refuses -- edit its configuration instead", namespacedName(ref)))
	}

	if !m.Spec.Identity.IsStrongIdentity() {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("node %q has a weak identity (missing UUID, serial number or permanent MAC "+
				"address) and cannot be approved", namespacedName(ref)))
	}

	role := req.Msg.GetRole()
	if role == pb.MachineRole_MACHINE_ROLE_UNSPECIFIED {
		return nil, invalid("role is required to approve a node")
	}

	// Checked here so the caller gets the reason, not a bare admission error.
	if h := req.Msg.GetHostname(); h != "" {
		if errs := validation.IsDNS1123Subdomain(h); len(errs) > 0 {
			return nil, invalid("hostname %q is not a valid RFC 1123 name: %s", h, strings.Join(errs, "; "))
		}
	}

	install, err := installFromPB(req.Msg.GetInstall())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	install, err = s.withInstallerImage(ctx, m, install)
	if err != nil {
		return nil, err
	}

	network, err := NetworkFromPB(req.Msg.GetNetwork(), m.Spec.Identity, m.Status.DiscoveredLinks)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	disks, err := disksFromPB(req.Msg.GetDisks())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.requireVIPLink(ctx, m, role, network); err != nil {
		return nil, err
	}

	// A rejected machine can be approved: this is how a rejection is undone. The only state ApproveMachine refuses to
	// touch is
	// already-approved, checked above.
	now := s.now()

	m.Spec.Role = machineRoleToSpec(role)
	m.Spec.NodeLabels = req.Msg.GetNodeLabels()
	m.Spec.Hostname = req.Msg.GetHostname()
	m.Spec.Install = install
	m.Spec.Network = network
	m.Spec.Disks = disks
	m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved
	m.Spec.ApprovalDecision = &ichorv1alpha1.TalosMachineApprovalDecision{
		Actor: s.actorName(ctx), At: metav1.NewTime(now),
	}

	if err := s.Update(ctx, m, strictFieldValidation); err != nil {
		if hint := approveRightHint(ctx, err); hint != "" {
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("approving node %q: %w; %s", namespacedName(ref), err, hint))
		}

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("approving node %q: %w", namespacedName(ref), err))
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(ctx), At: timestamppb.New(now), Action: "approve", Target: objectRef(m.Namespace, m.Name),
	}

	// The webhook raises the same warnings on the write; the response carries them too, since the
	// client does not see admission warnings.
	return connect.NewResponse(&pb.ApproveMachineResponse{
		Machine: machineToPB(m), Audit: audit, Warnings: m.DiscoveryWarnings(),
	}), nil
}

// requireVIPLink refuses to approve a control plane without a primary link into a cluster whose VIP Ichor
// holds. The VIP is announced on the link spec.network gives a machine (pkg/render), so a control plane
// without one never joins the election and the first node keeps the address for good: a VIP that cannot fail
// over, which looks like high availability and is not. The VIP counts as Ichor's when the endpoint type is vip
// and another control plane already has a spec.network; a vip adopted from a hardware load balancer, or one the
// cluster runs itself, has none, so approving into it is left alone.
func (s *Server) requireVIPLink(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, role pb.MachineRole, network *ichorv1alpha1.TalosMachineNetwork,
) error {
	if role != pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE || network != nil {
		return nil
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := s.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}, &cluster); err != nil ||
		cluster.Spec.Endpoint.Type != "vip" {
		return nil
	}

	var machines ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &machines, client.InNamespace(m.Namespace)); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	holds := false

	for i := range machines.Items {
		o := &machines.Items[i]
		holds = holds || (o.Name != m.Name && o.Spec.ClusterRef.Name == cluster.Name &&
			o.Spec.Role == opRoleControlPlane && o.Spec.IsApproved() && o.Spec.Network != nil)
	}

	if !holds {
		return nil
	}

	msg := fmt.Sprintf("node %q is a control plane of cluster %q, whose endpoint %s is a VIP Ichor holds on each control plane's "+
		"primary link; without one this node never joins the VIP and the first node keeps it for good. Give its primary link's "+
		"permanent MAC (`ichor approve node --nic MAC`, the network of the approval form)",
		m.Name, cluster.Name, cluster.Spec.Endpoint.Address)

	if len(m.Status.DiscoveredLinks) == 0 {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s", msg))
	}

	var links []string

	for _, l := range m.Status.DiscoveredLinks {
		state := "down"
		if l.LinkUp {
			state = "up"
		}

		links = append(links, fmt.Sprintf("%s (%s, %s)", l.PermanentMAC, l.Name, state))
	}

	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s; its links: %s", msg, strings.Join(links, ", ")))
}

// approveRightHint says what to do when the TalosMachine webhook refused an approval for want of the
// approve right; it is empty for any other error. Without a sign-in the writer is ichor-ui's
// ServiceAccount, which does not hold the right on purpose: an open ichor-ui must not hand a cluster's
// configuration to a machine on anyone's say-so.
func approveRightHint(ctx context.Context, err error) string {
	verb := fmt.Sprintf("%q verb on talosmachines/%s", ichorv1alpha1.MachineApproveVerb, ichorv1alpha1.MachineApprovalSubresource)
	if !strings.Contains(err.Error(), verb) {
		return ""
	}

	if _, signedIn := IdentityFrom(ctx); signedIn {
		return "bind your user to the ichor-operator or ichor-admin role, or to a role that holds it"
	}

	return "ichor-ui is not asking who you are, so it acts as its own ServiceAccount, which does not hold this right by design: " +
		"run the command with --local (it uses your kubeconfig, whose user needs the ichor-operator or ichor-admin role), " +
		"or turn on Ichor users or OIDC and sign in"
}

// withInstallerImage names the installer image of install when the caller left it empty: the installer of the
// cluster's schematic and Talos version, its SecureBoot variant when the cluster's mode is not Off. Talos refuses
// an empty image unless the node booted an Image Factory asset, so every install target this server writes names
// one. An image the caller named is kept. A cluster that cannot be read is an error, not a silent empty image.
func (s *Server) withInstallerImage(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, install *ichorv1alpha1.TalosMachineInstall,
) (*ichorv1alpha1.TalosMachineInstall, error) {
	if install == nil || install.Image != "" {
		return install, nil
	}

	key := types.NamespacedName{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}

	c, err := s.loadCluster(ctx, key)
	if err != nil {
		// loadCluster has already chosen the code (NotFound, PermissionDenied, or Internal for a failed read).
		return nil, connect.NewError(connect.CodeOf(err), fmt.Errorf(
			"node %q names no install image, so Ichor takes the installer from its cluster %q, which could not be read (%v); "+
				"make that cluster readable, or name the install image with --install-image", m.Name, key, err))
	}

	install.Image = secureboot.ClusterInstallerImage(c, secureboot.Mode(c.Spec.Security) != ichorv1alpha1.SecureBootOff)

	return install, nil
}

// installFromPB converts the wire install target to the spec field; nil means "none". Exactly
// one disk identifier must be set, the same rule the CRD enforces, so the caller learns it here.
func installFromPB(in *pb.MachineInstall) (*ichorv1alpha1.TalosMachineInstall, error) {
	if in == nil {
		return nil, nil
	}

	disk, err := diskFromPB("install.disk", in.GetDisk())
	if err != nil {
		return nil, err
	}

	return &ichorv1alpha1.TalosMachineInstall{Disk: disk, Image: in.GetImage()}, nil
}

func installToPB(in *ichorv1alpha1.TalosMachineInstall) *pb.MachineInstall {
	if in == nil {
		return nil
	}

	return &pb.MachineInstall{Disk: diskToPB(in.Disk), Image: in.Image}
}

// RejectMachine records that a pending machine was turned away by setting
// ApprovalState to rejected -- distinct from pending, so a rejected machine
// stops showing up as a fresh approval request. It never
// deletes the TalosMachine: deleting it would let the next discovery pass
// recreate it as fresh and pending again,
// which would make the rejection meaningless the moment the node's own
// discovery loop runs again. Rejecting an already-approved machine is
// refused -- "reject" has no meaning once a machine is under management;
// removing one is a different, later operation (architecture.md "Nodes",
// the removal sequence). Rejecting an already-rejected machine is allowed
// and simply records the new reason.
func (s *Server) RejectMachine(
	ctx context.Context, req *connect.Request[pb.RejectMachineRequest],
) (*connect.Response[pb.RejectMachineResponse], error) {
	ref := req.Msg.GetMachine()

	m, err := s.getMachine(ctx, ref)
	if err != nil {
		return nil, err
	}

	if m.Spec.IsApproved() {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("node %q is already approved; reject only applies to a pending node", namespacedName(ref)))
	}

	now := s.now()

	m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateRejected
	m.Spec.ApprovalDecision = &ichorv1alpha1.TalosMachineApprovalDecision{
		Actor: s.actorName(ctx), At: metav1.NewTime(now),
	}

	if err := s.Update(ctx, m, strictFieldValidation); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("rejecting node %q: %w", namespacedName(ref), err))
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(ctx), At: timestamppb.New(now), Action: "reject",
		Target: objectRef(m.Namespace, m.Name),
	}

	return connect.NewResponse(&pb.RejectMachineResponse{Audit: audit}), nil
}
