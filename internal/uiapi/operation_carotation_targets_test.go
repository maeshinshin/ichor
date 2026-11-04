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
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The etcd and aggregator targets of a ca-rotation through OperationService.

func planCA(srv *Server, targets ...string) (*pb.PlanOperationResponse, error) {
	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, CaTargets: targets,
	}))
	if err != nil {
		return nil, err
	}

	return res.Msg, nil
}

func TestPlanCARotationWithEtcdAndAggregatorWarnsAndStopsTheAPIOfASingleControlPlane(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	plan, err := planCA(srv, "kubernetes-aggregator", "etcd")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"snapshot", "one member at a time", "Extension API servers"} {
		if !strings.Contains(plan.GetWarning(), want) {
			t.Errorf("the warning does not say %q: %s", want, plan.GetWarning())
		}
	}

	// opObjects has one control plane: each etcd restart takes the Kubernetes API down.
	if !plan.GetKubernetesApiUnavailable() || plan.GetBlocked() != "" {
		t.Fatalf("plan = %v", plan)
	}

	def, err := planCA(srv)
	if err != nil {
		t.Fatal(err)
	}

	if def.GetWarning() != "" || def.GetKubernetesApiUnavailable() {
		t.Fatalf("the default rotation has no warning and does not stop the API: %v", def)
	}
}

func TestPlanCARotationRefusesWhatItCannotRotate(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	for name, targets := range map[string][]string{
		"unknown": {"nonsense"}, "twice": {"etcd", "etcd"},
	} {
		if _, err := planCA(srv, targets...); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: want InvalidArgument, got %v", name, err)
		}
	}

	// ca_targets belong to a ca-rotation alone.
	_, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_SHUTDOWN, CaTargets: []string{"etcd"},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a shutdown with ca_targets: want InvalidArgument, got %v", err)
	}
}

func TestPlanCARotationBlocksTheAggregatorOnAnOldVersionContract(t *testing.T) {
	objs := opObjects()
	objs[0].(*ichorv1alpha1.TalosCluster).Spec.VersionContract = "v1.13"

	srv, k8s := opServer(t, objs...)

	plan, err := planCA(srv, "kubernetes-aggregator")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(plan.GetBlocked(), "accepted list") {
		t.Fatalf("plan = %v", plan)
	}

	// Creating it is refused too, and writes nothing.
	_, err = srv.CreateOperation(context.Background(), connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, CaTargets: []string{"kubernetes-aggregator"},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || len(listOps(t, k8s)) != 0 {
		t.Fatalf("want FailedPrecondition and nothing written, got %v", err)
	}

	// etcd alone is fine on that contract.
	if plan, err = planCA(srv, "etcd"); err != nil || plan.GetBlocked() != "" {
		t.Fatalf("etcd on v1.13: %v %v", plan, err)
	}
}

func TestCreateCARotationWritesTheTargetsInOrderAndNothingForTheDefault(t *testing.T) {
	srv, k8s := opServer(t, opObjects()...)

	if _, err := srv.CreateOperation(context.Background(), connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION,
		CaTargets: []string{"kubernetes-aggregator", "talos", "etcd"},
	})); err != nil {
		t.Fatal(err)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.CARotation == nil ||
		!slices.Equal(ops[0].Spec.CARotation.Targets, []string{"talos", "etcd", "kubernetes-aggregator"}) {
		t.Fatalf("ops = %+v", ops)
	}

	for _, o := range ops {
		if err := k8s.Delete(context.Background(), &o); err != nil {
			t.Fatal(err)
		}
	}

	// The default pair is the same as no choice: the spec carries no caRotation, as it always did.
	if _, err := srv.CreateOperation(context.Background(), connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, CaTargets: []string{"kubernetes", "talos"},
	})); err != nil {
		t.Fatal(err)
	}

	if ops = listOps(t, k8s); len(ops) != 1 || ops[0].Spec.CARotation != nil {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestOperationDetailNamesTheCAsAndTheEtcdMembersRestarted(t *testing.T) {
	op := caOp("")
	op.Spec.CARotation = &ichorv1alpha1.TalosOperationCARotationSpec{Targets: []string{"etcd"}}

	if got := strings.Join(caRotationTargets(op), ","); got != "etcd" {
		t.Fatalf("targets = %s", got)
	}

	// Once the rotation has started the status is the record.
	op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{
		Certificates: []ichorv1alpha1.CARotationCertificate{{Name: "etcd"}, {Name: "kubernetes-aggregator"}},
	}

	if got := strings.Join(caRotationTargets(op), ","); got != "etcd,kubernetes-aggregator" {
		t.Fatalf("targets = %s", got)
	}

	if got := strings.Join(caRotationTargets(caOp("")), ","); got != "talos,kubernetes" {
		t.Fatalf("the default targets = %s", got)
	}
}
