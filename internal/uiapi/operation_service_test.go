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
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// OperationService (docs/adr/0068) over a fake client: the order of the two
// writes, who makes them, and what is refused before anything is written.

const (
	opNS       = "default"
	opCluster  = "c"
	opOldTalos = "v1.13.2"
	opNewTalos = "v1.14.5" // the release set's Talos (ADR 0113)

	testControllerLabelKey = "control-plane"
	testControllerPodName  = "ichor-controller"
	testControllerLabelVal = "controller-manager"
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
			Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: opOldTalos, KubernetesVersion: opOldK8s},
		},
		opMachine("w2", "worker", nil, nil),
		opMachine("w1", "worker", map[string]string{ichorv1alpha1.TalosMachineUpgradeOrderLabel: "1"}, nil),
		opMachine("cp1", "controlplane", nil, nil),
		opMachine("held", "worker", nil, map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: "x"}),
	}
}

func opServer(t *testing.T, objs ...client.Object) (*Server, client.Client) {
	t.Helper()

	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).WithStatusSubresource(&ichorv1alpha1.TalosOperation{}).Build()

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

	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(opObjects()...).Build()
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
	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(opObjects()...).Build()
	user := interceptor.NewClient(base, interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.GroupVersion.Group, Resource: "talosoperations"}, obj.GetName(), nil)
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
	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(opObjects()...).Build()
	user := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.GroupVersion.Group, Resource: "talosclusters"}, obj.GetName(), nil)
		},
	})
	srv := &Server{Client: base, Impersonate: func(Identity) (client.Client, error) { return user, nil }}

	_, err := srv.CreateOperation(asUser(context.Background()), opCreate(pb.OperationKind_OPERATION_KIND_KUBERNETES, "v1.36.6"))
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

func TestCreateKubeProxySwitchSetsAndThenRemovesTheConfirmationAnnotation(t *testing.T) {
	var annotated, cleared bool

	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(opObjects()...).Build()
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

// A controller write between the server's read and its update is a conflict; the switch must read
// again and go on instead of stopping with the mode changed and no operation (hardware run).
func TestCreateKubeProxySwitchSurvivesConflictingUpdates(t *testing.T) {
	var conflicts int

	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(opObjects()...).Build()
	k8s := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			// The first update of the spec and the first update that removes the annotation both lose a race.
			_, has := obj.(*ichorv1alpha1.TalosCluster).Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation]
			if conflicts < 2 && (has && conflicts == 0 || !has && conflicts == 1) {
				conflicts++

				return apierrors.NewConflict(schema.GroupResource{Group: "ichor.maesh.dev", Resource: resourceTalosClusters}, obj.GetName(),
					errors.New("the object has been modified"))
			}

			return c.Update(ctx, obj, opts...)
		},
	})

	if _, err := (&Server{Client: k8s}).CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_CNI, kubeProxyReplaced)); err != nil {
		t.Fatal(err)
	}

	c := getCluster(t, base)
	if conflicts != 2 || c.Spec.CNI.KubeProxy != kubeProxyReplaced || len(c.Annotations) != 0 || len(listOps(t, base)) != 1 {
		t.Fatalf("conflicts=%d cluster=%+v ops=%d", conflicts, c, len(listOps(t, base)))
	}
}

// A retry after an earlier failure (or a Git-first change that carries the annotation) finds the
// spec already changed; the annotation must not outlive the operation it consented to.
func TestCreateKubeProxySwitchClearsALeftoverAnnotation(t *testing.T) {
	objs := opObjects()
	cl := objs[0].(*ichorv1alpha1.TalosCluster)
	cl.Spec.CNI.KubeProxy = kubeProxyReplaced
	cl.Annotations = map[string]string{ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation: kubeProxyReplaced}

	srv, k8s := opServer(t, objs...)

	if _, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_CNI, kubeProxyReplaced)); err != nil {
		t.Fatal(err)
	}

	if c := getCluster(t, k8s); len(c.Annotations) != 0 || c.Spec.CNI.KubeProxy != kubeProxyReplaced || len(listOps(t, k8s)) != 1 {
		t.Fatalf("cluster=%+v ops=%d", c, len(listOps(t, k8s)))
	}
}

func TestCreateValidatesTheTarget(t *testing.T) {
	srv, k8s := opServer(t, opObjects()...)

	for name, req := range map[string]*connect.Request[pb.CreateOperationRequest]{
		"shutdown with a target":     opCreate(pb.OperationKind_OPERATION_KIND_SHUTDOWN, "v1"),
		"unspecified kind":           opCreate(pb.OperationKind_OPERATION_KIND_UNSPECIFIED, ""),
		"kube-proxy bad mode":        opCreate(pb.OperationKind_OPERATION_KIND_CNI, "off"),
		"talos target not semver":    opCreate(pb.OperationKind_OPERATION_KIND_TALOS, "latest"),
		"ichor target leading zero":  opCreate(pb.OperationKind_OPERATION_KIND_ICHOR, "v0.0.1814000000-02357186"),
		"kubernetes target no patch": opCreate(pb.OperationKind_OPERATION_KIND_KUBERNETES, "v1.34"),
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

func TestPlanTalosNamesASchematicOnlyChange(t *testing.T) {
	const newSchematic = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	plan := func(schematic, running string) *pb.PlanOperationResponse {
		objs := opObjects()
		if schematic != "" {
			objs[0].(*ichorv1alpha1.TalosCluster).Spec.Schematic = &ichorv1alpha1.TalosClusterSchematic{ID: schematic}
		}

		for _, o := range objs[1:] {
			o.(*ichorv1alpha1.TalosMachine).Status.Schematic = running
		}

		srv, _ := opServer(t, objs...)

		res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
			Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_TALOS,
		}))
		if err != nil {
			t.Fatal(err)
		}

		return res.Msg
	}

	got := plan(newSchematic, upgrade.DefaultSchematic)
	if got.GetCurrentSchematic() != upgrade.DefaultSchematic || got.GetTargetSchematic() != newSchematic || got.GetSpecChanges() {
		t.Fatalf("plan = %v", got)
	}

	for name, p := range map[string]*pb.PlanOperationResponse{
		"same schematic":               plan(newSchematic, newSchematic),
		"unknown node, stock target":   plan("", ""),
		"unknown node, stock explicit": plan(upgrade.DefaultSchematic, ""),
	} {
		if p.GetCurrentSchematic() != "" || p.GetTargetSchematic() != "" {
			t.Errorf("%s: plan names a schematic change: %v", name, p)
		}
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

const opOldK8s = "v1.35.4" // a minor below the release set (1.36.6), so the upgrade is a minor one

func rebootPending(m *ichorv1alpha1.TalosMachine, paths ...string) *ichorv1alpha1.TalosMachine {
	m.Status.RebootPaths = paths

	return m
}

func configObjects() []client.Object {
	return []client.Object{
		&ichorv1alpha1.TalosCluster{
			Name: opCluster, Namespace: opNS,
			Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: opOldTalos, KubernetesVersion: opOldK8s},
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

// ADR 0080 amendment: a pending change that only restarts kube-apiserver selects the machine, with no
// reboot fields, and a plan of such machines is a restart, not a reboot; one reboot makes it a reboot.
func TestPlanConfigSelectsAPIServerRestartMachinesAndSaysNoReboot(t *testing.T) {
	restartOnly := func(m *ichorv1alpha1.TalosMachine) *ichorv1alpha1.TalosMachine {
		m.Status.APIServerRestartPaths = []string{"KubeAuthenticationConfig.configuration"}

		return m
	}
	cluster := &ichorv1alpha1.TalosCluster{
		Name: opCluster, Namespace: opNS,
		Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: opOldTalos, KubernetesVersion: opOldK8s},
	}
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

	got := plan(cluster, restartOnly(opMachine(nodeTestCP, "controlplane", nil, nil)), opMachine("converged", "worker", nil, nil))
	if len(got.GetMachines()) != 1 || got.GetMachines()[0].GetName() != nodeTestCP || len(got.GetMachines()[0].GetRebootFields()) != 0 ||
		got.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS || got.GetBlocked() != "" ||
		!got.GetKubernetesApiUnavailable() {
		t.Fatalf("restart-only plan = %v", got)
	}

	mixed := plan(cluster, restartOnly(opMachine(nodeTestCP, "controlplane", nil, nil)),
		rebootPending(opMachine("w1", "worker", nil, nil), "machine.network.interfaces"))
	if len(mixed.GetMachines()) != 2 || mixed.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_REBOOT {
		t.Fatalf("mixed plan = %v", mixed)
	}
}

// ADR 0080: the controller reboots the machine it runs on last, so the plan lists that machine last
// even when it is a control plane.
func TestPlanConfigListsTheControllersOwnMachineLast(t *testing.T) {
	objs := configObjects()
	cp1 := objs[2].(*ichorv1alpha1.TalosMachine)
	cp1.Spec.Endpoint = "10.9.9.9:50000"
	cp1.Status.ObservedEndpoint, cp1.Status.ObservedEndpointFor = "10.0.0.22:50000", cp1.Spec.Endpoint

	pod := &corev1.Pod{
		Name: testControllerPodName, Namespace: opNS, Labels: map[string]string{testControllerLabelKey: testControllerLabelVal},
		Spec: corev1.PodSpec{NodeName: "ctl-node"},
	}
	node := &corev1.Node{Name: "ctl-node", Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.22"}}}}

	srv, _ := opServer(t, append(objs, node, pod)...)

	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CONFIG,
	}))
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(res.Msg.GetMachines()))

	for _, m := range res.Msg.GetMachines() {
		got = append(got, fmt.Sprintf("%s@%d", m.GetName(), m.GetStep()))
	}

	if strings.Join(got, ",") != "w2@1,cp1@2" {
		t.Fatalf("machines = %v, want the worker, then the controller's own control plane last", got)
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
			return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.GroupVersion.Group, Resource: "talosoperations"}, obj.GetName(), nil)
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

func TestAPausedOperationBlocksAndNamesBothWaysOut(t *testing.T) {
	paused := &ichorv1alpha1.TalosOperation{
		Name: "upgrade-x", Namespace: opNS,
		Spec:   ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
		Status: ichorv1alpha1.TalosOperationStatus{Phase: ichorv1alpha1.TalosOperationPhasePaused},
	}
	srv, k8s := opServer(t, append(opObjects(), paused)...)
	ctx := context.Background()

	cl := &ichorv1alpha1.TalosCluster{}
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: opNS, Name: opCluster}, cl); err != nil {
		t.Fatal(err)
	}

	got, err := srv.unfinishedOperation(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}

	want := `operation "upgrade-x" (talos) on this cluster is not finished (phase Paused); ` +
		`resume it with 'ichor operation resume upgrade-x', or delete it with 'ichor operation delete upgrade-x'`
	if got != want {
		t.Errorf("unfinishedOperation =\n%q\nwant\n%q", got, want)
	}
}

func TestUnfinishedOperationTellsAFailedIchorUpgradeToBeDeleted(t *testing.T) {
	failed := &ichorv1alpha1.TalosOperation{
		Name: "upgrade-ichor-x", Namespace: opNS,
		Spec:   ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindIchor},
		Status: ichorv1alpha1.TalosOperationStatus{Phase: ichorv1alpha1.TalosOperationPhasePaused, FailureReason: "installer Job failed"},
	}
	srv, k8s := opServer(t, append(opObjects(), failed)...)
	ctx := context.Background()

	cl := &ichorv1alpha1.TalosCluster{}
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: opNS, Name: opCluster}, cl); err != nil {
		t.Fatal(err)
	}

	got, err := srv.unfinishedOperation(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"rolled back", "ichor operation delete upgrade-ichor-x", "ichor upgrade ichor <version>"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q lacks %q", got, want)
		}
	}
}

func TestResumeAnnotatesOnlyAFailedOperation(t *testing.T) {
	newOp := func(name, phase, reason string, last *metav1.Time) *ichorv1alpha1.TalosOperation {
		return &ichorv1alpha1.TalosOperation{
			Name: name, Namespace: opNS,
			Spec:   ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindTalos},
			Status: ichorv1alpha1.TalosOperationStatus{Phase: phase, FailureReason: reason, LastRetry: last},
		}
	}

	future := metav1.NewTime(time.Now().Add(time.Hour))
	srv, k8s := opServer(t, append(opObjects(),
		newOp("failed", ichorv1alpha1.TalosOperationPhasePaused, "boom", nil),
		newOp("operator-paused", ichorv1alpha1.TalosOperationPhasePaused, "", nil),
		newOp("in-progress", ichorv1alpha1.TalosOperationPhaseInProgress, "", nil),
		newOp("retried", ichorv1alpha1.TalosOperationPhasePaused, "boom again", &future),
	)...)
	ctx := context.Background()

	resume := func(name string) error {
		_, err := srv.SetOperationPaused(ctx, connect.NewRequest(&pb.SetOperationPausedRequest{Operation: objectRef(opNS, name)}))

		return err
	}

	if err := resume("failed"); err != nil {
		t.Fatal(err)
	}

	annotation := func(name string) string {
		for _, o := range listOps(t, k8s) {
			if o.Name == name {
				return o.Annotations[ichorv1alpha1.OperationRetryAnnotation]
			}
		}

		return ""
	}

	if annotation("failed") == "" {
		t.Fatal("a failed operation was not annotated")
	}

	for _, name := range []string{"operator-paused", "in-progress"} {
		if err := resume(name); err != nil || annotation(name) != "" {
			t.Errorf("%s: err = %v, annotation %q; want a plain resume and no annotation", name, err, annotation(name))
		}
	}

	// A request is always newer than the one the controller already acted on.
	if err := resume("retried"); err != nil {
		t.Fatal(err)
	}

	at, err := time.Parse(time.RFC3339, annotation("retried"))
	if err != nil || !at.After(future.Time) {
		t.Fatalf("annotation %q (%v) is not after lastRetry %v", annotation("retried"), err, future.Time)
	}
}

func TestMachineActionsAcceptAHostnameAndRefuseAnAmbiguousOne(t *testing.T) {
	mk := func(name, host string) *ichorv1alpha1.TalosMachine {
		m := opMachine(name, "worker", nil, nil)
		m.Spec.Hostname = host

		return m
	}

	srv, _ := opServer(t, append(opObjects(), mk("11111111-aaaa", "w-1"), mk("22222222-bbbb", "dup"), mk("33333333-cccc", "dup"))...)
	ctx := context.Background()

	m, err := srv.resolveMachine(ctx, objectRef(opNS, "w-1"))
	if err != nil || m.Name != "11111111-aaaa" {
		t.Fatalf("hostname: %v, %v", m, err)
	}

	if m, err = srv.resolveMachine(ctx, objectRef(opNS, "11111111-aaaa")); err != nil || m.Name != "11111111-aaaa" {
		t.Fatalf("own name: %v, %v", m, err)
	}

	if _, err = srv.resolveMachine(ctx, objectRef(opNS, "dup")); connect.CodeOf(err) != connect.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "22222222-bbbb") {
		t.Fatalf("ambiguous: %v", err)
	}

	if _, err = srv.resolveMachine(ctx, objectRef(opNS, "nobody")); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown: %v", err)
	}
}
