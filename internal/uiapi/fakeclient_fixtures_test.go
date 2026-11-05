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
	"testing"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Fixtures for the plain Go tests over a fake client (no envtest).

const (
	testNS       = "default"
	testEndpoint = "10.0.0.10:50000"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}

	if err := ichorv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}

	return s
}

func testMachine() *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Name: "m1", Namespace: testNS,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: "c"},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "u", Serial: "s", MACAddresses: []string{"aa:bb:cc:dd:ee:ff"}},
			Endpoint:      testEndpoint,
			ApprovalState: ichorv1alpha1.MachineApprovalStatePending,
		},
	}
}

const bgUser = "oidc:alice"

func asUser(ctx context.Context) context.Context {
	return ContextWithIdentity(ctx, Identity{User: testUserBob})
}

// reviewing wraps c so a SelfSubjectAccessReview is answered with allowed
// and recorded in asked; everything else goes through. It stands in for RBAC
// in the override tests.
func reviewing(c client.Client, allowed bool, asked *[]authv1.ResourceAttributes) client.Client {
	return interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if sar, ok := obj.(*authv1.SelfSubjectAccessReview); ok {
				*asked = append(*asked, *sar.Spec.ResourceAttributes)
				sar.Status.Allowed = allowed

				return nil
			}

			return c.Create(ctx, obj, opts...)
		},
	})
}
