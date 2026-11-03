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
	"testing"

	"connectrpc.com/connect"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/release"
)

// The cluster rollout: the steps a cluster behind the release set needs, in order, and
// that a start creates the first one only.
func TestPlanClusterUpgradeOrdersTalosThenKubernetes(t *testing.T) {
	srv, _ := opServer(t, opObjects()...)

	res, err := srv.PlanClusterUpgrade(context.Background(), connect.NewRequest(&pb.PlanClusterUpgradeRequest{
		Cluster: objectRef(opNS, opCluster),
	}))
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, s := range res.Msg.GetSteps() {
		got = append(got, s.GetStep())
	}

	if want := []string{"talos", "kubernetes"}; !slices.Equal(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}

	if first := res.Msg.GetSteps()[0]; first.GetTo() != release.Pinned.Talos || first.GetKind() != pb.OperationKind_OPERATION_KIND_TALOS {
		t.Fatalf("first step = %v", first)
	}

	if res.Msg.GetReleaseSet().GetTalosVersion() != release.Pinned.Talos {
		t.Fatalf("release set = %v", res.Msg.GetReleaseSet())
	}
}

func TestStartClusterUpgradeCreatesOnlyTheNextStep(t *testing.T) {
	srv, k8s := opServer(t, opObjects()...)
	ref := objectRef(opNS, opCluster)

	res, err := srv.StartClusterUpgrade(context.Background(), connect.NewRequest(&pb.StartClusterUpgradeRequest{Cluster: ref}))
	if err != nil {
		t.Fatal(err)
	}

	if res.Msg.GetStep() != "talos" {
		t.Fatalf("step = %q, want talos", res.Msg.GetStep())
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindTalos {
		t.Fatalf("operations = %v, want one talos operation", ops)
	}

	// The next step waits: the Talos operation is unfinished, so nothing else starts on the cluster.
	_, err = srv.StartClusterUpgrade(context.Background(), connect.NewRequest(&pb.StartClusterUpgradeRequest{Cluster: ref}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second start err = %v, want FailedPrecondition", err)
	}

	if len(listOps(t, k8s)) != 1 {
		t.Fatal("a second operation was created while the first runs")
	}
}

func TestComponentVersionsCompareWithTheReleaseSet(t *testing.T) {
	c := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
		TalosVersion: "v1.13.2", VersionContract: "v1.13", KubernetesVersion: release.Pinned.Kubernetes,
	}}

	got := map[string]string{}
	for _, v := range componentVersionsToPB(c) {
		got[v.GetName()] = v.GetState()
	}

	want := map[string]string{
		componentTalos:    string(release.Behind),
		componentContract: string(release.Behind),
		componentK8s:      string(release.UpToDate),
		componentCilium:   string(release.Unknown),
	}
	for name, state := range want {
		if got[name] != state {
			t.Errorf("%s state = %q, want %q", name, got[name], state)
		}
	}
}
