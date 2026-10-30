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
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// An object an older `ichor init` created (field manager "ichor", operation Update) owns its list
// items, which an apply as FieldOwner cannot remove. applyOne hands them over first, so the
// plaintext ichor-ui Service port goes away and an operator's own port stays (ADR 0105).
func TestApplyOneRemovesAPortOwnedByAnOlderInit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	port := func(name string, p int32) corev1.ServicePort {
		return corev1.ServicePort{Name: name, Port: p, TargetPort: intstr.FromInt32(p), Protocol: corev1.ProtocolTCP}
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ichor-ichor-ui-service", Namespace: f.ns}, //nolint:modernize
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{port("http", 8080)}, Selector: map[string]string{"a": "b"}},
	}

	if err := k8sClient.Create(ctx, svc, client.FieldOwner("ichor")); err != nil {
		t.Fatalf("creating the Service as init: %v", err)
	}

	// An operator's own edit, by another manager.
	svc.Spec.Ports = append(svc.Spec.Ports, port("extra", 9999))
	if err := k8sClient.Update(ctx, svc, client.FieldOwner("kubectl-edit")); err != nil {
		t.Fatalf("adding the operator's port: %v", err)
	}

	want := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{kName: svc.Name, "namespace": f.ns},
		kSpec: map[string]any{
			"selector": map[string]any{"a": "b"},
			"ports":    []any{map[string]any{"name": "https", "port": int64(8443), "targetPort": int64(8443), "protocol": "TCP"}},
		},
	}}

	if err := applyOne(ctx, k8sClient, want); err != nil {
		t.Fatalf("applyOne: %v", err)
	}

	got := &corev1.Service{}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(svc), got); err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(got.Spec.Ports))
	for _, p := range got.Spec.Ports {
		names = append(names, p.Name)
	}

	slices.Sort(names)

	if strings.Join(names, ",") != "extra,https" {
		t.Errorf("ports = %v, want [extra https]", names)
	}
}
