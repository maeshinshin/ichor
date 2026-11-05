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
	"testing"

	"connectrpc.com/connect"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func unmanagedCluster() *ichorv1alpha1.TalosCluster {
	return &ichorv1alpha1.TalosCluster{
		Name: "c", Namespace: testNS,
		Spec:   ichorv1alpha1.TalosClusterSpec{CNI: ichorv1alpha1.TalosClusterCNI{Preset: ichorv1alpha1.TalosClusterCNIPresetUnmanaged}},
		Status: ichorv1alpha1.TalosClusterStatus{CNI: &ichorv1alpha1.TalosClusterCNIStatus{Detected: "flannel", DetectedVersion: "v0.26.1"}},
	}
}

func TestCniServiceRefusesAnUnmanagedCluster(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(unmanagedCluster()).Build()
	s := &Server{Client: k8s}
	ctx := context.Background()
	ref := objectRef(testNS, "c")

	calls := map[string]func() error{
		"values get": func() error {
			_, err := s.GetCiliumValues(ctx, connect.NewRequest(&pb.GetCiliumValuesRequest{Cluster: ref}))
			return err
		},
		"validate": func() error {
			_, err := s.ValidateCiliumValues(ctx, connect.NewRequest(&pb.ValidateCiliumValuesRequest{Cluster: ref}))
			return err
		},
		"diff": func() error {
			_, err := s.DiffCiliumValues(ctx, connect.NewRequest(&pb.DiffCiliumValuesRequest{Cluster: ref}))
			return err
		},
		"set": func() error {
			_, err := s.SetCiliumValues(ctx, connect.NewRequest(&pb.SetCiliumValuesRequest{Cluster: ref}))
			return err
		},
		"upgrade": func() error {
			_, err := s.StartCniUpgrade(ctx, connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref}))
			return err
		},
	}

	for name, call := range calls {
		err := call()
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("%s: want FailedPrecondition, got %v", name, err)
		}

		if want := ichorv1alpha1.CNINotManagedMessage; err.(*connect.Error).Message() != want { //nolint:errorlint // test
			t.Fatalf("%s: message %q, want %q", name, err.(*connect.Error).Message(), want) //nolint:errorlint // test
		}
	}
}

func TestClusterToPBCarriesTheCNIFacts(t *testing.T) {
	got := clusterToPB(unmanagedCluster())
	if got.GetCniPreset() != "unmanaged" || got.GetCniDetected() != "flannel" || got.GetCniDetectedVersion() != "v0.26.1" {
		t.Fatalf("got %v", got)
	}
}

func TestPlanCNIOperationRefusesAnUnmanagedCluster(t *testing.T) {
	objs := opObjects()
	for _, o := range objs {
		if c, ok := o.(*ichorv1alpha1.TalosCluster); ok {
			c.Spec.CNI.Preset = ichorv1alpha1.TalosClusterCNIPresetUnmanaged
		}
	}

	srv, _ := opServer(t, objs...)

	_, err := planOp(srv, pb.OperationKind_OPERATION_KIND_CNI, kubeProxyReplaced)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}

func TestDismissCARotationAdviceSetsOnlyTheAnnotation(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(unmanagedCluster()).Build()

	resp, err := (&Server{Client: k8s}).DismissCARotationAdvice(context.Background(),
		connect.NewRequest(&pb.DismissCARotationAdviceRequest{Cluster: objectRef(testNS, "c")}))
	if err != nil {
		t.Fatal(err)
	}

	if resp.Msg.GetAudit().GetAction() != "dismiss-ca-rotation-advice" {
		t.Fatalf("audit action %q", resp.Msg.GetAudit().GetAction())
	}

	var got ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNS, Name: "c"}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Annotations[ichorv1alpha1.TalosClusterCARotationAdviceDismissedAnnotation] != "true" ||
		got.Spec.CNI.Preset != ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		t.Fatalf("got annotations %v spec %+v", got.Annotations, got.Spec.CNI)
	}
}
