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

package chart

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// TestProtectedNamespaceCannotBeDeletedWithoutConsent guards: with the chart's rendered policy installed, a
// kube-apiserver refuses DELETE of a Namespace labelled deleteguard.ProtectedLabel unless it carries the
// allow-delete annotation, and admits an unlabelled one. envtest runs no namespace controller, so this covers
// admission only.
func TestProtectedNamespaceCannotBeDeletedWithoutConsent(t *testing.T) {
	ctx := context.Background()
	env := &envtest.Environment{}

	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("starting envtest: %v", err)
	}

	t.Cleanup(func() { _ = env.Stop() })

	admin, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatal(err)
	}

	docs := renderChart(t, "ichor")
	for kind, n := range map[string]string{
		kindValidatingAdmissionPolicy:     "ichor-protected-namespace",
		kindValidatingAdmissionPolicyBind: "ichor-protected-namespace-binding",
	} {
		obj := find(docs, kind, n)
		if obj == nil {
			t.Fatalf("the chart renders no %s %s", kind, n)
		}

		if err := admin.Create(ctx, &unstructured.Unstructured{Object: obj}); err != nil {
			t.Fatalf("creating %s %s: %v", kind, n, err)
		}
	}

	namespace := func(name string, labels, annotations map[string]string) *corev1.Namespace {
		return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels, Annotations: annotations}}
	}
	protected := map[string]string{deleteguard.ProtectedLabel: deleteguard.ProtectedValue}
	consent := map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}

	// Policies take a moment to compile: wait until a labelled Namespace is refused.
	deadline := time.Now().Add(30 * time.Second)
	for n := 0; ; n++ {
		probe := namespace(fmt.Sprintf("probe-%d", n), protected, nil) // a probe that went through stays Terminating
		if err := admin.Create(ctx, probe); err != nil {
			t.Fatal(err)
		}

		err := admin.Delete(ctx, probe)
		if isPolicyDenial(err, "ichor-protected-namespace") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("the policy never refused a labelled Namespace: delete returned %v", err)
		}

		time.Sleep(500 * time.Millisecond)
	}

	labelled := namespace("holds-ichor", protected, nil)
	if err := admin.Create(ctx, labelled); err != nil {
		t.Fatal(err)
	}

	if err := admin.Delete(ctx, labelled); !isPolicyDenial(err, "allow-delete") {
		t.Errorf("deleting a labelled Namespace without the annotation: got %v, want the policy's refusal", err)
	}

	if err := admin.Get(ctx, client.ObjectKeyFromObject(labelled), &corev1.Namespace{}); err != nil {
		t.Errorf("the refused Namespace is gone: %v", err)
	}

	plain := namespace("plain", nil, nil)
	if err := admin.Create(ctx, plain); err != nil {
		t.Fatal(err)
	}

	if err := admin.Delete(ctx, plain); err != nil {
		t.Errorf("deleting an unlabelled Namespace: %v", err)
	}

	consented := namespace("consented", protected, consent)
	if err := admin.Create(ctx, consented); err != nil {
		t.Fatal(err)
	}

	if err := admin.Delete(ctx, consented); err != nil {
		t.Errorf("deleting a labelled Namespace with the annotation: %v", err)
	}

	labelled.Annotations = consent
	if err := admin.Update(ctx, labelled); err != nil {
		t.Fatal(err)
	}

	if err := admin.Delete(ctx, labelled); err != nil {
		t.Errorf("deleting the labelled Namespace after annotating it: %v", err)
	}
}

// TestProtectedNamespacePolicyIsGatedLikeProtectedSecrets guards: the Namespace policy and its binding render
// exactly when the Secret policy does (admission.enabled), and the chart's manager role may get and patch only
// the release namespace.
func TestProtectedNamespacePolicyIsGatedLikeProtectedSecrets(t *testing.T) {
	names := map[string]string{
		"ichor-protected-secrets":   "ichor-protected-secrets-binding",
		"ichor-protected-namespace": "ichor-protected-namespace-binding",
	}

	for _, enabled := range []bool{true, false} {
		docs := renderChartWithValues(t, "ichor", fmt.Sprintf("admission.enabled=%t", enabled))
		for policy, binding := range names {
			if got := find(docs, kindValidatingAdmissionPolicy, policy) != nil; got != enabled {
				t.Errorf("admission.enabled=%t: policy %s rendered = %t", enabled, policy, got)
			}

			if got := find(docs, kindValidatingAdmissionPolicyBind, binding) != nil; got != enabled {
				t.Errorf("admission.enabled=%t: binding %s rendered = %t", enabled, binding, got)
			}
		}
	}

	role := findNameSuffix(renderChart(t, "ichor"), kindClusterRole, "-manager-role")
	for _, verb := range []string{"get", "patch"} {
		if !rbacTriples(allRules(role))[rbacTriple{"", "namespaces", verb, kDefault}] {
			t.Errorf("the manager role lacks %s on the namespace %s alone", verb, kDefault)
		}
	}
}
