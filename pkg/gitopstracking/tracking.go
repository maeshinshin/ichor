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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// FluxKustomizeControllerFieldManager and FluxHelmControllerFieldManager
// are the field managers Flux's own controllers register under
// managedFields when they apply a resource with server-side apply (the
// default since Flux v2; checked against fluxcd.io's current
// kustomize-controller and helm-controller docs, 2026-10-26): one for a
// Kustomization's applies, one for a HelmRelease's.
//
// "flux-client-side-apply" -- what this package checked before
// docs/adr/0044 -- is NOT one of these: fluxcd.io's own FAQ documents it as
// the field manager name an operator must use on their *own* `kubectl`
// edits for Flux to preserve them across reconciliation, the opposite of a
// signal that Flux itself owns the resource. Checking for it here matched
// client-side-apply-era Flux (pre server-side-apply default) at best, and
// a resource a human hand-edited under that reserved name at worst -- never
// evidence of current Flux ownership. See docs/adr/0044 for the sources.
const (
	FluxKustomizeControllerFieldManager = "kustomize-controller"
	FluxHelmControllerFieldManager      = "helm-controller"
)

var fluxFieldManagers = []string{FluxKustomizeControllerFieldManager, FluxHelmControllerFieldManager}

// IsTracked reports whether obj is already tracked by Argo CD or Flux, so
// a caller can either warn (gitops export) or refuse to write it directly
// (pkg/prmode.Decide, docs/adr/0044) about one of the kinds
// architecture.md's "GitOps and Argo CD" section says must never be in
// Git (a Secret, a TalosOperation, an unapproved TalosMachine, Ichor's own
// runtime labels/annotations) showing up there anyway. It never errors
// just because Argo CD or Flux isn't installed -- a missing argocd-cm
// ConfigMap, or no managed field at all, both mean "not tracked", not
// "unknown".
//
// Argo CD: read argocd-cm's own resourceTrackingMethod and
// instanceLabelKey rather than assuming either -- a resource marked under
// one method is not detected under another. If the ConfigMap exists but
// does not set resourceTrackingMethod, both the annotation and the label
// are checked: an ambiguous default is treated as "check both" rather than
// guessed at, so a missed true positive never costs more than a redundant
// check.
//
// Flux: it marks nothing permanent on the resource's intent -- ownership
// lives in the owning Kustomization's or HelmRelease's .status.inventory
// -- but every object a Flux controller applies also carries a
// managedFields entry naming that controller (fluxFieldManagers above),
// which is one field read on the object already in hand instead of
// listing every Kustomization/HelmRelease and searching its inventory for
// this one entry. docs/adr/0044 records why this lookup, not an inventory
// scan, is enough even though this function now gates a write rather than
// only a warning.
func IsTracked(
	ctx context.Context, k8sClient client.Client, argoCDNamespace string, obj client.Object,
) (tracked bool, by string, err error) {
	for _, mf := range obj.GetManagedFields() {
		if slices.Contains(fluxFieldManagers, mf.Manager) {
			return true, "Flux", nil
		}
	}

	var cm corev1.ConfigMap

	err = k8sClient.Get(ctx, types.NamespacedName{Namespace: argoCDNamespace, Name: ArgoConfigMapName}, &cm)
	if apierrors.IsNotFound(err) {
		return false, "", nil
	}

	if err != nil {
		return false, "", fmt.Errorf("reading %s/%s: %w", argoCDNamespace, ArgoConfigMapName, err)
	}

	method := cm.Data[ResourceTrackingMethodKey]

	checkAnnotation := method == "" || method == "annotation" || method == "annotation+label"
	checkLabel := method == "" || method == "label" || method == "annotation+label"

	if checkAnnotation {
		if _, ok := obj.GetAnnotations()[ArgoTrackingAnnotation]; ok {
			return true, "Argo CD", nil
		}
	}

	if checkLabel {
		labelKey := cm.Data[ApplicationInstanceLabelKey]
		if labelKey == "" {
			labelKey = ArgoDefaultInstanceLabelKey
		}

		if _, ok := obj.GetLabels()[labelKey]; ok {
			return true, "Argo CD", nil
		}
	}

	return false, "", nil
}
