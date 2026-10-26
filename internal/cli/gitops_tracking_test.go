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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const trackingTestArgoCDNamespace = "argocd"

func trackingTestMachine() *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Name: "tracking-fixture-machine", Namespace: testNamespace,
	}
}

// TestIsTracked_NeitherArgoCDNorFlux proves the default outcome -- no
// argocd-cm, no managed fields -- is "not tracked", not an error: a
// cluster running neither tool is the common case, not an edge one.
func TestIsTracked_NeitherArgoCDNorFlux(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	tracked, by, err := isTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, trackingTestMachine())
	if err != nil {
		t.Fatalf("isTracked: %v", err)
	}

	if tracked {
		t.Fatalf("expected not tracked, got tracked by %q", by)
	}
}

// TestIsTracked_FluxManagedFieldsEntry proves the Flux fast path: a
// managedFields entry naming Flux's own field manager (fluxFieldManager,
// kustomize-controller, Flux's default and only a first signal) is enough on
// its own, with no argocd-cm involved at all. The reserved name a user's own
// kubectl edits use is deliberately not Flux's applying name.
func TestIsTracked_FluxManagedFieldsEntry(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	m := trackingTestMachine()
	m.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: fluxFieldManager, Operation: metav1.ManagedFieldsOperationApply,
		APIVersion: testAPIVersion, FieldsType: "FieldsV1",
	}}

	tracked, by, err := isTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, m)
	if err != nil {
		t.Fatalf("isTracked: %v", err)
	}

	if !tracked || by != "Flux" {
		t.Fatalf("expected tracked by Flux, got tracked=%v by=%q", tracked, by)
	}
}

// TestIsTracked_ArgoCDAnnotationMethod proves the annotation-tracking
// path: argocd-cm declares resourceTrackingMethod=annotation, and the
// object carries the tracking-id annotation.
func TestIsTracked_ArgoCDAnnotationMethod(t *testing.T) {
	cm := &corev1.ConfigMap{
		Name: argoConfigMapName, Namespace: trackingTestArgoCDNamespace,
		Data: map[string]string{resourceTrackingMethodKey: "annotation"},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cm).Build()

	m := trackingTestMachine()
	m.Annotations = map[string]string{argoTrackingAnnotation: "argocd:Application:argocd/root"}

	tracked, by, err := isTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, m)
	if err != nil {
		t.Fatalf("isTracked: %v", err)
	}

	if !tracked || by != "Argo CD" {
		t.Fatalf("expected tracked by Argo CD, got tracked=%v by=%q", tracked, by)
	}
}

// TestIsTracked_ArgoCDLabelMethodCustomKey proves the label-tracking path
// honors argocd-cm's own instanceLabelKey rather than assuming the
// default: the default's app.kubernetes.io/instance label is absent, and
// yet this is still detected tracked via the custom key.
func TestIsTracked_ArgoCDLabelMethodCustomKey(t *testing.T) {
	cm := &corev1.ConfigMap{
		Name: argoConfigMapName, Namespace: trackingTestArgoCDNamespace,
		Data: map[string]string{
			resourceTrackingMethodKey:   "label",
			applicationInstanceLabelKey: "custom.example.com/instance",
		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cm).Build()

	m := trackingTestMachine()
	m.Labels = map[string]string{"custom.example.com/instance": "root"}

	tracked, by, err := isTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, m)
	if err != nil {
		t.Fatalf("isTracked: %v", err)
	}

	if !tracked || by != "Argo CD" {
		t.Fatalf("expected tracked by Argo CD via the custom label key, got tracked=%v by=%q", tracked, by)
	}

	// The default label key must NOT also be checked once a custom key is
	// configured -- that would be a false positive for any object that
	// merely carries Kubernetes' own common instance label.
	m2 := trackingTestMachine()
	m2.Labels = map[string]string{argoDefaultInstanceLabelKey: "root"}

	tracked2, _, err := isTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, m2)
	if err != nil {
		t.Fatalf("isTracked: %v", err)
	}

	if tracked2 {
		t.Fatal("expected the default instance label to NOT match once argocd-cm sets a custom key")
	}
}

// TestIsTracked_ArgoCDConfigMapMissingIsNotTracked proves a missing
// argocd-cm (Argo CD not installed) is treated as "not tracked", not an
// error.
func TestIsTracked_ArgoCDConfigMapMissingIsNotTracked(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	m := trackingTestMachine()
	m.Annotations = map[string]string{argoTrackingAnnotation: "argocd:Application:argocd/root"}

	tracked, _, err := isTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, m)
	if err != nil {
		t.Fatalf("isTracked: %v", err)
	}

	if tracked {
		t.Fatal("expected not tracked when argocd-cm does not exist, got tracked")
	}
}
