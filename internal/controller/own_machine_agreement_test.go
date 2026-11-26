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
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// Two pieces of code decide which machine the controller runs on: the controller (holdOwnNodeLast, from its own
// node name) and the plan the UI backend shows (from the controller's pod). This builds one cluster and asks both,
// and fails when they name different machines, which no test of either alone can see.
func TestThePlanAndTheExecutorNameTheSameMachineAsTheControllers(t *testing.T) {
	const lastMachine = "zz-last" // sorts after every other worker, so the plan ends with it unless it holds the controller's own

	type scenario struct {
		// setup changes the machines (by name) and returns the Nodes of the cluster and the Node the controller's pod runs on.
		setup func(ms map[string]*ichorv1alpha1.TalosMachine) (nodes []client.Object, podNode string)
	}

	node := func(name, address string) client.Object {
		n := &corev1.Node{Name: name}
		if address != "" {
			n.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: address}}
		}

		return n
	}

	// addressNodes gives every machine a Node, named node-<machine>, at the address it answers on.
	addressNodes := func(ms map[string]*ichorv1alpha1.TalosMachine) (nodes []client.Object) {
		for name, m := range ms {
			nodes = append(nodes, node("node-"+name, hostOf(m.DialEndpoint())))
		}

		return nodes
	}

	for name, sc := range map[string]scenario{
		"a machine with spec.hostname": {setup: func(ms map[string]*ichorv1alpha1.TalosMachine) ([]client.Object, string) {
			ms["cp2"].Spec.Hostname = "named-cp2"

			return append(addressNodes(ms), node("named-cp2", "")), "named-cp2"
		}},
		"a worker found by the address of its Node": {setup: func(ms map[string]*ichorv1alpha1.TalosMachine) ([]client.Object, string) {
			return addressNodes(ms), "node-w1"
		}},
		"a control plane found at the address it was last observed on": {setup: func(ms map[string]*ichorv1alpha1.TalosMachine) ([]client.Object, string) {
			ms["cp2"].Status.ObservedEndpoint, ms["cp2"].Status.ObservedEndpointFor = "10.0.9.2:50000", ms["cp2"].Spec.Endpoint

			return addressNodes(ms), "node-cp2"
		}},
		"a control plane whose Node still has the address spec names": {setup: func(ms map[string]*ichorv1alpha1.TalosMachine) ([]client.Object, string) {
			nodes := addressNodes(ms) // before the machine moves: node-cp2 is at the address spec names
			ms["cp2"].Status.ObservedEndpoint, ms["cp2"].Status.ObservedEndpointFor = "10.0.9.2:50000", ms["cp2"].Spec.Endpoint

			return nodes, "node-cp2"
		}},
		"a controller outside the cluster": {setup: func(ms map[string]*ichorv1alpha1.TalosMachine) ([]client.Object, string) {
			return addressNodes(ms), "elsewhere"
		}},
	} {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := clientgoscheme.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}

			if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}

			ms := map[string]*ichorv1alpha1.TalosMachine{}

			for i, mm := range []struct{ name, role string }{
				{"cp1", roleControlPlane}, {"cp2", roleControlPlane}, {"cp3", roleControlPlane},
				{"w1", workerRole}, {"w2", workerRole}, {lastMachine, workerRole},
			} {
				ms[mm.name] = &ichorv1alpha1.TalosMachine{
					Namespace: testIchorNamespace, Name: mm.name,
					Spec: ichorv1alpha1.TalosMachineSpec{
						ClusterRef: corev1.LocalObjectReference{Name: "c"}, Role: mm.role, Endpoint: fmt.Sprintf("10.0.0.%d:50000", i+1),
						ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
					},
				}
			}

			nodes, podNode := sc.setup(ms)

			cluster := &ichorv1alpha1.TalosCluster{
				Namespace: testIchorNamespace, Name: "c",
				Spec: ichorv1alpha1.TalosClusterSpec{
					TalosVersion: "v1.13.2", KubernetesVersion: "v1.35.4", VersionContract: "v1.14",
				},
			}
			pod := &corev1.Pod{
				Namespace: testIchorNamespace, Name: "ichor-controller", Labels: map[string]string{controlPlaneLabel: valueControllerMgr},
				Spec: corev1.PodSpec{NodeName: podNode},
			}

			objs := append([]client.Object{cluster, pod}, nodes...)
			for _, m := range ms {
				objs = append(objs, m)
			}

			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			ctx := context.Background()

			// The plan.
			res, err := (&uiapi.Server{Client: c}).PlanOperation(ctx, connect.NewRequest(&pb.PlanOperationRequest{
				Cluster: &pb.ObjectRef{Namespace: testIchorNamespace, Name: "c"}, Kind: pb.OperationKind_OPERATION_KIND_KUBERNETES, Target: "v1.36.6",
			}))
			if err != nil {
				t.Fatal(err)
			}

			planOwn := ""
			if machines := res.Msg.GetMachines(); machines[len(machines)-1].GetName() != lastMachine {
				planOwn = machines[len(machines)-1].GetName()
			}

			// The executor.
			r := &TalosOperationReconciler{Client: c, APIReader: c, NodeName: podNode}
			cps := []*ichorv1alpha1.TalosMachine{ms["cp1"], ms["cp2"], ms["cp3"]}
			workers := []*ichorv1alpha1.TalosMachine{ms["w1"], ms["w2"], ms[lastMachine]}

			_, _, own, err := r.holdOwnNodeLast(ctx, cluster, ichorv1alpha1.TalosOperationKindKubernetes, cps, workers)
			execOwn := namesOfMachines(own)

			if err != nil {
				execOwn = "(stopped: " + err.Error() + ")"
			}

			if planOwn != execOwn {
				t.Fatalf("the plan says the controller runs on %q, the executor on %q", planOwn, execOwn)
			}
		})
	}
}

func hostOf(endpoint string) string {
	host, _, _ := strings.Cut(endpoint, ":")

	return host
}
