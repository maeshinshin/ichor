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

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ownClusterName is the managed cluster of these tests.
const ownClusterName = "prod"

// reclaimControllerStub plays the controller: a request that has no phase is inspected, and one with a confirmUUID is Done.
func reclaimControllerStub(t *testing.T, refusals []string) (client.Client, *ichorv1alpha1.MachineReclaim) {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	var last ichorv1alpha1.MachineReclaim

	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if err := cl.Get(ctx, key, obj, opts...); err != nil {
				return err
			}

			if mr, ok := obj.(*ichorv1alpha1.MachineReclaim); ok {
				switch {
				case mr.Spec.ConfirmUUID != "":
					mr.Status.Phase, mr.Status.Result = ichorv1alpha1.MachineReclaimDone, "10.0.0.5 is in maintenance mode; the Kubernetes Node w1 was deleted"
				default:
					mr.Status.Phase = ichorv1alpha1.MachineReclaimAwaitingConfirmation
					mr.Status.Inspection = &ichorv1alpha1.MachineReclaimInspection{
						UUID: testUUID, Hostname: "w1", Role: testRoleWorker, ClusterName: ownClusterName, ClusterID: "cid", ClusterReachable: true,
						Graceful: true, Refusals: refusals,
					}
				}

				last = *mr.DeepCopy()
			}

			return nil
		},
	}).Build()

	return c, &last
}

func ownDeps(c client.Client, stdin string, out *bytes.Buffer, registered *[]string) ownReclaimDeps {
	return ownReclaimDeps{
		K8s: c, Namespace: ichorControllerNamespace, Who: func(context.Context) (string, error) { return testUserAlice, nil },
		Register: func(_ context.Context, a string) error { *registered = append(*registered, a); return nil },
		In:       strings.NewReader(stdin), Out: out, Poll: time.Millisecond,
	}
}

func TestOwnReclaimWipesAnOrphanAndRegistersIt(t *testing.T) {
	c, last := reclaimControllerStub(t, nil)

	var (
		out        bytes.Buffer
		registered []string
	)

	err := runMachineReclaimOwn(context.Background(), ownDeps(c, testUUID+"\n", &out, &registered), testEndpointAddr,
		reclaimFlags{Cluster: ownClusterName, Wait: time.Minute})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	sp := last.Spec
	if !sp.ClusterCredentials || sp.CredentialSecretRef != "" || sp.ClusterRef == nil || sp.ClusterRef.Name != ownClusterName || sp.ConfirmUUID != testUUID {
		t.Fatalf("request %+v", sp)
	}

	if last.Annotations[ichorv1alpha1.ReclaimRequestedByAnnotation] != testUserAlice || last.Annotations[ichorv1alpha1.ReclaimConfirmedByAnnotation] != testUserAlice {
		t.Fatalf("audit annotations %v", last.Annotations)
	}

	if len(registered) != 1 || registered[0] != testEndpointAddr {
		t.Fatalf("registered %v", registered)
	}
}

func TestOwnReclaimNeedsTheCluster(t *testing.T) {
	c, _ := reclaimControllerStub(t, nil)

	var (
		out        bytes.Buffer
		registered []string
	)

	err := runMachineReclaimOwn(context.Background(), ownDeps(c, "", &out, &registered), testEndpointAddr, reclaimFlags{})
	if err == nil || !strings.Contains(err.Error(), "--cluster NAME") {
		t.Fatalf("%v", err)
	}
}

// The two refusals of an orphan never send the user round in a circle: a machine that is still approved is
// pointed at Remove node here, and adopt's own refusal says to wipe with reclaim (see TestAdoptRefusalOffersTheWipe).
func TestOwnReclaimRefusalPointsToRemoveNode(t *testing.T) {
	c, last := reclaimControllerStub(t, []string{"node AAAA-1 is already an Ichor machine (approved); to wipe it, use Remove node (`ichor node remove`), which wipes it"})

	var (
		out        bytes.Buffer
		registered []string
	)

	err := runMachineReclaimOwn(context.Background(), ownDeps(c, testUUID+"\n", &out, &registered), testEndpointAddr,
		reclaimFlags{Cluster: ownClusterName, Wait: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "Remove node") || strings.Contains(err.Error(), "adopt") {
		t.Fatalf("%v", err)
	}

	if last.Spec.ConfirmUUID != "" || len(registered) != 0 {
		t.Fatalf("a refused request must send nothing: %+v", last.Spec)
	}
}
