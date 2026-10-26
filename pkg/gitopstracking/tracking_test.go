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

package gitopstracking

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	trackingTestArgoCDNamespace = "argocd"
	trackingTestAPIVersion      = "v1"
	trackingTestFieldsType      = "FieldsV1"
)

// trackingTestObject stands in for the live cluster objects callers
// actually check (a TalosMachine, a TalosConfigPatch, ...): IsTracked only
// ever reads client.Object's generic metadata, so a plain ConfigMap
// exercises the same code paths without pulling api/v1alpha1 into this
// package's own tests.
func trackingTestObject() *corev1.ConfigMap {
	return &corev1.ConfigMap{Name: "tracking-fixture", Namespace: "default"}
}

func trackingTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("adding corev1 to scheme: %v", err)
	}

	return s
}

// TestIsTracked_NeitherArgoCDNorFlux proves the default outcome -- no
// argocd-cm, no managed fields -- is "not tracked", not an error: a
// cluster running neither tool is the common case, not an edge one.
func TestIsTracked_NeitherArgoCDNorFlux(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).Build()

	tracked, by, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, trackingTestObject())
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if tracked {
		t.Fatalf("expected not tracked, got tracked by %q", by)
	}
}

// TestIsTracked_FluxKustomizeControllerManagedFieldsEntry proves the Flux
// path with the field manager Flux's kustomize-controller actually uses
// under server-side apply (fluxcd.io, "Kustomization", checked 2026-10-26)
// -- not "flux-client-side-apply", docs/adr/0044's fix.
func TestIsTracked_FluxKustomizeControllerManagedFieldsEntry(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).Build()

	obj := trackingTestObject()
	obj.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: "kustomize-controller", Operation: metav1.ManagedFieldsOperationApply,
		APIVersion: trackingTestAPIVersion, FieldsType: trackingTestFieldsType,
	}}

	tracked, by, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, obj)
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if !tracked || by != "Flux" {
		t.Fatalf("expected tracked by Flux, got tracked=%v by=%q", tracked, by)
	}
}

// TestIsTracked_FluxHelmControllerManagedFieldsEntry proves the same for
// helm-controller (a Flux HelmRelease), fluxcd.io's own helm-controller
// docs, checked 2026-10-26.
func TestIsTracked_FluxHelmControllerManagedFieldsEntry(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).Build()

	obj := trackingTestObject()
	obj.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: "helm-controller", Operation: metav1.ManagedFieldsOperationApply,
		APIVersion: trackingTestAPIVersion, FieldsType: trackingTestFieldsType,
	}}

	tracked, by, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, obj)
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if !tracked || by != "Flux" {
		t.Fatalf("expected tracked by Flux, got tracked=%v by=%q", tracked, by)
	}
}

// TestIsTracked_FluxClientSideApplyAloneIsNotTracked is docs/adr/0044's
// regression test for the bug it fixes: "flux-client-side-apply" is
// fluxcd.io's reserved field manager name for an operator's own kubectl
// edits that Flux should preserve, not a signal Flux itself applied this
// object, so it must not report tracked on its own.
func TestIsTracked_FluxClientSideApplyAloneIsNotTracked(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).Build()

	obj := trackingTestObject()
	obj.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: "flux-client-side-apply", Operation: metav1.ManagedFieldsOperationApply,
		APIVersion: trackingTestAPIVersion, FieldsType: trackingTestFieldsType,
	}}

	tracked, by, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, obj)
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if tracked {
		t.Fatalf("expected flux-client-side-apply alone to not be tracked, got tracked by %q", by)
	}
}

// TestIsTracked_ArgoCDAnnotationMethod proves the annotation-tracking
// path: argocd-cm declares resourceTrackingMethod=annotation, and the
// object carries the tracking-id annotation.
func TestIsTracked_ArgoCDAnnotationMethod(t *testing.T) {
	cm := &corev1.ConfigMap{
		Name: ArgoConfigMapName, Namespace: trackingTestArgoCDNamespace,
		Data: map[string]string{ResourceTrackingMethodKey: "annotation"},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).WithObjects(cm).Build()

	obj := trackingTestObject()
	obj.Annotations = map[string]string{ArgoTrackingAnnotation: "argocd:Application:argocd/root"}

	tracked, by, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, obj)
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
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
		Name: ArgoConfigMapName, Namespace: trackingTestArgoCDNamespace,
		Data: map[string]string{
			ResourceTrackingMethodKey:   "label",
			ApplicationInstanceLabelKey: "custom.example.com/instance",
		},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).WithObjects(cm).Build()

	obj := trackingTestObject()
	obj.Labels = map[string]string{"custom.example.com/instance": "root"}

	tracked, by, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, obj)
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if !tracked || by != "Argo CD" {
		t.Fatalf("expected tracked by Argo CD via the custom label key, got tracked=%v by=%q", tracked, by)
	}

	// The default label key must NOT also be checked once a custom key is
	// configured -- that would be a false positive for any object that
	// merely carries Kubernetes' own common instance label.
	obj2 := trackingTestObject()
	obj2.Labels = map[string]string{ArgoDefaultInstanceLabelKey: "root"}

	tracked2, _, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, obj2)
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if tracked2 {
		t.Fatal("expected the default instance label to NOT match once argocd-cm sets a custom key")
	}
}

// TestIsTracked_ArgoCDConfigMapMissingIsNotTracked proves a missing
// argocd-cm (Argo CD not installed) is treated as "not tracked", not an
// error.
func TestIsTracked_ArgoCDConfigMapMissingIsNotTracked(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).Build()

	obj := trackingTestObject()
	obj.Annotations = map[string]string{ArgoTrackingAnnotation: "argocd:Application:argocd/root"}

	tracked, _, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, obj)
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if tracked {
		t.Fatal("expected not tracked when argocd-cm does not exist, got tracked")
	}
}
