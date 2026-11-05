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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The ca-rotation kind through OperationService.

const caOpsResource = "talosoperations"

func caOp(phase string) *ichorv1alpha1.TalosOperation {
	return &ichorv1alpha1.TalosOperation{
		Name: "x", Namespace: opNS,
		Spec:   ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: opCluster}, Kind: ichorv1alpha1.TalosOperationKindCARotation},
		Status: ichorv1alpha1.TalosOperationStatus{Phase: phase},
	}
}

func TestPlanCARotationNamesEveryMachineIncludingHeldTheStagesAndTheStaleBundle(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	res, err := planOp(srv, pb.OperationKind_OPERATION_KIND_CA_ROTATION, "")
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(res.Msg.GetMachines()))
	for _, m := range res.Msg.GetMachines() {
		names = append(names, m.GetName())
	}

	// A held machine is not skipped: it would be locked out when the old CA is dropped.
	if got := strings.Join(names, ","); got != "cp1,w1,held,w2" || len(res.Msg.GetHeld()) != 0 {
		t.Fatalf("machines = %s, plan = %v", got, res.Msg)
	}

	if res.Msg.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_CA_ROTATION || res.Msg.GetSpecChanges() || res.Msg.GetBlocked() != "" {
		t.Fatalf("plan = %v", res.Msg)
	}

	if got := strings.Join(res.Msg.GetStages(), ","); got != "Accepting,Issuing,Dropping" || !res.Msg.GetReplacesSecretsBundle() {
		t.Fatalf("stages = %s, plan = %v", got, res.Msg)
	}
}

func TestCreateCARotationWritesAnOperationAndNoSpec(t *testing.T) {
	srv, k8s := opServer(t, opObjects()...)

	res, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_CA_ROTATION, ""))
	if err != nil {
		t.Fatal(err)
	}

	ops := listOps(t, k8s)
	if res.Msg.GetSpecChanged() || res.Msg.GetOperation().GetKind() != pb.OperationKind_OPERATION_KIND_CA_ROTATION ||
		len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindCARotation {
		t.Fatalf("response = %v ops = %+v", res.Msg, ops)
	}
}

func TestCreateCARotationRefusedWhileAnotherOperationIsUnfinished(t *testing.T) {
	srv, k8s := opServer(t, append(opObjects(), caOp(ichorv1alpha1.TalosOperationPhaseInProgress))...)

	_, err := srv.CreateOperation(context.Background(), opCreate(pb.OperationKind_OPERATION_KIND_CA_ROTATION, ""))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || len(listOps(t, k8s)) != 1 {
		t.Fatalf("want FailedPrecondition and no new operation, got %v", err)
	}
}

func TestCreateCARotationRefusedWhenTheCallerMayNotCreateOperations(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(opObjects()...).Build()
	user := interceptor.NewClient(base, interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			if sar, ok := obj.(*authv1.SelfSubjectAccessReview); ok {
				sar.Status.Allowed = true // the start right is held; creating operations is what is refused

				return nil
			}

			return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.GroupVersion.Group, Resource: caOpsResource}, obj.GetName(), nil)
		},
	})
	srv := &Server{Client: base, Impersonate: func(Identity) (client.Client, error) { return user, nil }}

	_, err := srv.CreateOperation(asUser(context.Background()), opCreate(pb.OperationKind_OPERATION_KIND_CA_ROTATION, ""))
	if connect.CodeOf(err) != connect.CodePermissionDenied || len(listOps(t, base)) != 0 {
		t.Fatalf("want PermissionDenied and nothing written, got %v", err)
	}
}

func TestOperationToPBCarriesTheCARotationStage(t *testing.T) {
	op := caOp("")
	op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{Stage: ichorv1alpha1.CARotationStageIssuing}

	got := operationToPB(op)
	if got.GetKind() != pb.OperationKind_OPERATION_KIND_CA_ROTATION || got.GetCaRotationStage() != "Issuing" {
		t.Fatalf("operation = %v", got)
	}
}
