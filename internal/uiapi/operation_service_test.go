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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// OperationService (docs/adr/0068) over a fake client: the order of the two
// writes, who makes them, and what is refused before anything is written.

const (
	opNS       = "default"
	opCluster  = "c"
	opOldTalos = "v1.14.3"
	opNewTalos = "v1.14.4"
)

func opMachine(name, role string, labels, annotations map[string]string) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Name: name, Namespace: opNS, Labels: labels, Annotations: annotations,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Role: role,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}
}

func opObjects() []client.Object {
	return []client.Object{
		&ichorv1alpha1.TalosCluster{
			Name: opCluster, Namespace: opNS,
			Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: opOldTalos, KubernetesVersion: "v1.36.0"},
		},
		opMachine("w2", "worker", nil, nil),
		opMachine("w1", "worker", map[string]string{ichorv1alpha1.TalosMachineUpgradeOrderLabel: "1"}, nil),
		opMachine("cp1", "controlplane", nil, nil),
		opMachine("held", "worker", nil, map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: "x"}),
	}
}

func opServer(t *testing.T, objs ...client.Object) (*Server, client.Client) {
	t.Helper()

	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(objs...).WithStatusSubresource(&ichorv1alpha1.TalosOperation{}).Build()

	return &Server{Client: k8s}, k8s
}

func opCreate(kind pb.OperationKind, target string) *connect.Request[pb.CreateOperationRequest] {
	return connect.NewRequest(&pb.CreateOperationRequest{Cluster: objectRef(opNS, opCluster), Kind: kind, Target: target})
}

func getCluster(t *testing.T, k8s client.Client) *ichorv1alpha1.TalosCluster {
	t.Helper()

	var c ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: opNS, Name: opCluster}, &c); err != nil {
		t.Fatal(err)
	}

	return &c
}

func listOps(t *testing.T, k8s client.Client) []ichorv1alpha1.TalosOperation {
	t.Helper()

	var l ichorv1alpha1.TalosOperationList
	if err := k8s.List(context.Background(), &l); err != nil {
		t.Fatal(err)
	}

	return l.Items
}

func TestCreateTalosUpgradeChangesTheSpecThenCreatesTheOperation(t *testing.T) {
	// The order is observed, not assumed: the interceptor records each write.
	var order []string

	base := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(opObjects()...).Build()
	k8s := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			order = append(order, "update cluster "+obj.(*ichorv1alpha1.TalosCluster).Spec.TalosVersion)

			return c.Update(ctx, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*ichorv1alpha1.TalosOperation); ok && !isDryRun(opts) {
				order = append(order, "create operation")
			}

			return c.Create(ctx, obj, opts...)
		},
	})

	res, err := (&Server{Client: k8s}).CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_TALOS, opNewTalos))
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(order, ","); got != "update cluster "+opNewTalos+",create operation" {
		t.Fatalf("write order = %q", got)
	}

	if !res.Msg.GetSpecChanged() || getCluster(t, base).Spec.TalosVersion != opNewTalos {
		t.Fatalf("spec not changed: %v", getCluster(t, base).Spec)
	}

	ops := listOps(t, base)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindTalos || ops[0].Spec.ClusterRef.Name != opCluster {
		t.Fatalf("operations = %+v", ops)
	}
}

func isDryRun(opts []client.CreateOption) bool {
	var co client.CreateOptions
	co.ApplyOptions(opts)

	return len(co.DryRun) > 0
}

func TestCreateWithSameVersionDoesNotWriteTheSpec(t *testing.T) {
	srv, k8s := opServer(t, opObjects()...)

	res, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_TALOS, opOldTalos))
	if err != nil {
		t.Fatal(err)
	}

	if res.Msg.GetSpecChanged() || len(listOps(t, k8s)) != 1 {
		t.Fatalf("specChanged=%v ops=%d", res.Msg.GetSpecChanged(), len(listOps(t, k8s)))
	}
}

func TestCreateRefusedWhenTheCallerMayNotCreateOperationsLeavesTheSpecAlone(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(opObjects()...).Build()
	user := interceptor.NewClient(base, interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.SchemeGroupVersion.Group, Resource: "talosoperations"}, obj.GetName(), nil)
		},
	})
	srv := &Server{Client: base, Impersonate: func(Identity) (client.Client, error) { return user, nil }}

	_, err := srv.CreateOperation(asUser(context.Background()), opCreate(pb.OperationKind_OPERATION_KIND_TALOS, opNewTalos))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}

	if v := getCluster(t, base).Spec.TalosVersion; v != opOldTalos {
		t.Fatalf("the spec was changed to %s although the operation was refused", v)
	}

	if len(listOps(t, base)) != 0 {
		t.Fatal("an operation exists")
	}
}

func TestCreateRefusedWhenTheCallerMayNotUpdateTheCluster(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(opObjects()...).Build()
	user := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.SchemeGroupVersion.Group, Resource: "talosclusters"}, obj.GetName(), nil)
		},
	})
	srv := &Server{Client: base, Impersonate: func(Identity) (client.Client, error) { return user, nil }}

	_, err := srv.CreateOperation(asUser(context.Background()), opCreate(pb.OperationKind_OPERATION_KIND_KUBERNETES, "v1.36.1"))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}

	if len(listOps(t, base)) != 0 {
		t.Fatal("an operation exists")
	}
}

func TestCreateRefusedWhileAnotherOperationIsUnfinished(t *testing.T) {
	running := &ichorv1alpha1.TalosOperation{
		Name: "x", Namespace: opNS,
		Spec:   ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
		Status: ichorv1alpha1.TalosOperationStatus{Phase: ichorv1alpha1.TalosOperationPhaseInProgress},
	}
	srv, k8s := opServer(t, append(opObjects(), running)...)

	_, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_TALOS, opNewTalos))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), `"x"`) {
		t.Fatalf("want FailedPrecondition naming x, got %v", err)
	}

	if getCluster(t, k8s).Spec.TalosVersion != opOldTalos {
		t.Fatal("the spec was changed")
	}
}

func TestCreateRefusedWhenArgoTracksTheCluster(t *testing.T) {
	objs := opObjects()
	c := objs[0].(*ichorv1alpha1.TalosCluster)
	c.Annotations = map[string]string{argoTrackingAnnot: "app:ichor.maesh.dev/TalosCluster:default/c"}
	objs = append(objs, argoCM())

	srv, k8s := opServer(t, objs...)

	_, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_TALOS, opNewTalos))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}

	if getCluster(t, k8s).Spec.TalosVersion != opOldTalos || len(listOps(t, k8s)) != 0 {
		t.Fatal("something was written")
	}

	// Without a target nothing in Git needs to change: the operation alone is created.
	if _, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_TALOS, "")); err != nil {
		t.Fatalf("operation without a target: %v", err)
	}
}

func TestCreateKubeProxySwitchSetsAndThenRemovesTheConfirmationAnnotation(t *testing.T) {
	var annotated, cleared bool

	base := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(opObjects()...).Build()
	k8s := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			cl := obj.(*ichorv1alpha1.TalosCluster)
			_, has := cl.Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation]

			switch {
			case has && cl.Spec.CNI.KubeProxy == "replaced":
				annotated = true
			case !has && annotated:
				cleared = true
			case !has:
				t.Error("kubeProxy changed without the confirmation annotation")
			}

			return c.Update(ctx, obj, opts...)
		},
	})

	if _, err := (&Server{Client: k8s}).CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_CNI, "replaced")); err != nil {
		t.Fatal(err)
	}

	c := getCluster(t, base)
	if !annotated || !cleared || c.Spec.CNI.KubeProxy != "replaced" || len(c.Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation]) != 0 {
		t.Fatalf("annotated=%v cleared=%v cluster=%+v", annotated, cleared, c)
	}

	if ops := listOps(t, base); len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindCNI {
		t.Fatalf("operations = %+v", ops)
	}
}

func TestCreateValidatesTheTarget(t *testing.T) {
	srv, k8s := opServer(t, opObjects()...)

	for name, req := range map[string]*connect.Request[pb.CreateOperationRequest]{
		"shutdown with a target":  opCreate(pb.OperationKind_OPERATION_KIND_SHUTDOWN, "v1"),
		"unspecified kind":        opCreate(pb.OperationKind_OPERATION_KIND_UNSPECIFIED, ""),
		"kube-proxy bad mode":     opCreate(pb.OperationKind_OPERATION_KIND_CNI, "off"),
		"talos target not semver": opCreate(pb.OperationKind_OPERATION_KIND_TALOS, "latest"),
	} {
		if _, err := srv.CreateOperation(context.Background(), req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: want InvalidArgument, got %v", name, err)
		}
	}

	if len(listOps(t, k8s)) != 0 {
		t.Fatal("an operation was created")
	}
}

func TestShutdownCreatesAnOperationWithoutTouchingTheSpec(t *testing.T) {
	srv, k8s := opServer(t, opObjects()...)

	res, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_SHUTDOWN, ""))
	if err != nil {
		t.Fatal(err)
	}

	if res.Msg.GetSpecChanged() || res.Msg.GetOperation().GetKind() != pb.OperationKind_OPERATION_KIND_SHUTDOWN {
		t.Fatalf("response = %v", res.Msg)
	}

	if len(listOps(t, k8s)) != 1 || res.Msg.GetAudit().GetAction() != "create" {
		t.Fatalf("ops=%d audit=%v", len(listOps(t, k8s)), res.Msg.GetAudit())
	}
}

func TestPlanTalosListsControlPlanesFirstThenWorkersByOrderLabelAndSkipsHeld(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_TALOS, Target: opNewTalos,
	}))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(res.Msg.GetMachines()))
	for _, m := range res.Msg.GetMachines() {
		names = append(names, m.GetName())
	}

	if got := strings.Join(names, ","); got != "cp1,w1,w2" {
		t.Fatalf("order = %s", got)
	}

	if res.Msg.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_REBOOT || !res.Msg.GetSpecChanges() ||
		res.Msg.GetCurrent() != opOldTalos || strings.Join(res.Msg.GetHeld(), ",") != "held" {
		t.Fatalf("plan = %v", res.Msg)
	}
}

func TestPlanShutdownIsWorkersThenControlPlanesAndStops(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_SHUTDOWN,
	}))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(res.Msg.GetMachines()))
	for _, m := range res.Msg.GetMachines() {
		names = append(names, m.GetName())
	}

	// Holds do not apply to a shutdown (ADR 0067), so "held" is stopped too.
	if got := strings.Join(names, ","); got != "w1,held,w2,cp1" || res.Msg.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_STOP || len(res.Msg.GetHeld()) != 0 {
		t.Fatalf("plan = %v", res.Msg)
	}
}

func TestPlanSaysWhyCreateWouldBeRefused(t *testing.T) {
	running := &ichorv1alpha1.TalosOperation{
		Name: "x", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
	}
	srv, _ := opServer(t, append(opObjects(), running)...)

	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_TALOS, Target: opNewTalos,
	}))
	if err != nil || !strings.Contains(res.Msg.GetBlocked(), `"x"`) {
		t.Fatalf("err=%v plan=%v", err, res.Msg)
	}
}

func rebootPending(m *ichorv1alpha1.TalosMachine, paths ...string) *ichorv1alpha1.TalosMachine {
	m.Status.RebootPaths = paths

	return m
}

func configObjects() []client.Object {
	return []client.Object{
		&ichorv1alpha1.TalosCluster{
			Name: opCluster, Namespace: opNS,
			Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: opOldTalos, KubernetesVersion: "v1.36.0"},
		},
		rebootPending(opMachine("w2", "worker", nil, nil), "machine.network.interfaces"),
		rebootPending(opMachine("cp1", "controlplane", nil, nil), "cluster.proxy.disabled", "machine.network.interfaces"),
		opMachine("converged", "worker", nil, nil),
		rebootPending(opMachine("held", "worker", nil, map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: "x"}), "cluster.proxy.disabled"),
	}
}

func TestPlanConfigListsOnlyMachinesWithAPendingRebootAndTheirFields(t *testing.T) {
	srv, _ := opServer(t, configObjects()...)

	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CONFIG,
	}))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(res.Msg.GetMachines()))

	for _, m := range res.Msg.GetMachines() {
		names = append(names, m.GetName())
	}

	if got := strings.Join(names, ","); got != "cp1,w2" {
		t.Fatalf("machines = %s, want the control plane then the worker, not the converged or held ones", got)
	}

	if f := res.Msg.GetMachines()[0].GetRebootFields(); strings.Join(f, ",") != "cluster.proxy.disabled,machine.network.interfaces" {
		t.Fatalf("cp1 reboot fields = %v", f)
	}

	if res.Msg.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_REBOOT || res.Msg.GetSpecChanges() || res.Msg.GetBlocked() != "" ||
		strings.Join(res.Msg.GetHeld(), ",") != "held" {
		t.Fatalf("plan = %v", res.Msg)
	}
}

func TestPlanSaysTheKubernetesAPIGoesDownWhenTheOnlyControlPlaneReboots(t *testing.T) {
	plan := func(objs ...client.Object) *pb.PlanOperationResponse {
		srv, _ := opServer(t, objs...)

		res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
			Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CONFIG,
		}))
		if err != nil {
			t.Fatal(err)
		}

		return res.Msg
	}

	if !plan(configObjects()...).GetKubernetesApiUnavailable() {
		t.Fatal("one approved control plane that reboots: the plan must say the Kubernetes API is unavailable")
	}

	twoPlanes := append(configObjects(), rebootPending(opMachine("cp2", "controlplane", nil, nil), "machine.network.interfaces"))
	if plan(twoPlanes...).GetKubernetesApiUnavailable() {
		t.Fatal("with a second control plane the API stays up")
	}

	workerOnly := []client.Object{
		configObjects()[0],
		rebootPending(opMachine("w2", "worker", nil, nil), "machine.network.interfaces"),
		opMachine("cp1", "controlplane", nil, nil),
	}
	if plan(workerOnly...).GetKubernetesApiUnavailable() {
		t.Fatal("the control plane does not reboot in this plan, so the API stays up")
	}
}

func TestPlanConfigIsBlockedWhenNothingNeedsAReboot(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CONFIG,
	}))
	if err != nil || !strings.Contains(res.Msg.GetBlocked(), "needs a reboot") {
		t.Fatalf("err=%v plan=%v", err, res.Msg)
	}

	_, err = srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_CONFIG, ""))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("create err = %v, want FailedPrecondition", err)
	}
}

func TestCreateConfigCreatesAnOperationWithoutTouchingTheSpecAndTakesNoTarget(t *testing.T) {
	srv, k8s := opServer(t, configObjects()...)

	before := getCluster(t, k8s).ResourceVersion

	res, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_CONFIG, ""))
	if err != nil {
		t.Fatal(err)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindConfig || res.Msg.GetSpecChanged() ||
		getCluster(t, k8s).ResourceVersion != before {
		t.Fatalf("ops=%+v spec changed=%v", ops, res.Msg.GetSpecChanged())
	}

	_, err = srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_CONFIG, "v1.2.3"))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a target err = %v, want InvalidArgument", err)
	}
}

func TestPauseResumeAndDeleteAreMadeAsTheCaller(t *testing.T) {
	op := &ichorv1alpha1.TalosOperation{
		Name: "x", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
	}
	srv, k8s := opServer(t, append(opObjects(), op)...)
	ctx := context.Background()

	res, err := srv.SetOperationPaused(ctx, connect.NewRequest(&pb.SetOperationPausedRequest{Operation: objectRef(opNS, "x"), Paused: true}))
	if err != nil || !res.Msg.GetOperation().GetPaused() || listOps(t, k8s)[0].Spec.Paused != true {
		t.Fatalf("pause: err=%v res=%v", err, res)
	}

	if _, err := srv.SetOperationPaused(ctx, connect.NewRequest(&pb.SetOperationPausedRequest{Operation: objectRef(opNS, "x")})); err != nil ||
		listOps(t, k8s)[0].Spec.Paused {
		t.Fatalf("resume: %v", err)
	}

	denied := interceptor.NewClient(k8s.(client.WithWatch), interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.SchemeGroupVersion.Group, Resource: "talosoperations"}, obj.GetName(), nil)
		},
	})
	srv.Impersonate = func(Identity) (client.Client, error) { return denied, nil }

	if _, err := srv.DeleteOperation(asUser(ctx), connect.NewRequest(&pb.DeleteOperationRequest{Operation: objectRef(opNS, "x")})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}

	srv.Impersonate = nil

	if _, err := srv.DeleteOperation(ctx, connect.NewRequest(&pb.DeleteOperationRequest{Operation: objectRef(opNS, "x")})); err != nil || len(listOps(t, k8s)) != 0 {
		t.Fatalf("delete: %v", err)
	}
}

func TestListAndGetReturnStatusMachinesAsTheyAre(t *testing.T) {
	op := &ichorv1alpha1.TalosOperation{
		Name: "x", Namespace: opNS,
		Spec: ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
		Status: ichorv1alpha1.TalosOperationStatus{
			Phase: ichorv1alpha1.TalosOperationPhasePaused, FailureReason: "boom",
			Machines: []ichorv1alpha1.TalosOperationMachineStatus{{Name: "cp1", Phase: "Completed"}, {Name: "w1", Phase: "InProgress"}},
		},
	}
	other := op.DeepCopy()
	other.Name, other.Spec.ClusterRef.Name = "y", "other"

	srv, _ := opServer(t, append(opObjects(), op, other)...)

	l, err := srv.ListOperations(context.Background(), connect.NewRequest(&pb.ListOperationsRequest{Cluster: objectRef(opNS, opCluster)}))
	if err != nil || len(l.Msg.GetOperations()) != 1 {
		t.Fatalf("list: err=%v res=%v", err, l)
	}

	g, err := srv.GetOperation(context.Background(), connect.NewRequest(&pb.GetOperationRequest{Operation: objectRef(opNS, "x")}))
	if err != nil {
		t.Fatal(err)
	}

	o := g.Msg.GetOperation()
	if o.GetFailureReason() != "boom" || len(o.GetMachines()) != 2 || o.GetMachines()[1].GetPhase() != "InProgress" || o.GetPhase() != "Paused" {
		t.Fatalf("operation = %v", o)
	}
}
