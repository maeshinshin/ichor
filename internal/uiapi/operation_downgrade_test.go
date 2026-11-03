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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// A downgrade is refused by the plan, so CreateOperation never writes the
// lower version into the spec before an executor refuses it.
func TestPlanAndCreateRefuseADowngrade(t *testing.T) {
	for name, tc := range map[string]struct {
		kind   pb.OperationKind
		target string
	}{
		componentTalos: {pb.OperationKind_OPERATION_KIND_TALOS, "v1.14.2"},
		componentK8s:   {pb.OperationKind_OPERATION_KIND_KUBERNETES, "v1.35.9"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, base := opServer(t, opObjects()...)
			before := getCluster(t, base).Spec

			// Only the release set's version is a target, so a lower one is refused as
			// not supported, before the downgrade check is reached.
			_, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
				Cluster: objectRef(opNS, opCluster), Kind: tc.kind, Target: tc.target,
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "release set") {
				t.Fatalf("plan err = %v, want the release set refusal", err)
			}

			if _, err := srv.CreateOperation(context.Background(), opCreate(tc.kind, tc.target)); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("create err = %v", err)
			}

			if got := getCluster(t, base).Spec; got.TalosVersion != before.TalosVersion || got.KubernetesVersion != before.KubernetesVersion {
				t.Fatalf("spec was written: %+v", got)
			}

			if len(listOps(t, base)) != 0 {
				t.Fatal("an operation was created")
			}
		})
	}
}

func TestPlanRefusesAnOlderIchorVersion(t *testing.T) {
	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName, Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.5.0"},
	}
	srv, _ := opServer(t, append(opObjects(), inst)...)

	res, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_ICHOR, Target: "v0.4.0",
	}))
	if err != nil || !strings.Contains(res.Msg.GetBlocked(), "downgrade") {
		t.Fatalf("err=%v plan=%v", err, res.Msg)
	}
}
