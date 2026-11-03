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

package controller

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/uiapi"
	"github.com/maeshinshin/ichor/pkg/release"
)

// The order the confirmation plan shows is the order the executor runs in: same machines in, same names out.
func TestPlanOperationOrderMatchesTheExecutorsCandidateOrder(t *testing.T) {
	const ns, cl = "ns", "c"

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}

	if err := ichorv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}

	order := func(v string) map[string]string {
		return map[string]string{ichorv1alpha1.TalosMachineUpgradeOrderLabel: v}
	}
	mk := func(name, role string, labels, ann map[string]string) client.Object {
		return &ichorv1alpha1.TalosMachine{
			Name: name, Namespace: ns, Labels: labels, Annotations: ann,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cl}, Role: role,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
	}

	objs := []client.Object{
		&ichorv1alpha1.TalosCluster{
			Name: cl, Namespace: ns,
			Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: "v1.10.0", KubernetesVersion: "v1.36.0"},
		},
		mk("w-b", "worker", nil, nil),
		mk("w-a", "worker", nil, nil),
		mk("w-z", "worker", order("1"), nil),
		mk("w-y", "worker", order("2"), nil),
		mk("w-held", "worker", order("0"), map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: "x"}),
		mk("cp-2", "controlplane", nil, nil),
		mk("cp-1", "controlplane", order("5"), nil),
		mk("cp-0", "controlplane", nil, nil),
	}
	k8s := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()

	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: cl}, &cluster); err != nil {
		t.Fatal(err)
	}

	cps, workers, err := (&TalosOperationReconciler{Client: k8s}).candidateMachines(context.Background(), &cluster)
	if err != nil {
		t.Fatal(err)
	}

	want := make([]string, 0, len(cps)+len(workers))
	for _, m := range append(cps, workers...) {
		want = append(want, m.Name)
	}

	res, err := (&uiapi.Server{Client: k8s}).PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: &pb.ObjectRef{Namespace: ns, Name: cl}, Kind: pb.OperationKind_OPERATION_KIND_TALOS, Target: release.Pinned.Talos,
	}))
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(res.Msg.GetMachines()))
	for _, m := range res.Msg.GetMachines() {
		got = append(got, m.GetName())
	}

	if len(want) != 7 || !slices.Equal(got, want) {
		t.Fatalf("plan order %v, executor order %v", got, want)
	}
}
