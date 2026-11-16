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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const silentNodeName = "node-silent"

// ownNodeWorld is a cluster of the control planes cp-named (spec.hostname node-named) and cp-silent (no hostname,
// endpoint 10.0.0.2, and no Node at that address unless extra has one, so its node name cannot be read) and the
// worker worker-named. The controller runs on controllerNode.
func ownNodeWorld(t *testing.T, controllerNode string, extra ...client.Object) (*TalosOperationReconciler, *ichorv1alpha1.TalosCluster) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	machine := func(name, role, hostname, endpoint string) *ichorv1alpha1.TalosMachine {
		return &ichorv1alpha1.TalosMachine{
			Namespace: testIchorNamespace, Name: name,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: "c"}, Role: role, Hostname: hostname, Endpoint: endpoint,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
	}

	cluster := &ichorv1alpha1.TalosCluster{Namespace: testIchorNamespace, Name: "c"}
	objs := append([]client.Object{
		cluster,
		machine("cp-named", roleControlPlane, "node-named", "10.0.0.1:50000"),
		machine("cp-silent", roleControlPlane, "", "10.0.0.2:50000"),
		machine("worker-named", workerRole, "node-worker", "10.0.0.3:50000"),
	}, extra...)

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	return &TalosOperationReconciler{Client: c, APIReader: c, NodeName: controllerNode}, cluster
}

func namesOfMachines(ms []*ichorv1alpha1.TalosMachine) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}

	return strings.Join(out, ",")
}

// A machine whose node name cannot be read may be the one the controller runs on. The two orderings that keep that
// machine for last stop rather than treat it as another machine; a machine whose name is the controller's node
// settles that the unreadable one is not it.
func TestOwnNodeOrderingStopsWhenTheControllersMachineMayBeTheUnreadOne(t *testing.T) {
	silentNode := &corev1.Node{
		Name:   silentNodeName,
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.2"}}},
	}

	for name, tc := range map[string]struct {
		controllerNode string
		extra          []client.Object
		wantErr        string // a substring of the error; "" for none
		wantOwn        string // the machine held for last
	}{
		"the controller's machine is named and another is not":      {controllerNode: "node-named", wantOwn: "cp-named"},
		"the controller's machine is the one that cannot be read":   {controllerNode: silentNodeName, wantErr: `node "` + silentNodeName + `"`},
		"the controller runs elsewhere and one name cannot be read": {controllerNode: "elsewhere", wantErr: `node "elsewhere"`},
		"the unnamed machine is found by its Node's address":        {controllerNode: silentNodeName, extra: []client.Object{silentNode}, wantOwn: "cp-silent"},
	} {
		t.Run(name, func(t *testing.T) {
			r, cluster := ownNodeWorld(t, tc.controllerNode, tc.extra...)
			ctx := context.Background()

			var all ichorv1alpha1.TalosMachineList
			if err := r.List(ctx, &all); err != nil {
				t.Fatal(err)
			}

			cps, workers := []*ichorv1alpha1.TalosMachine{&all.Items[0], &all.Items[1]}, []*ichorv1alpha1.TalosMachine{&all.Items[2]}

			_, _, own, err := r.holdOwnNodeLast(ctx, cluster, ichorv1alpha1.TalosOperationKindConfig, cps, workers)
			if got := namesOfMachines(own); (tc.wantErr == "") != (err == nil) || got != tc.wantOwn ||
				(err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("holdOwnNodeLast: own=%q err=%v, want own=%q and an error containing %q", got, err, tc.wantOwn, tc.wantErr)
			}

			_, last, err := r.shutdownPlan(ctx, cluster)
			if got := namesOfMachines(last); (tc.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) ||
				(tc.wantOwn == "cp-named" && got != "cp-silent,cp-named") {
				t.Fatalf("shutdownPlan: last=%q err=%v, want an error containing %q", got, err, tc.wantErr)
			}
		})
	}
}
