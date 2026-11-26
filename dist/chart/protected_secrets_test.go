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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// TestProtectedSecretsCannotBeDeletedWithoutConsent guards: with the chart's rendered policy installed, a
// kube-apiserver refuses DELETE of a Secret labelled deleteguard.ProtectedLabel unless it carries
// the allow-delete annotation, and always admits an unlabelled Secret (the control that shows the refusal is
// the label's).
func TestProtectedSecretsCannotBeDeletedWithoutConsent(t *testing.T) {
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
		kindValidatingAdmissionPolicy:     "ichor-protected-secrets",
		kindValidatingAdmissionPolicyBind: "ichor-protected-secrets-binding",
	} {
		obj := find(docs, kind, n)
		if obj == nil {
			t.Fatalf("the chart renders no %s %s", kind, n)
		}

		if err := admin.Create(ctx, &unstructured.Unstructured{Object: obj}); err != nil {
			t.Fatalf("creating %s %s: %v", kind, n, err)
		}
	}

	secret := func(name string, labels, annotations map[string]string) *corev1.Secret {
		return &corev1.Secret{
			Name: name, Namespace: kDefault, Labels: labels, Annotations: annotations,
			Data: map[string][]byte{"k": []byte("v")},
		}
	}
	protected := map[string]string{deleteguard.ProtectedLabel: deleteguard.ProtectedValue}
	consent := map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}

	// Policies take a moment to compile: wait until a labelled Secret is refused.
	deadline := time.Now().Add(30 * time.Second)
	for n := 0; ; n++ {
		probe := secret("probe", protected, nil)
		if err := admin.Create(ctx, probe); err != nil {
			t.Fatal(err)
		}

		err := admin.Delete(ctx, probe)
		if isPolicyDenial(err, "ichor-protected-secrets") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("the policy never refused a labelled Secret: delete returned %v", err)
		}

		_ = admin.Delete(ctx, probe) // an unprotected probe went through; try again
		time.Sleep(500 * time.Millisecond)
	}

	labelled := secret("bundle", protected, nil)
	if err := admin.Create(ctx, labelled); err != nil {
		t.Fatal(err)
	}

	if err := admin.Delete(ctx, labelled); !isPolicyDenial(err, "allow-delete") {
		t.Errorf("deleting a labelled Secret without the annotation: got %v, want the policy's refusal", err)
	}

	// The namespace controller removes a namespace's Secrets with DeleteCollection.
	err = admin.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace(kDefault))
	if !isPolicyDenial(err, "allow-delete") {
		t.Errorf("deleting the namespace's Secrets in one call: got %v, want the policy's refusal", err)
	}

	if err := admin.Get(ctx, client.ObjectKeyFromObject(labelled), &corev1.Secret{}); err != nil {
		t.Errorf("the refused Secret is gone: %v", err)
	}

	plain := secret("plain", nil, nil)
	if err := admin.Create(ctx, plain); err != nil {
		t.Fatal(err)
	}

	if err := admin.Delete(ctx, plain); err != nil {
		t.Errorf("deleting an unlabelled Secret: %v", err)
	}

	consented := secret("consented", protected, consent)
	if err := admin.Create(ctx, consented); err != nil {
		t.Fatal(err)
	}

	if err := admin.Delete(ctx, consented); err != nil {
		t.Errorf("deleting a labelled Secret with the annotation: %v", err)
	}

	labelled.Annotations = consent
	if err := admin.Update(ctx, labelled); err != nil {
		t.Fatal(err)
	}

	if err := admin.Delete(ctx, labelled); err != nil {
		t.Errorf("deleting the labelled Secret after annotating it: %v", err)
	}
}
