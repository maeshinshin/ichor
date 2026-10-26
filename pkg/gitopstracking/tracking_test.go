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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

	if !tracked || by != TrackedByFlux {
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

	if !tracked || by != TrackedByFlux {
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

	if !tracked || by != TrackedByArgoCD {
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

	if !tracked || by != TrackedByArgoCD {
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

// fluxKustomization builds an unstructured Flux Kustomization whose
// .status.inventory lists ids, the shape kustomize-controller writes.
func fluxKustomization(ids ...string) *unstructured.Unstructured {
	entries := make([]any, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, map[string]any{"id": id, "v": "v1"})
	}

	k := &unstructured.Unstructured{}
	k.SetGroupVersionKind(schema.GroupVersionKind{
		Group: fluxKustomizeGroup, Version: fluxKustomizeVersion, Kind: "Kustomization",
	})
	k.SetName("apps")
	k.SetNamespace("flux-system")

	_ = unstructured.SetNestedSlice(k.Object, entries, "status", "inventory", "entries")

	return k
}

// TestIsTracked_FluxInventoryHitWithoutFieldManager is the case the cheap
// signal misses: no managedFields entry at all, yet a Kustomization's
// inventory owns the object (docs/adr/0044).
func TestIsTracked_FluxInventoryHitWithoutFieldManager(t *testing.T) {
	// a ConfigMap is core/ConfigMap, so its id has an empty group.
	k := fluxKustomization("default_tracking-fixture__ConfigMap")
	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).WithObjects(k).Build()

	tracked, by, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, trackingTestObject())
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if !tracked || by != TrackedByFlux {
		t.Fatalf("expected tracked by Flux via inventory, got tracked=%v by=%q", tracked, by)
	}
}

// TestIsTracked_FluxInventoryMiss proves an inventory that lists other
// objects does not mark this one tracked.
func TestIsTracked_FluxInventoryMiss(t *testing.T) {
	k := fluxKustomization("default_someone-else__ConfigMap")
	k8sClient := fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).WithObjects(k).Build()

	tracked, by, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, trackingTestObject())
	if err != nil {
		t.Fatalf("IsTracked: %v", err)
	}

	if tracked {
		t.Fatalf("expected not tracked, got tracked by %q", by)
	}
}

// listFails returns a client whose every list fails with err.
func listFails(t *testing.T, err error) client.Client {
	t.Helper()

	return interceptor.NewClient(fake.NewClientBuilder().WithScheme(trackingTestScheme(t)).Build(), interceptor.Funcs{
		List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
			return err
		},
	})
}

// TestIsTracked_FluxNotInstalledIsNotTracked proves a missing Flux CRD (the
// RESTMapper's no-match error, or a not-found) is "not tracked", not an
// error -- the same discipline as a missing argocd-cm.
func TestIsTracked_FluxNotInstalledIsNotTracked(t *testing.T) {
	gk := schema.GroupKind{Group: fluxKustomizeGroup, Kind: "Kustomization"}
	gr := schema.GroupResource{Group: fluxKustomizeGroup, Resource: "kustomizations"}

	for name, listErr := range map[string]error{
		"no match":  &meta.NoKindMatchError{GroupKind: gk},
		"not found": apierrors.NewNotFound(gr, ""),
	} {
		t.Run(name, func(t *testing.T) {
			k8sClient := listFails(t, listErr)

			tracked, _, err := IsTracked(context.Background(), k8sClient, trackingTestArgoCDNamespace, trackingTestObject())
			if err != nil || tracked {
				t.Fatalf("expected (not tracked, no error), got tracked=%v err=%v", tracked, err)
			}
		})
	}
}

// TestIsTracked_FluxListFailureIsAnError proves any other list failure is
// surfaced, not silently read as "not tracked": a write gate must not guess.
func TestIsTracked_FluxListFailureIsAnError(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{}, "", nil)

	c := listFails(t, forbidden)
	_, _, err := IsTracked(context.Background(), c, trackingTestArgoCDNamespace, trackingTestObject())
	if err == nil {
		t.Fatal("expected an error when listing Kustomizations is forbidden")
	}
}
