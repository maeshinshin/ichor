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
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	orderOwnSpecEndpoint = "10.9.9.9:50000"
	orderOwnObserved     = "10.0.0.22:50000"
	orderOwnIP           = "10.0.0.22"
)

// kubernetesOrderObjects is a cluster of cp1, cp2, cp3, w1 and w2 (and a held worker) whose controller runs on
// ownName, or on no machine when ownName is "".
func kubernetesOrderObjects(ownName string) []client.Object {
	objs := append(make([]client.Object, 0, 9),
		&ichorv1alpha1.TalosCluster{
			Name: opCluster, Namespace: opNS,
			Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: opOldTalos, KubernetesVersion: opOldK8s, VersionContract: testVersionContract},
		},
		opMachine("cp1", "controlplane", nil, nil), opMachine("cp2", "controlplane", nil, nil), opMachine("cp3", "controlplane", nil, nil),
		opMachine("w2", "worker", nil, nil), opMachine("w1", "worker", nil, nil),
		opMachine("held", "worker", nil, map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: "x"}),
	)

	if ownName == "" {
		return objs
	}

	for _, o := range objs {
		if m, ok := o.(*ichorv1alpha1.TalosMachine); ok && m.Name == ownName {
			m.Spec.Endpoint = orderOwnSpecEndpoint
			m.Status.ObservedEndpoint, m.Status.ObservedEndpointFor = orderOwnObserved, m.Spec.Endpoint
		}
	}

	return append(objs,
		&corev1.Pod{
			Name: testControllerPodName, Namespace: opNS, Labels: map[string]string{testControllerLabelKey: testControllerLabelVal},
			Spec: corev1.PodSpec{NodeName: testControllerNode},
		},
		&corev1.Node{Name: testControllerNode, Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: orderOwnIP}}}},
	)
}

func planKubernetesOrderOf(t *testing.T, ownName string) *pb.PlanOperationResponse {
	t.Helper()

	srv, _ := opServer(t, kubernetesOrderObjects(ownName)...)

	res, err := planOp(srv, pb.OperationKind_OPERATION_KIND_KUBERNETES, "v1.36.6")
	if err != nil {
		t.Fatal(err)
	}

	return res.Msg
}

func plannedMachines(p *pb.PlanOperationResponse) string {
	got := make([]string, 0, len(p.GetMachines()))

	for _, m := range p.GetMachines() {
		got = append(got, fmt.Sprintf("%s@%d", m.GetName(), m.GetStep()))
	}

	return strings.Join(got, ",")
}

// The executor visits every control plane, the controller's own included in its place, in each of the first three
// stages, and holds the controller's own machine back only in the Kubelets stage, where it goes last. The plan names
// that order for each stage.
func TestPlanKubernetesStatesTheOrderEachStageRunsInWhenTheControllerRunsOnAControlPlane(t *testing.T) {
	p := planKubernetesOrderOf(t, "cp2")
	own := "cp2 (where Ichor's controller runs)"

	want := []string{
		"kube-apiserver on every control plane: cp1, then " + own + ", then cp3",
		"kube-controller-manager and kube-scheduler on every control plane: cp1, then " + own + ", then cp3",
		"kube-proxy and the bootstrap manifests: cp1, then " + own + ", then cp3",
		"kubelets: cp1, then cp3, then w1, then w2, then " + own,
	}

	if got := p.GetStages(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stages =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The machine list is the Kubelets stage's.
	if got := plannedMachines(p); got != "cp1@1,cp3@2,w1@3,w2@4,cp2@5" {
		t.Fatalf("machines = %s, want the Kubelets stage's order with the controller's own control plane last", got)
	}
}

func TestPlanKubernetesHoldsAWorkerThatHostsTheControllerBackToo(t *testing.T) {
	p := planKubernetesOrderOf(t, "w1")

	if got := plannedMachines(p); got != "cp1@1,cp2@2,cp3@3,w2@4,w1@5" {
		t.Fatalf("machines = %s", got)
	}

	if got := p.GetStages()[0]; got != "kube-apiserver on every control plane: cp1, then cp2, then cp3" {
		t.Fatalf("first stage = %q: a worker is not in it", got)
	}
}

func TestPlanKubernetesWithoutAControllerMachineListsControlPlanesThenWorkers(t *testing.T) {
	p := planKubernetesOrderOf(t, "")

	if got := plannedMachines(p); got != "cp1@1,cp2@2,cp3@3,w1@4,w2@5" || strings.Contains(strings.Join(p.GetStages(), "|"), "where Ichor") {
		t.Fatalf("machines = %s, stages = %q", got, p.GetStages())
	}
}
