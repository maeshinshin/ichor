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

const (
	caTalos      = ichorv1alpha1.CARotationTargetTalos
	caKubernetes = ichorv1alpha1.CARotationTargetKubernetes
	caEtcd       = ichorv1alpha1.CARotationTargetEtcd
	caAggregator = ichorv1alpha1.CARotationTargetAggregator

	// caLegacyContract has no accepted list for the aggregator CA.
	caLegacyContract = "v1.13"
)

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

	plan, err := planCA(srv, caAggregator, caEtcd)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"snapshot", "three reboots each", "one at a time", "Extension API servers"} {
		if !strings.Contains(plan.GetWarning(), want) {
			t.Errorf("the warning does not say %q: %s", want, plan.GetWarning())
		}
	}

	// opObjects has one control plane: each etcd reboot takes the Kubernetes API down.
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
		"not a CA": {"service-account"}, "twice": {caEtcd, caEtcd},
	} {
		if _, err := planCA(srv, targets...); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: want InvalidArgument, got %v", name, err)
		}
	}

	// ca_targets belong to a ca-rotation alone.
	_, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_SHUTDOWN, CaTargets: []string{caEtcd},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a shutdown with ca_targets: want InvalidArgument, got %v", err)
	}

	// A rotation has no version target.
	if _, err = planOp(srv, pb.OperationKind_OPERATION_KIND_CA_ROTATION, opNewTalos); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a ca-rotation with a target: want InvalidArgument, got %v", err)
	}
}

func TestPlanCARotationBlocksTheAggregatorOnAnOldVersionContract(t *testing.T) {
	objs := opObjects()
	objs[0].(*ichorv1alpha1.TalosCluster).Spec.VersionContract = caLegacyContract

	srv, k8s := opServer(t, objs...)

	plan, err := planCA(srv, caAggregator)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(plan.GetBlocked(), "accepted list") {
		t.Fatalf("plan = %v", plan)
	}

	// Creating it is refused too, and writes nothing.
	_, err = srv.CreateOperation(context.Background(), connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, CaTargets: []string{caAggregator},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || len(listOps(t, k8s)) != 0 {
		t.Fatalf("want FailedPrecondition and nothing written, got %v", err)
	}

	// etcd alone is fine on that contract.
	if plan, err = planCA(srv, caEtcd); err != nil || plan.GetBlocked() != "" {
		t.Fatalf("etcd on an old contract: %v %v", plan, err)
	}
}

func TestCreateCARotationWritesTheTargetsInOrderAndNothingForTheDefault(t *testing.T) {
	srv, k8s := opServer(t, opObjects()...)

	if _, err := srv.CreateOperation(context.Background(), connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION,
		CaTargets: []string{caAggregator, caTalos, caEtcd},
	})); err != nil {
		t.Fatal(err)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.CARotation == nil ||
		!slices.Equal(ops[0].Spec.CARotation.Targets, []string{caTalos, caEtcd, caAggregator}) {
		t.Fatalf("ops = %+v", ops)
	}

	for _, o := range ops {
		if err := k8s.Delete(context.Background(), &o); err != nil {
			t.Fatal(err)
		}
	}

	// The default pair is the same as no choice: the spec carries no caRotation, as it always did.
	if _, err := srv.CreateOperation(context.Background(), connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, CaTargets: []string{caKubernetes, caTalos},
	})); err != nil {
		t.Fatal(err)
	}

	if ops = listOps(t, k8s); len(ops) != 1 || ops[0].Spec.CARotation != nil {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestPlanAndOperationCarryTheCATargets(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	def, err := planCA(srv)
	if err != nil || !slices.Equal(def.GetCaTargets(), []string{caTalos, caKubernetes}) {
		t.Fatalf("the plan of an unchosen rotation spells out the default pair: %v %v", def.GetCaTargets(), err)
	}

	chosen, err := planCA(srv, caEtcd, caTalos)
	if err != nil || !slices.Equal(chosen.GetCaTargets(), []string{caTalos, caEtcd}) {
		t.Fatalf("the plan carries the choice in canonical order: %v %v", chosen.GetCaTargets(), err)
	}

	op := caOp("")
	op.Spec.CARotation = &ichorv1alpha1.TalosOperationCARotationSpec{Targets: []string{caEtcd}}

	if got := operationToPB(op).GetCaTargets(); !slices.Equal(got, []string{caEtcd}) {
		t.Fatalf("the operation carries its targets for the UI: %v", got)
	}

	other := caOp("")
	other.Spec.Kind = ichorv1alpha1.TalosOperationKindShutdown

	if got := operationToPB(other).GetCaTargets(); len(got) != 0 {
		t.Fatalf("only a ca-rotation has targets: %v", got)
	}
}

func TestOperationDetailNamesTheCAsAndTheEtcdMembersRestarted(t *testing.T) {
	op := caOp("")
	op.Spec.CARotation = &ichorv1alpha1.TalosOperationCARotationSpec{Targets: []string{caEtcd}}

	if got := caRotationTargets(op); !slices.Equal(got, []string{caEtcd}) {
		t.Fatalf("targets = %v", got)
	}

	// Once the rotation has started the status is the record.
	op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{
		Certificates: []ichorv1alpha1.CARotationCertificate{{Name: caEtcd}, {Name: caAggregator}},
	}

	if got := caRotationTargets(op); !slices.Equal(got, []string{caEtcd, caAggregator}) {
		t.Fatalf("targets = %v", got)
	}

	if got := caRotationTargets(caOp("")); !slices.Equal(got, []string{caTalos, caKubernetes}) {
		t.Fatalf("the default targets = %v", got)
	}
}
