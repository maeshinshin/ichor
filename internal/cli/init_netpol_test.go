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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	keyAPIVersion = "apiVersion"
	keyStatus     = "status"
	keyType       = "type"
	keyName       = "name"
	keyNamespace  = "namespace"
	keyKind       = "kind"
	keyMetadata   = "metadata"
)

const testPolicies = `apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: ichor-default-deny
  namespace: ichor-system
spec:
  endpointSelector: {}
  ingress:
  - {}
`

func ciliumDaemonSetObj(desired, ready int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		keyAPIVersion: "apps/v1", keyKind: "DaemonSet",
		keyMetadata: map[string]any{keyNamespace: ciliumNamespace, keyName: ciliumDaemonSet},
		keyStatus:   map[string]any{"desiredNumberScheduled": desired, "numberReady": ready},
	}}
}

func ciliumCRDObj(established bool) *unstructured.Unstructured {
	status := "False"
	if established {
		status = "True"
	}

	return &unstructured.Unstructured{Object: map[string]any{
		keyAPIVersion: "apiextensions.k8s.io/v1", keyKind: "CustomResourceDefinition",
		keyMetadata: map[string]any{keyName: ciliumPolicyCRDKey},
		keyStatus: map[string]any{"conditions": []any{
			map[string]any{keyType: "Established", keyStatus: status},
		}},
	}}
}

// The policies are created only once the cilium DaemonSet has its agents ready and the CRD is
// Established, and the wait is retried until then (ADR 0100).
func TestApplyNetworkPolicies_WaitsForCiliumReady(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme(t)

	// What the cluster answers over time: one of two agents ready for the first two reads of the
	// DaemonSet, then both, and the CRD not Established until the fourth read of the DaemonSet.
	reads, ready := 0, false

	wrapped := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ciliumDaemonSetObj(2, 1), ciliumCRDObj(false)).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				err := cl.Get(ctx, key, obj, opts...)
				u, ok := obj.(*unstructured.Unstructured)

				switch {
				case err != nil || !ok:
				case u.GetKind() == "DaemonSet":
					reads++

					if reads >= 3 {
						u.Object[keyStatus] = ciliumDaemonSetObj(2, 2).Object[keyStatus]
					}
				case u.GetKind() == "CustomResourceDefinition" && reads >= 4:
					u.Object[keyStatus] = ciliumCRDObj(true).Object[keyStatus]
				}

				return err
			},
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				ready = ready || reads >= 4

				return cl.Create(ctx, obj, opts...)
			},
			Apply: func(ctx context.Context, cl client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
				ready = ready || reads >= 4

				return cl.Apply(ctx, obj, opts...)
			},
		}).Build()

	var out bytes.Buffer

	if err := applyNetworkPolicies(ctx, wrapped, []byte(testPolicies), cniCilium, time.Minute, time.Millisecond, &out); err != nil {
		t.Fatalf("applyNetworkPolicies: %v\n%s", err, out.String())
	}

	if !ready {
		t.Errorf("a policy was created before Cilium was Ready (DaemonSet read %d times)", reads)
	}

	if !strings.Contains(out.String(), "waiting for Cilium to be Ready") {
		t.Errorf("no wait was reported:\n%s", out.String())
	}

	got := &unstructured.Unstructured{}
	got.SetAPIVersion("cilium.io/v2")
	got.SetKind("CiliumNetworkPolicy")

	if err := wrapped.Get(ctx, client.ObjectKey{Namespace: ichorControllerNamespace, Name: "ichor-default-deny"}, got); err != nil {
		t.Fatalf("the policy was not created: %v", err)
	}
}

func TestApplyNetworkPolicies_NothingIsCreatedWhileCiliumIsNotReady(t *testing.T) {
	ctx := context.Background()

	created := false
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(ciliumDaemonSetObj(1, 0), ciliumCRDObj(true)).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				created = true

				return nil
			},
			Apply: func(context.Context, client.WithWatch, runtime.ApplyConfiguration, ...client.ApplyOption) error {
				created = true

				return nil
			},
		}).Build()

	var out bytes.Buffer

	err := applyNetworkPolicies(ctx, c, []byte(testPolicies), cniCilium, 50*time.Millisecond, 5*time.Millisecond, &out)
	if err == nil || !strings.Contains(err.Error(), "Cilium is not Ready") {
		t.Fatalf("err = %v, want the Cilium not Ready error", err)
	}

	if created {
		t.Error("a policy was created although Cilium never became Ready")
	}
}

func TestApplyNetworkPolicies_SkipsWithoutCilium(t *testing.T) {
	created := false
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			created = true

			return nil
		},
	}).Build()

	var out bytes.Buffer

	if err := applyNetworkPolicies(context.Background(), c, []byte(testPolicies), "flannel", time.Millisecond, time.Millisecond, &out); err != nil {
		t.Fatalf("flannel: %v", err)
	}

	if err := applyNetworkPolicies(context.Background(), c, nil, cniCilium, time.Millisecond, time.Millisecond, &out); err != nil {
		t.Fatalf("no manifest: %v", err)
	}

	if created {
		t.Error("a policy was created on a cluster that cannot enforce it")
	}

	if !strings.Contains(out.String(), "flannel") {
		t.Errorf("the skip was not explained:\n%s", out.String())
	}
}

// Through runInit: the policies come after the CRs, once Cilium is Ready.
func TestRunInit_AppliesNetworkPoliciesAfterCiliumIsReady(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(ciliumDaemonSetObj(1, 1), ciliumCRDObj(true)).Build()
	deps, _, _ := newFakeInitDeps(t, k8sClient)
	deps.NetworkPolicies = []byte(testPolicies)
	deps.CiliumWait = time.Second

	opts := validInitOptions()
	opts.Namespace = ichorControllerNamespace

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	got := &unstructured.Unstructured{}
	got.SetAPIVersion("cilium.io/v2")
	got.SetKind("CiliumNetworkPolicy")

	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ichorControllerNamespace, Name: "ichor-default-deny"}, got); err != nil {
		t.Fatalf("runInit did not apply the network policies: %v", err)
	}
}
