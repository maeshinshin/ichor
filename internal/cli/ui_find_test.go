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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestFindUIService_ByLabelNotName proves a kustomize-installed Service
// (namePrefix makes it ichor-ichor-ui-service) is found through its component
// label, and an unlabelled look-alike is not.
func TestFindUIService_ByLabelNotName(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(
		&corev1.Service{Name: "ichor-ichor-ui-service", Namespace: "ichor-system", Labels: map[string]string{uiComponentLabel: uiComponentValue}},
		&corev1.Service{Name: "ichor-ui-service", Namespace: "other"},
	).Build()

	svc, err := findUIService(context.Background(), c, "ichor-system")
	if err != nil {
		t.Fatal(err)
	}

	if svc.Name != "ichor-ichor-ui-service" {
		t.Fatalf("found %s", svc.Name)
	}

	if _, err := findUIService(context.Background(), c, "other"); err == nil {
		t.Fatal("expected an error for an unlabelled Service")
	}
}
