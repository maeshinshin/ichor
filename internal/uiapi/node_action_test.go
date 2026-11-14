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
	"strings"
	"testing"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// Node actions over a fake client: what the plan says, what is refused before
// anything is written, and what the TalosOperation looks like.

const nodeTestCP = "cp1"

func nodePlanReq(machine string, action pb.NodeAction) *connect.Request[pb.PlanOperationRequest] {
	return connect.NewRequest(&pb.PlanOperationRequest{
		Kind: pb.OperationKind_OPERATION_KIND_NODE,
		Node: &pb.NodeActionRequest{Machine: objectRef(opNS, machine), Action: action},
	})
}

func nodeCreateReq(n *pb.NodeActionRequest) *connect.Request[pb.CreateOperationRequest] {
	return connect.NewRequest(&pb.CreateOperationRequest{Kind: pb.OperationKind_OPERATION_KIND_NODE, Node: n})
}

func planNodeOK(t *testing.T, s *Server, req *connect.Request[pb.PlanOperationRequest]) *pb.PlanOperationResponse {
	t.Helper()

	res, err := s.PlanOperation(context.Background(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	return res.Msg
}

func TestNodePlanSaysWhatHappensInPlainEnglish(t *testing.T) {
	s, _ := opServer(t, opObjects()...)

	p := planNodeOK(t, s, nodePlanReq("w1", pb.NodeAction_NODE_ACTION_REBOOT))
	if p.GetBlocked() != "" || len(p.GetSteps()) < 4 || p.GetWarning() == "" || p.GetConfirmName() != "" {
		t.Fatalf("reboot plan = %+v", p)
	}

	if len(p.GetMachines()) != 1 || p.GetMachines()[0].GetName() != "w1" {
		t.Fatalf("a node plan lists exactly its machine: %+v", p.GetMachines())
	}

	// The only control plane: its reboot takes the API away, and the plan says so.
	cp := planNodeOK(t, s, nodePlanReq(nodeTestCP, pb.NodeAction_NODE_ACTION_REBOOT))
	if !cp.GetKubernetesApiUnavailable() || !strings.Contains(cp.GetWarning(), "only control plane") {
		t.Fatalf("sole control plane reboot must warn about the API: %+v", cp)
	}

	sd := planNodeOK(t, s, nodePlanReq("w1", pb.NodeAction_NODE_ACTION_SHUTDOWN))
	if sd.GetConfirmName() != "w1" || !strings.Contains(sd.GetWarning(), "stays off") {
		t.Fatalf("shutdown plan = %+v", sd)
	}

	reset := planNodeOK(t, s, nodePlanReq("w1", pb.NodeAction_NODE_ACTION_RESET))
	if reset.GetConfirmName() != "w1" || !reset.GetOverrideRequired() || !strings.Contains(reset.GetWarning(), "cannot be undone") {
		t.Fatalf("reset plan = %+v", reset)
	}

	rm := planNodeOK(t, s, nodePlanReq("w1", pb.NodeAction_NODE_ACTION_REMOVE))
	if rm.GetConfirmName() != "w1" || !rm.GetOverrideRequired() || rm.GetBlocked() != "" {
		t.Fatalf("remove plan = %+v", rm)
	}

	steps := strings.Join(rm.GetSteps(), "\n")
	for _, want := range []string{"power it off", "Delete the Kubernetes Node"} {
		if !strings.Contains(steps, want) {
			t.Fatalf("the remove plan lacks %q: %q", want, steps)
		}
	}

	for _, want := range []string{"cannot be undone", "STATE and EPHEMERAL partitions are wiped", "user disks are not touched", "powered off"} {
		if !strings.Contains(rm.GetWarning(), want) {
			t.Fatalf("the remove warning lacks %q: %q", want, rm.GetWarning())
		}
	}

	if strings.Contains(strings.Join(reset.GetSteps(), "\n"), "power it off") {
		t.Fatalf("a reset reboots the node, it does not power it off: %q", reset.GetSteps())
	}
}

func TestNodePlanRefusals(t *testing.T) {
	objs := append(opObjects(), opMachine("cp2", "controlplane", nil, nil))
	s, _ := opServer(t, objs...)

	for name, tc := range map[string]struct {
		machine string
		req     *pb.NodeActionRequest
		want    string
	}{
		"service outside the allowlist":      {"w1", &pb.NodeActionRequest{Action: pb.NodeAction_NODE_ACTION_RESTART_SERVICE, Service: "etcd"}, "cannot be restarted"},
		"no service":                         {"w1", &pb.NodeActionRequest{Action: pb.NodeAction_NODE_ACTION_RESTART_SERVICE}, "choose the service"},
		"etcd action on a worker":            {"w1", &pb.NodeActionRequest{Action: pb.NodeAction_NODE_ACTION_ETCD_DEFRAG}, "only control planes"},
		"stopping one of two control planes": {nodeTestCP, &pb.NodeActionRequest{Action: pb.NodeAction_NODE_ACTION_SHUTDOWN}, "two control planes"},
		"removing one of two control planes": {nodeTestCP, &pb.NodeActionRequest{Action: pb.NodeAction_NODE_ACTION_REMOVE}, "quorum"},
	} {
		tc.req.Machine = objectRef(opNS, tc.machine)

		p, err := s.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{Kind: pb.OperationKind_OPERATION_KIND_NODE, Node: tc.req}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !strings.Contains(p.Msg.GetBlocked(), tc.want) {
			t.Errorf("%s: blocked = %q, want it to contain %q", name, p.Msg.GetBlocked(), tc.want)
		}
	}

	if _, err := s.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Kind: pb.OperationKind_OPERATION_KIND_NODE, Node: &pb.NodeActionRequest{Machine: objectRef(opNS, "w1"), Service: nameKubelet, Action: pb.NodeAction_NODE_ACTION_REBOOT},
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a service with reboot: %v", err)
	}
}

func TestNodeCreateWritesOneNodeOperation(t *testing.T) {
	s, k8s := opServer(t, opObjects()...)

	res, err := s.CreateOperation(context.Background(), nodeCreateReq(&pb.NodeActionRequest{
		Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_RESTART_SERVICE, Service: nameKubelet,
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindNode || ops[0].Spec.ClusterRef.Name != opCluster {
		t.Fatalf("operations = %+v", ops)
	}

	n := ops[0].Spec.Node
	if n == nil || n.MachineRef.Name != "w1" || n.Action != ichorv1alpha1.NodeActionRestartService || n.Service != nameKubelet {
		t.Fatalf("spec.node = %+v", n)
	}

	got := res.Msg.GetOperation()
	if got.GetNodeAction() != pb.NodeAction_NODE_ACTION_RESTART_SERVICE || got.GetNodeMachine().GetName() != "w1" || res.Msg.GetAudit().GetAction() != "create" {
		t.Fatalf("response = %+v", res.Msg)
	}

	// A second one is refused while the first has not finished.
	_, err = s.CreateOperation(context.Background(), nodeCreateReq(&pb.NodeActionRequest{
		Machine: objectRef(opNS, "w2"), Action: pb.NodeAction_NODE_ACTION_REBOOT,
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second operation: %v", err)
	}
}

func TestNodeCreateIsRefusedWhenBlockedAndWritesNothing(t *testing.T) {
	s, k8s := opServer(t, opObjects()...)

	_, err := s.CreateOperation(context.Background(), nodeCreateReq(&pb.NodeActionRequest{
		Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_RESTART_SERVICE, Service: "machined",
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("service outside the allowlist: %v", err)
	}

	_, err = s.CreateOperation(context.Background(), nodeCreateReq(&pb.NodeActionRequest{
		Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_REMOVE,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("remove is not an operation: %v", err)
	}

	if ops := listOps(t, k8s); len(ops) != 0 {
		t.Fatalf("nothing may be written: %+v", ops)
	}
}

func TestNodeShutdownRestingInShutDownDoesNotBlockTheNextOperation(t *testing.T) {
	objs := append(opObjects(), &ichorv1alpha1.TalosOperation{
		Name: "shutdown-op", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindNode,
		},
		Status: ichorv1alpha1.TalosOperationStatus{Phase: ichorv1alpha1.TalosOperationPhaseShutDown},
	})
	s, _ := opServer(t, objs...)

	if p := planNodeOK(t, s, nodePlanReq("w2", pb.NodeAction_NODE_ACTION_REBOOT)); p.GetBlocked() != "" {
		t.Fatalf("a node that is off holds nothing: %q", p.GetBlocked())
	}
}

func resetReq(confirm string, override bool) *connect.Request[pb.CreateOperationRequest] {
	n := &pb.NodeActionRequest{Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_RESET, Confirm: confirm}
	if override {
		n.Override = &pb.Override{}
	}

	return nodeCreateReq(n)
}

func TestNodeResetNeedsTheNameAndOverride(t *testing.T) {
	s, k8s := opServer(t, opObjects()...)
	bg := context.Background()

	if _, err := s.CreateOperation(bg, resetReq("not-the-name", true)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("wrong name: %v", err)
	}

	if _, err := s.CreateOperation(bg, resetReq("w1", false)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no override: %v", err)
	}

	// With no signed-in user nobody can be asked whether they may override.
	if _, err := s.CreateOperation(bg, resetReq("w1", true)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("no user: %v", err)
	}

	var asked []authv1.ResourceAttributes

	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, false, &asked), nil }
	user := context.WithValue(bg, identityKey{}, Identity{User: bgUser})

	if _, err := s.CreateOperation(user, resetReq("w1", true)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("RBAC says no: %v", err)
	}

	// The start right is asked first, and denied here, so override is not reached.
	if len(asked) == 0 || asked[0].Verb != "start" || asked[0].Subresource != "node-reset" {
		t.Fatalf("access review = %+v", asked)
	}

	if ops := listOps(t, k8s); len(ops) != 0 {
		t.Fatalf("nothing may be written: %+v", ops)
	}

	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, true, &asked), nil }

	res, err := s.CreateOperation(user, resetReq("w1", true))
	if err != nil {
		t.Fatalf("allowed: %v", err)
	}

	if a := res.Msg.GetAudit(); a.GetAction() != createOverrideAction || a.GetReason() != "" {
		t.Fatalf("audit = %+v", a)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Node.Action != ichorv1alpha1.NodeActionReset || ops[0].Spec.Node.Confirm != "w1" {
		t.Fatalf("operations = %+v", ops)
	}
}

// createOverrideAction is the audit action of an override create (the "-override" suffix is added to create).
const createOverrideAction = "create-override"

// An override asks for no reason: the verb gates it, the audit record and the Event say who
// asked, and nothing typed is stored.
func TestNodeResetOverrideRecordsNoReason(t *testing.T) {
	s, k8s := opServer(t, opObjects()...)
	var asked []authv1.ResourceAttributes
	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, true, &asked), nil }
	user := context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser})

	req := nodeCreateReq(&pb.NodeActionRequest{
		Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_RESET, Confirm: "w1", Override: &pb.Override{},
	})

	res, err := s.CreateOperation(user, req)
	if err != nil {
		t.Fatalf("override: %v", err)
	}

	if a := res.Msg.GetAudit(); a.GetAction() != createOverrideAction || a.GetReason() != "" {
		t.Fatalf("audit = %+v", a)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || !ops[0].Spec.Node.Override || ops[0].Spec.Node.OverrideReason != "" {
		t.Fatalf("spec.node = %+v", ops)
	}

	var events corev1.EventList
	if err := k8s.List(context.Background(), &events); err != nil {
		t.Fatal(err)
	}

	for _, e := range events.Items {
		if e.Reason == "Override" && strings.HasSuffix(e.Message, "requested by "+bgUser+" under override") {
			return
		}
	}

	t.Fatalf("no Override event without a reason: %+v", events.Items)
}

func TestRemoveMachineDeletesAfterTheTypedName(t *testing.T) {
	s, k8s := opServer(t, opObjects()...)
	var asked []authv1.ResourceAttributes
	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, true, &asked), nil }
	ctx := context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser})
	override := &pb.Override{}

	if _, err := s.RemoveMachine(ctx, connect.NewRequest(&pb.RemoveMachineRequest{Machine: objectRef(opNS, "w1"), Confirm: "w1"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no override: %v", err)
	}

	if _, err := s.RemoveMachine(ctx, connect.NewRequest(&pb.RemoveMachineRequest{Machine: objectRef(opNS, "w1"), Confirm: "nope", Override: override})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("wrong name: %v", err)
	}

	var m ichorv1alpha1.TalosMachine
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: opNS, Name: "w1"}, &m); err != nil {
		t.Fatalf("a refused removal leaves the machine: %v", err)
	}

	res, err := s.RemoveMachine(ctx, connect.NewRequest(&pb.RemoveMachineRequest{Machine: objectRef(opNS, "w1"), Confirm: "w1", Override: override}))
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	if res.Msg.GetAudit().GetAction() != "remove" {
		t.Fatalf("audit = %+v", res.Msg.GetAudit())
	}

	err = k8s.Get(ctx, types.NamespacedName{Namespace: opNS, Name: "w1"}, &m)
	// The fake client deletes at once, or, with the finalizer the removal sets, marks it deleted.
	if err == nil && (m.DeletionTimestamp.IsZero() ||
		m.Annotations[deleteguard.AllowDeleteAnnotation] != deleteguard.AllowDeleteValue ||
		m.Annotations[deleteguard.ResetOnDeleteAnnotation] != deleteguard.AllowDeleteValue ||
		m.Annotations[deleteguard.ResetPowerOffAnnotation] != deleteguard.AllowDeleteValue) {
		t.Fatalf("a remove must reset the node and power it off: %+v", m)
	}
}

func TestRemoveMachineRefusesTheLastControlPlane(t *testing.T) {
	s, _ := opServer(t, opObjects()...)

	_, err := s.RemoveMachine(context.Background(), connect.NewRequest(&pb.RemoveMachineRequest{Machine: objectRef(opNS, nodeTestCP), Confirm: nodeTestCP}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("last control plane: %v", err)
	}
}

// A reset asked for by the node's hostname is confirmed with the machine's own name, which is what the
// TalosOperation webhook compares with machineRef.name; the hostname does not confirm.
func TestNodeResetByHostnameNeedsTheMachineNameAsConfirm(t *testing.T) {
	objs := opObjects()

	for _, o := range objs {
		if m, ok := o.(*ichorv1alpha1.TalosMachine); ok && m.Name == "w1" {
			m.Spec.Hostname = "node-w1"
		}
	}

	s, k8s := opServer(t, objs...)
	s.Impersonate = func(Identity) (client.Client, error) {
		return reviewing(k8s, true, new([]authv1.ResourceAttributes)), nil
	}
	user := context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser})

	req := nodeCreateReq(&pb.NodeActionRequest{
		Machine: objectRef(opNS, "node-w1"), Action: pb.NodeAction_NODE_ACTION_RESET, Confirm: "node-w1",
		Override: &pb.Override{},
	})
	if _, err := s.CreateOperation(user, req); err == nil || !strings.Contains(err.Error(), `type the node name "w1"`) {
		t.Fatalf("the hostname must not confirm: %v", err)
	}

	req.Msg.Node.Confirm = "w1"
	if _, err := s.CreateOperation(user, req); err != nil {
		t.Fatal(err)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Node.MachineRef.Name != "w1" || ops[0].Spec.Node.Confirm != "w1" {
		t.Fatalf("operations = %+v", ops)
	}
}

// A wipe mode is for reset only, must be one Ichor knows, shows in the plan and reaches the spec.
func TestNodeResetWipeMode(t *testing.T) {
	s, k8s := opServer(t, opObjects()...)

	plan := func(action pb.NodeAction, mode string) *connect.Request[pb.PlanOperationRequest] {
		r := nodePlanReq("w1", action)
		r.Msg.Node.WipeMode = mode

		return r
	}

	p := planNodeOK(t, s, plan(pb.NodeAction_NODE_ACTION_RESET, ichorv1alpha1.WipeModeSystemDisk))
	if steps := strings.Join(p.GetSteps(), "\n"); !strings.Contains(steps, "whole system disk") || strings.Contains(steps, "STATE and EPHEMERAL") {
		t.Fatalf("a system-disk reset must say it wipes the whole disk: %q", steps)
	}

	if steps := strings.Join(planNodeOK(t, s, plan(pb.NodeAction_NODE_ACTION_RESET, ichorv1alpha1.WipeModeSystemDiskAndUserDisks)).GetSteps(), "\n"); !strings.Contains(steps, "every user disk") {
		t.Fatalf("a system-disk-and-user-disks reset must say it wipes every user disk: %q", steps)
	}

	if steps := strings.Join(planNodeOK(t, s, plan(pb.NodeAction_NODE_ACTION_RESET, "")).GetSteps(), "\n"); !strings.Contains(steps, "STATE and EPHEMERAL") {
		t.Fatalf("the default reset keeps its text: %q", steps)
	}

	for _, bad := range []*connect.Request[pb.PlanOperationRequest]{
		plan(pb.NodeAction_NODE_ACTION_REBOOT, ichorv1alpha1.WipeModeAll),
		plan(pb.NodeAction_NODE_ACTION_RESET, "everything"),
	} {
		if _, err := s.PlanOperation(context.Background(), bad); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("%+v: %v", bad.Msg.GetNode(), err)
		}
	}

	var asked []authv1.ResourceAttributes

	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, true, &asked), nil }
	user := context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser})

	if _, err := s.CreateOperation(user, nodeCreateReq(&pb.NodeActionRequest{
		Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_RESET, Confirm: "w1", Override: &pb.Override{},
		WipeMode: ichorv1alpha1.WipeModeAll,
	})); err != nil {
		t.Fatalf("create: %v", err)
	}

	if ops := listOps(t, k8s); len(ops) != 1 || ops[0].Spec.Node.WipeMode != ichorv1alpha1.WipeModeAll {
		t.Fatalf("operations = %+v", ops)
	}
}

// --reboot: the node reboots into maintenance mode after the wipe, so no power-off is asked for,
// and the plan says it reboots.
func TestRemoveMachineWithRebootDoesNotPowerOff(t *testing.T) {
	s, k8s := opServer(t, opObjects()...)
	var asked []authv1.ResourceAttributes
	s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, true, &asked), nil }
	ctx := context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser})

	plan := planNodeOK(t, s, connect.NewRequest(&pb.PlanOperationRequest{
		Kind: pb.OperationKind_OPERATION_KIND_NODE,
		Node: &pb.NodeActionRequest{Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_REMOVE, Reboot: true},
	}))
	if !strings.Contains(strings.Join(plan.GetSteps(), "\n"), "reboot it into maintenance mode") {
		t.Fatalf("steps = %q", plan.GetSteps())
	}

	if _, err := s.RemoveMachine(ctx, connect.NewRequest(&pb.RemoveMachineRequest{
		Machine: objectRef(opNS, "w1"), Confirm: "w1", Reboot: true, Override: &pb.Override{},
	})); err != nil {
		t.Fatalf("remove: %v", err)
	}

	var m ichorv1alpha1.TalosMachine
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: opNS, Name: "w1"}, &m); err == nil {
		if m.Annotations[deleteguard.ResetOnDeleteAnnotation] != deleteguard.AllowDeleteValue || m.Annotations[deleteguard.ResetPowerOffAnnotation] != "" {
			t.Fatalf("annotations = %v", m.Annotations)
		}
	}
}

// A remove takes the same wipe modes as a reset: the plan says what is wiped, a wrong mode is refused, and
// RemoveMachine records the choice for the removal flow (the default records nothing).
func TestRemoveMachineWipeMode(t *testing.T) {
	for _, tc := range []struct{ mode, inPlan, notInPlan string }{
		{"", "STATE and EPHEMERAL", ""},
		{ichorv1alpha1.WipeModeSystemDisk, "whole system disk", ""},
		{ichorv1alpha1.WipeModeAll, "whole system disk", "every user disk"},
		{ichorv1alpha1.WipeModeSystemDiskAndUserDisks, "every user disk", ""},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			s, k8s := opServer(t, opObjects()...)
			var asked []authv1.ResourceAttributes
			s.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, true, &asked), nil }
			ctx := context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser})

			plan := planNodeOK(t, s, connect.NewRequest(&pb.PlanOperationRequest{
				Kind: pb.OperationKind_OPERATION_KIND_NODE,
				Node: &pb.NodeActionRequest{Machine: objectRef(opNS, "w1"), Action: pb.NodeAction_NODE_ACTION_REMOVE, WipeMode: tc.mode},
			}))
			text := strings.Join(plan.GetSteps(), "\n") + plan.GetWarning()
			if !strings.Contains(text, tc.inPlan) || (tc.notInPlan != "" && strings.Contains(text, tc.notInPlan)) {
				t.Fatalf("plan lacks %q or says %q: %q %q", tc.inPlan, tc.notInPlan, plan.GetSteps(), plan.GetWarning())
			}

			if _, err := s.RemoveMachine(ctx, connect.NewRequest(&pb.RemoveMachineRequest{
				Machine: objectRef(opNS, "w1"), Confirm: "w1", Override: &pb.Override{}, WipeMode: tc.mode,
			})); err != nil {
				t.Fatalf("remove: %v", err)
			}

			var m ichorv1alpha1.TalosMachine
			if err := k8s.Get(ctx, types.NamespacedName{Namespace: opNS, Name: "w1"}, &m); err != nil {
				t.Fatalf("get: %v", err)
			}

			if got := m.Annotations[deleteguard.ResetWipeModeAnnotation]; got != tc.mode {
				t.Fatalf("wipe-mode annotation = %q, want %q", got, tc.mode)
			}
		})
	}

	s, _ := opServer(t, opObjects()...)
	if _, err := s.RemoveMachine(context.Background(), connect.NewRequest(&pb.RemoveMachineRequest{
		Machine: objectRef(opNS, "w1"), Confirm: "w1", Override: &pb.Override{}, WipeMode: "everything",
	})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unknown wipe mode = %v", err)
	}
}
