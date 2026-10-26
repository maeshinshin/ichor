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
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// ArgoTrackingAnnotation and ArgoDefaultInstanceLabelKey are Argo CD's own
// defaults (argocd-cm's application.resourceTrackingMethod and
// application.instanceLabelKey, read from that ConfigMap rather than
// guessed -- architecture.md's "GitOps and Argo CD": "the default has
// moved between releases").
const (
	ArgoTrackingAnnotation      = "argocd.argoproj.io/tracking-id"
	ArgoDefaultInstanceLabelKey = "app.kubernetes.io/instance"
	ArgoConfigMapName           = "argocd-cm"
	ResourceTrackingMethodKey   = "application.resourceTrackingMethod"
	ApplicationInstanceLabelKey = "application.instanceLabelKey"
)

// TrackedBy values IsTracked returns (and prmode.Decide consumes).
const (
	TrackedByFlux   = "Flux"
	TrackedByArgoCD = "Argo CD"
)

// FluxKustomizeControllerFieldManager and FluxHelmControllerFieldManager are
// the field managers Flux's controllers register in managedFields when they
// server-side-apply a resource. They are only a fast FIRST signal, never
// proof of absence: a miss falls through to the authoritative inventory scan
// (fluxTrackedByInventory), because the name is not something Ichor may
// assume -- the same reason Argo CD's tracking method is read from argocd-cm
// instead of guessed.
//
// "flux-client-side-apply" -- what this package checked before -- is NOT a Flux-applied signal: fluxcd.io's FAQ
// documents it
// as the field manager name an operator must use on their *own* `kubectl`
// edits for Flux to preserve them, the opposite of "Flux owns this".
const (
	FluxKustomizeControllerFieldManager = "kustomize-controller"
	FluxHelmControllerFieldManager      = "helm-controller"
)

var fluxFieldManagers = []string{FluxKustomizeControllerFieldManager, FluxHelmControllerFieldManager}

// Flux's Kustomization is read as unstructured so Ichor takes no Flux Go
// module dependency. Inventory entry ids are "<namespace>_<name>_<group>_<kind>"
// (kustomize-controller api/v1 ResourceRef doc comment, checked 2026-10-26;
// an empty group, as for core kinds, leaves an empty field), at
// .status.inventory.entries[].id. The version is a separate "v" field.
const (
	fluxKustomizeGroup       = "kustomize.toolkit.fluxcd.io"
	fluxKustomizeVersion     = "v1"
	fluxKustomizationList    = "KustomizationList"
	fluxInventoryIDSeparator = "_"
)

// fluxTrackedByInventory is the authoritative Flux check: list every
// Kustomization and search .status.inventory for obj. A cluster without
// Flux's CRD (no-match / not-found) is "not tracked", not an error.
func fluxTrackedByInventory(ctx context.Context, k8sClient client.Client, obj client.Object) (bool, error) {
	gvk, err := apiutil.GVKForObject(obj, k8sClient.Scheme())
	if err != nil {
		return false, fmt.Errorf("resolving the kind of %T: %w", obj, err)
	}

	id := strings.Join([]string{obj.GetNamespace(), obj.GetName(), gvk.Group, gvk.Kind}, fluxInventoryIDSeparator)

	var list unstructured.UnstructuredList

	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group: fluxKustomizeGroup, Version: fluxKustomizeVersion, Kind: fluxKustomizationList,
	})

	if err := k8sClient.List(ctx, &list); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return false, nil
		}

		return false, fmt.Errorf("listing Flux Kustomizations: %w", err)
	}

	for i := range list.Items {
		entries, _, _ := unstructured.NestedSlice(list.Items[i].Object, "status", "inventory", "entries")
		for _, e := range entries {
			if m, ok := e.(map[string]any); ok && m["id"] == id {
				return true, nil
			}
		}
	}

	return false, nil
}

// argoTracked reports whether obj carries Argo CD's marker under the
// tracking method cm declares. If cm does not set resourceTrackingMethod,
// both the annotation and the label are checked: an ambiguous default is
// treated as "check both" rather than guessed at.
func argoTracked(cm *corev1.ConfigMap, obj client.Object) bool {
	method := cm.Data[ResourceTrackingMethodKey]

	checkAnnotation := method == "" || method == "annotation" || method == "annotation+label"
	checkLabel := method == "" || method == "label" || method == "annotation+label"

	if checkAnnotation {
		if _, ok := obj.GetAnnotations()[ArgoTrackingAnnotation]; ok {
			return true
		}
	}

	if checkLabel {
		labelKey := cm.Data[ApplicationInstanceLabelKey]
		if labelKey == "" {
			labelKey = ArgoDefaultInstanceLabelKey
		}

		if _, ok := obj.GetLabels()[labelKey]; ok {
			return true
		}
	}

	return false
}

// IsTracked reports whether obj is already tracked by Argo CD or Flux, so a
// caller can warn (gitops export) or refuse to write it directly
// (pkg/prmode.Decide). It never errors just because Argo CD or
// Flux isn't installed -- a missing argocd-cm, or a missing Flux CRD, means
// "not tracked", not "unknown". One function serves both the warning and the
// write gate on purpose.
//
// Order, cheapest first: a Flux field manager on the object (a hit is
// enough); Argo CD via argocd-cm's own resourceTrackingMethod/
// instanceLabelKey; finally the Flux inventory scan, the authoritative
// owner-side answer, reached whenever the first two find nothing.
func IsTracked(
	ctx context.Context, k8sClient client.Client, argoCDNamespace string, obj client.Object,
) (tracked bool, by string, err error) {
	for _, mf := range obj.GetManagedFields() {
		if slices.Contains(fluxFieldManagers, mf.Manager) {
			return true, TrackedByFlux, nil
		}
	}

	var cm corev1.ConfigMap

	err = k8sClient.Get(ctx, types.NamespacedName{Namespace: argoCDNamespace, Name: ArgoConfigMapName}, &cm)

	switch {
	case err == nil:
		if argoTracked(&cm, obj) {
			return true, TrackedByArgoCD, nil
		}
	case !apierrors.IsNotFound(err):
		return false, "", fmt.Errorf("reading %s/%s: %w", argoCDNamespace, ArgoConfigMapName, err)
	}

	byInventory, err := fluxTrackedByInventory(ctx, k8sClient, obj)
	if err != nil {
		return false, "", err
	}

	if byInventory {
		return true, TrackedByFlux, nil
	}

	return false, "", nil
}

// ArgoApplicationOf names the Application that tracks obj, so PR mode can
// read its source. The annotation value is
// "<app>:<group>/<kind>:<namespace>/<name>", with "<appNamespace>_<app>" for
// an Application outside argoCDNamespace; the label value is the bare app
// name. Honoring the method argocd-cm declares is the same rule IsTracked
// uses, so a CR IsTracked reported is found here too. An error means "cannot
// say which Application", and the caller must not write.
func ArgoApplicationOf(
	ctx context.Context, k8sClient client.Client, argoCDNamespace string, obj client.Object,
) (types.NamespacedName, error) {
	var cm corev1.ConfigMap

	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: argoCDNamespace, Name: ArgoConfigMapName}, &cm); err != nil {
		return types.NamespacedName{}, fmt.Errorf("reading %s/%s: %w", argoCDNamespace, ArgoConfigMapName, err)
	}

	method := cm.Data[ResourceTrackingMethodKey]

	if method == "" || method == "annotation" || method == "annotation+label" {
		if v, ok := obj.GetAnnotations()[ArgoTrackingAnnotation]; ok {
			app, _, _ := strings.Cut(v, ":")

			return qualifiedApp(argoCDNamespace, app)
		}
	}

	if method == "" || method == "label" || method == "annotation+label" {
		labelKey := cm.Data[ApplicationInstanceLabelKey]
		if labelKey == "" {
			labelKey = ArgoDefaultInstanceLabelKey
		}

		if v, ok := obj.GetLabels()[labelKey]; ok {
			return qualifiedApp(argoCDNamespace, v)
		}
	}

	return types.NamespacedName{}, fmt.Errorf("%s carries no Argo CD tracking marker", client.ObjectKeyFromObject(obj))
}

func qualifiedApp(argoCDNamespace, app string) (types.NamespacedName, error) {
	ns, name := argoCDNamespace, app
	if before, after, ok := strings.Cut(app, "_"); ok {
		ns, name = before, after
	}

	if ns == "" || name == "" {
		return types.NamespacedName{}, fmt.Errorf("the Argo CD tracking marker names no Application (%q)", app)
	}

	return types.NamespacedName{Namespace: ns, Name: name}, nil
}
