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
	"time"

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The key-rotation kind through OperationService (docs/adr/0121).

// keyObjects is opObjects with a version contract the key rotation can run under.
func keyObjects(contract string) []client.Object {
	objs := opObjects()
	objs[0].(*ichorv1alpha1.TalosCluster).Spec.VersionContract = contract

	return objs
}

func keyPlan(srv *Server, kr *pb.KeyRotationRequest) (*pb.PlanOperationResponse, error) {
	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_KEY_ROTATION, KeyRotation: kr,
	}))
	if err != nil {
		return nil, err
	}

	return res.Msg, nil
}

func TestPlanKeyRotationNamesTheControlPlanesTheStagesAndTheStaleBundle(t *testing.T) {
	srv, _ := opServer(t, keyObjects("v1.14")...)

	plan, err := keyPlan(srv, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Only control planes carry the two documents: a worker is not touched.
	names := make([]string, 0, len(plan.GetMachines()))
	for _, m := range plan.GetMachines() {
		names = append(names, m.GetName())
	}

	if got := strings.Join(names, ","); got != nodeTestCP {
		t.Fatalf("machines = %s", got)
	}

	if plan.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_KEY_ROTATION || plan.GetSpecChanges() || plan.GetBlocked() != "" {
		t.Fatalf("plan = %v", plan)
	}

	if got := strings.Join(plan.GetStages(), ","); got != "Accepting,Issuing,Reencrypting,SettlingTokens,Dropping" || !plan.GetReplacesSecretsBundle() {
		t.Fatalf("stages = %s, plan = %v", got, plan)
	}

	// With one control plane its kube-apiserver restarts in every applying stage and the API is away meanwhile.
	if !plan.GetKubernetesApiUnavailable() {
		t.Fatalf("a lone control plane's API server goes down: %v", plan)
	}

	// Without the secretbox target there is nothing to re-encrypt, without the other nothing to settle.
	for target, want := range map[string]string{
		ichorv1alpha1.KeyRotationTargetSecretbox:      "Accepting,Issuing,Reencrypting,Dropping",
		ichorv1alpha1.KeyRotationTargetServiceAccount: "Accepting,Issuing,SettlingTokens,Dropping",
	} {
		plan, err = keyPlan(srv, &pb.KeyRotationRequest{Targets: []string{target}})
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(plan.GetStages(), ","); got != want {
			t.Fatalf("%s-only stages = %s, want %s", target, got, want)
		}
	}
}

func TestPlanKeyRotationIsBlockedBelowTheMultiDocumentContractNamingIt(t *testing.T) {
	srv, _ := opServer(t, keyObjects("v1.13")...)

	plan, err := keyPlan(srv, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(plan.GetBlocked(), "v1.14") {
		t.Fatalf("blocked = %q", plan.GetBlocked())
	}

	if _, err := srv.CreateOperation(context.Background(), keyCreate(nil)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("a blocked plan must refuse the create: %v", err)
	}
}

func TestPlanKeyRotationRefusesWhatItCannotRun(t *testing.T) {
	srv, _ := opServer(t, keyObjects("v1.14")...)

	for name, req := range map[string]*pb.KeyRotationRequest{
		"an unknown key":                             {Targets: []string{"aescbc"}},
		"a duplicated key":                           {Targets: []string{ichorv1alpha1.KeyRotationTargetSecretbox, ichorv1alpha1.KeyRotationTargetSecretbox}},
		"a bad duration":                             {TokenSettle: "soon"},
		"a negative duration":                        {TokenSettle: "-5m"},
		"a settle too short":                         {TokenSettle: "5m"},
		"a short settle for the service-account key": {Targets: []string{ichorv1alpha1.KeyRotationTargetServiceAccount}, TokenSettle: "74m"},
	} {
		if _, err := keyPlan(srv, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: want InvalidArgument, got %v", name, err)
		}
	}

	// The secretbox key has no tokens to wait for: a short settle is ignored, not refused.
	if _, err := keyPlan(srv, &pb.KeyRotationRequest{Targets: []string{ichorv1alpha1.KeyRotationTargetSecretbox}, TokenSettle: "5m"}); err != nil {
		t.Errorf("a secretbox-only rotation with a short settle: %v", err)
	}

	// A target (a version) means nothing here.
	_, err := planOp(srv, pb.OperationKind_OPERATION_KIND_KEY_ROTATION, opNewTalos)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a target: want InvalidArgument, got %v", err)
	}
}

func keyCreate(kr *pb.KeyRotationRequest) *connect.Request[pb.CreateOperationRequest] {
	return connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_KEY_ROTATION, KeyRotation: kr,
	})
}

func TestCreateKeyRotationWritesTheSpecOfTheRequestAndNoClusterSpec(t *testing.T) {
	srv, k8s := opServer(t, keyObjects("v1.14")...)

	res, err := srv.CreateOperation(context.Background(), keyCreate(&pb.KeyRotationRequest{
		Targets: []string{ichorv1alpha1.KeyRotationTargetSecretbox, ichorv1alpha1.KeyRotationTargetServiceAccount}, TokenSettle: "2h", IgnoreLegacyTokens: true,
	}))
	if err != nil {
		t.Fatal(err)
	}

	ops := listOps(t, k8s)
	if res.Msg.GetSpecChanged() || res.Msg.GetOperation().GetKind() != pb.OperationKind_OPERATION_KIND_KEY_ROTATION || len(ops) != 1 {
		t.Fatalf("response = %v ops = %+v", res.Msg, ops)
	}

	kr := ops[0].Spec.KeyRotation
	if ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindKeyRotation || kr == nil {
		t.Fatalf("operation = %+v", ops[0].Spec)
	}

	targets := slices.Clone(kr.Targets)
	slices.Sort(targets)

	if !slices.Equal(targets, []string{ichorv1alpha1.KeyRotationTargetSecretbox, ichorv1alpha1.KeyRotationTargetServiceAccount}) || kr.TokenSettle == nil ||
		kr.TokenSettle.Duration != 2*time.Hour || !kr.IgnoreLegacyTokens {
		t.Fatalf("spec.keyRotation = %+v", kr)
	}
}

func TestCreateKeyRotationDefaultsToBothKeysAndTheDefaultSettle(t *testing.T) {
	srv, k8s := opServer(t, keyObjects("v1.14")...)

	if _, err := srv.CreateOperation(context.Background(), keyCreate(nil)); err != nil {
		t.Fatal(err)
	}

	kr := listOps(t, k8s)[0].Spec.KeyRotation

	targets := slices.Clone(kr.Targets)
	slices.Sort(targets)

	if !slices.Equal(targets, []string{ichorv1alpha1.KeyRotationTargetSecretbox, ichorv1alpha1.KeyRotationTargetServiceAccount}) || kr.TokenSettle != nil || kr.IgnoreLegacyTokens {
		t.Fatalf("spec.keyRotation = %+v", kr)
	}
}

func TestCreateKeyRotationRefusedWhileAnotherOperationIsUnfinished(t *testing.T) {
	srv, k8s := opServer(t, append(keyObjects("v1.14"), caOp(ichorv1alpha1.TalosOperationPhaseInProgress))...)

	if _, err := srv.CreateOperation(context.Background(), keyCreate(nil)); connect.CodeOf(err) != connect.CodeFailedPrecondition || len(listOps(t, k8s)) != 1 {
		t.Fatalf("want FailedPrecondition and no new operation, got %v", err)
	}
}

func TestOperationToPBCarriesTheKeyRotationStageAndDetails(t *testing.T) {
	op := caOp("")
	op.Spec.Kind = ichorv1alpha1.TalosOperationKindKeyRotation
	op.Spec.KeyRotation = &ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: []string{ichorv1alpha1.KeyRotationTargetSecretbox}}
	op.Status.KeyRotation = &ichorv1alpha1.TalosOperationKeyRotationStatus{Stage: ichorv1alpha1.KeyRotationStageReencrypting}

	got := operationToPB(op)
	if got.GetKind() != pb.OperationKind_OPERATION_KIND_KEY_ROTATION || got.GetKeyRotationStage() != "Reencrypting" {
		t.Fatalf("operation = %v", got)
	}

	if !strings.Contains(got.GetSummary(), "key") {
		t.Fatalf("summary = %q", got.GetSummary())
	}

	details := map[string]string{}
	for _, d := range got.GetDetails() {
		details[d.GetKey()] = d.GetValue()
	}

	if details["Keys"] != ichorv1alpha1.KeyRotationTargetSecretbox {
		t.Fatalf("details = %v", details)
	}
}
