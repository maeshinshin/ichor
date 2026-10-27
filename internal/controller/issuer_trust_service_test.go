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
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// A kustomize install prefixes the Service name; the controller finds it by label.
const otherSvc = "other"

func TestUIService_ByLabelThenByNameOverride(t *testing.T) {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		&corev1.Service{Namespace: "ns", Name: "ichor-ichor-ui-service", Labels: map[string]string{uiComponentLabel: uiComponentValue}},
		&corev1.Service{Namespace: "ns", Name: otherSvc},
	).Build()

	r := &IssuerTrustReconciler{Client: c, Namespace: "ns"}

	svc, err := r.uiService(context.Background())
	if err != nil || svc.Name != "ichor-ichor-ui-service" {
		t.Fatalf("by label: %v %v", svc, err)
	}

	r.UIServiceName = otherSvc

	if svc, err = r.uiService(context.Background()); err != nil || svc.Name != otherSvc {
		t.Fatalf("by name: %v %v", svc, err)
	}

	empty := &IssuerTrustReconciler{Client: fake.NewClientBuilder().WithScheme(s).Build(), Namespace: "ns"}
	if _, err := empty.uiService(context.Background()); !apierrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}
