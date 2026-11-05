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

package installer

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

const ciliumCRDStub = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ciliumnetworkpolicies.cilium.io
spec:
  group: cilium.io
  names: {kind: CiliumNetworkPolicy, listKind: CiliumNetworkPolicyList, plural: ciliumnetworkpolicies, singular: ciliumnetworkpolicy}
  scope: Namespaced
  versions:
  - name: v2
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        x-kubernetes-preserve-unknown-fields: true
`

func policyManifest(ns string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: ichor-default-deny
  namespace: %s
spec:
  endpointSelector: {}
  ingress:
  - {}
`, ns))
}

func policyExists(t *testing.T, ns string) bool {
	t.Helper()

	return exists(t, "cilium.io/v2", "CiliumNetworkPolicy", ns, "ichor-default-deny")
}

// The policies are applied last and only when Cilium's CRD is Established: an upgrade on a
// cluster without Cilium must not fail on a kind the API server does not know.
// One test, in this order, because the CRD is cluster-wide and stays once created.
func TestRunAppliesNetworkPoliciesOnlyWhenCiliumsCRDExists(t *testing.T) {
	f := newFixture(t)

	var order []string

	err := Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run, Manifest: f.manifest("ctrl:v2", ""), Ready: noWait(&order),
		NetworkPolicies: policyManifest(f.ns),
	})
	if err != nil {
		t.Fatalf("Run without Cilium: %v", err)
	}

	if policyExists(t, f.ns) {
		t.Fatal("a policy was applied although Cilium's CRD does not exist")
	}

	crds, err := Decode([]byte(ciliumCRDStub))
	if err != nil {
		t.Fatal(err)
	}

	if err := applyOne(context.Background(), k8sClient, crds[0]); err != nil {
		t.Fatalf("creating the Cilium CRD stub: %v", err)
	}

	err = wait.PollUntilContextTimeout(context.Background(), 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(crds[0].GroupVersionKind())

		if err := k8sClient.Get(ctx, types.NamespacedName{Name: crds[0].GetName()}, live); err != nil {
			return false, err
		}

		return crdEstablished(live), nil
	})
	if err != nil {
		t.Fatalf("waiting for the stub CRD: %v", err)
	}

	f2 := newFixture(t)

	order = nil

	err = Run(context.Background(), k8sClient, Options{
		Namespace: f2.ns, RunID: f2.run, Manifest: f2.manifest("ctrl:v2", ""), Ready: noWait(&order),
		NetworkPolicies: policyManifest(f2.ns), CNIDetected: detectedCilium,
	})
	if err != nil {
		t.Fatalf("Run with Cilium: %v", err)
	}

	if !policyExists(t, f2.ns) {
		t.Fatal("the policy was not applied although Cilium's CRD is Established")
	}

	if last := order[len(order)-1]; last != "CiliumNetworkPolicy/ichor-default-deny" {
		t.Errorf("the policies must be the last step, post-checks ran in order %v", order)
	}

	// The CRD exists now, but another CNI (or none detected yet) still skips them.
	for _, detected := range []string{"flannel", ""} {
		f3 := newFixture(t)

		err = Run(context.Background(), k8sClient, Options{
			Namespace: f3.ns, RunID: f3.run, Manifest: f3.manifest("ctrl:v2", ""), Ready: noWait(&order),
			NetworkPolicies: policyManifest(f3.ns), CNIDetected: detected,
		})
		if err != nil {
			t.Fatalf("Run (CNI %q): %v", detected, err)
		}

		if policyExists(t, f3.ns) {
			t.Fatalf("a policy was applied with the CNI detected as %q", detected)
		}
	}

	// The operator's explicit opt-out wins over Cilium plus the CRD, and the reason is logged.
	f4 := newFixture(t)

	var log strings.Builder

	err = Run(context.Background(), k8sClient, Options{
		Namespace: f4.ns, RunID: f4.run, Manifest: f4.manifest("ctrl:v2", ""), Ready: noWait(&order),
		NetworkPolicies: policyManifest(f4.ns), CNIDetected: detectedCilium, SkipCiliumPolicies: true, Log: &log,
	})
	if err != nil {
		t.Fatalf("Run with the opt-out: %v", err)
	}

	if policyExists(t, f4.ns) {
		t.Fatal("a policy was applied although the operator opted out")
	}

	if !strings.Contains(log.String(), "opted out") {
		t.Errorf("the opt-out reason was not logged: %q", log.String())
	}
}

const detectedCilium = "cilium"

func TestIncludeCiliumPolicies(t *testing.T) {
	for _, tc := range []struct {
		detected  string
		crd, want bool
	}{
		{detectedCilium, true, true}, {detectedCilium, false, false}, {"flannel", true, false},
		{"calico", true, false}, {"other", true, false}, {"none", true, false}, {"", true, false},
	} {
		if got := IncludeCiliumPolicies(tc.detected, tc.crd); got != tc.want {
			t.Errorf("IncludeCiliumPolicies(%q, %v) = %v, want %v", tc.detected, tc.crd, got, tc.want)
		}
	}
}
