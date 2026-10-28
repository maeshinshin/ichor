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
	"strings"
	"testing"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// These tests are plain Go tests over a fake client (no envtest): the gate
// under test reads only object metadata, argocd-cm and Flux inventories
// (docs/adr/0051).

const (
	trackedTestNS     = "default"
	trackedArgoNS     = "argocd"
	trackedEndpoint   = "10.0.0.10:50000"
	argoTrackingAnnot = "argocd.argoproj.io/tracking-id"
	argoTrackedBy     = "app:x"
)

func trackedScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}

	if err := ichorv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}

	return s
}

func trackedMachine(annotations map[string]string) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Name: "m1", Namespace: trackedTestNS, Annotations: annotations,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: "c"},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "u", Serial: "s", MACAddresses: []string{"aa:bb:cc:dd:ee:ff"}},
			Endpoint:      trackedEndpoint,
			ApprovalState: ichorv1alpha1.MachineApprovalStatePending,
		},
	}
}

func trackedPatch() *ichorv1alpha1.TalosConfigPatch {
	return &ichorv1alpha1.TalosConfigPatch{Name: "p1", Namespace: trackedTestNS}
}

func argoCM() *corev1.ConfigMap {
	return &corev1.ConfigMap{Name: "argocd-cm", Namespace: trackedArgoNS}
}

func fluxInventory(id string) *unstructured.Unstructured {
	k := &unstructured.Unstructured{}
	k.SetGroupVersionKind(schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "Kustomization"})
	k.SetName("apps")
	k.SetNamespace("flux-system")
	_ = unstructured.SetNestedSlice(k.Object, []any{map[string]any{"id": id, "v": "v1alpha1"}}, "status", "inventory", "entries")

	return k
}

func wantRefused(t *testing.T, err error, tool string) {
	t.Helper()

	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}

	for _, want := range []string{tool, "edit it in Git"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q must mention %q", err.Error(), want)
		}
	}
}

func approveReq() *connect.Request[pb.ApproveMachineRequest] {
	return connect.NewRequest(&pb.ApproveMachineRequest{
		Machine: objectRef(trackedTestNS, "m1"), Role: pb.MachineRole_MACHINE_ROLE_WORKER,
	})
}

func TestApproveRefusedWhenArgoCDTracks(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).
		WithObjects(trackedMachine(map[string]string{argoTrackingAnnot: argoTrackedBy}), argoCM()).Build()

	_, err := (&Server{Client: k8s}).ApproveMachine(context.Background(), approveReq())
	wantRefused(t, err, "Argo CD")

	var m ichorv1alpha1.TalosMachine
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: trackedTestNS, Name: "m1"}, &m); err != nil {
		t.Fatal(err)
	}

	if m.Spec.IsApproved() {
		t.Fatal("a refused approval must not write the CR")
	}
}

func TestRejectRefusedWhenArgoCDTracks(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).
		WithObjects(trackedMachine(map[string]string{argoTrackingAnnot: argoTrackedBy}), argoCM()).Build()

	_, err := (&Server{Client: k8s}).RejectMachine(context.Background(),
		connect.NewRequest(&pb.RejectMachineRequest{Machine: objectRef(trackedTestNS, "m1")}))
	wantRefused(t, err, "Argo CD")
}

func TestPatchUpdateAndDeleteRefusedWhenFluxInventoryTracks(t *testing.T) {
	id := trackedTestNS + "_p1_ichor.maesh.dev_TalosConfigPatch"
	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(trackedPatch(), fluxInventory(id)).Build()
	s := &Server{Client: k8s}

	_, err := s.UpdatePatch(context.Background(), connect.NewRequest(&pb.UpdatePatchRequest{
		Patch: sysctlPatchWire("p1", "c"),
	}))
	wantRefused(t, err, "Flux")

	_, err = s.DeletePatch(context.Background(), connect.NewRequest(&pb.DeletePatchRequest{Patch: objectRef(trackedTestNS, "p1")}))
	wantRefused(t, err, "Flux")

	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: trackedTestNS, Name: "p1"}, &ichorv1alpha1.TalosConfigPatch{}); err != nil {
		t.Fatalf("the patch must still exist: %v", err)
	}
}

func TestUntrackedWritesAreUnchanged(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(trackedMachine(nil), argoCM()).Build()

	if _, err := (&Server{Client: k8s}).ApproveMachine(context.Background(), approveReq()); err != nil {
		t.Fatalf("untracked approve: %v", err)
	}
}

func TestCreatePatchIsNotGated(t *testing.T) {
	// argocd-cm exists and a Flux inventory even names the future object:
	// a CR that does not exist yet is still created (docs/adr/0051).
	id := trackedTestNS + "_new_ichor.maesh.dev_TalosConfigPatch"
	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(argoCM(), fluxInventory(id)).Build()

	if _, err := (&Server{Client: k8s}).CreatePatch(context.Background(), connect.NewRequest(&pb.CreatePatchRequest{
		Patch: sysctlPatchWire("new", "c"),
	})); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestWriteRefusedWhenTrackingCannotBeDetermined(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(trackedMachine(nil), argoCM()).Build()
	k8s := interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.ConfigMap); ok {
				return errors.New("forbidden")
			}

			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := (&Server{Client: k8s}).ApproveMachine(context.Background(), approveReq())
	if err == nil {
		t.Fatal("want a refusal, got a successful write")
	}

	var m ichorv1alpha1.TalosMachine
	if err := base.Get(context.Background(), types.NamespacedName{Namespace: trackedTestNS, Name: "m1"}, &m); err != nil {
		t.Fatal(err)
	}

	if m.Spec.IsApproved() {
		t.Fatal("an undeterminable tracking state must not become a direct write")
	}
}
