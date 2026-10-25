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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// argoTrackingAnnotation and argoDefaultInstanceLabelKey are Argo CD's own
// defaults (argocd-cm's application.resourceTrackingMethod and
// application.instanceLabelKey, read from that ConfigMap rather than
// guessed -- architecture.md's "GitOps and Argo CD": "the default has
// moved between releases").
const (
	argoTrackingAnnotation      = "argocd.argoproj.io/tracking-id"
	argoDefaultInstanceLabelKey = "app.kubernetes.io/instance"
	argoConfigMapName           = "argocd-cm"
	fluxFieldManager            = "flux-client-side-apply"
	resourceTrackingMethodKey   = "application.resourceTrackingMethod"
	applicationInstanceLabelKey = "application.instanceLabelKey"
)

// isTracked reports whether obj is already tracked by Argo CD or Flux, so
// a caller can warn about one of the kinds architecture.md's "GitOps and
// Argo CD" section says must never be in Git (a Secret, a TalosOperation,
// an unapproved TalosMachine, Ichor's own runtime labels/annotations)
// showing up there anyway. It never errors just because Argo CD or Flux
// isn't installed -- a missing argocd-cm ConfigMap, or no managed field
// at all, both mean "not tracked", not "unknown".
//
// Argo CD: read argocd-cm's own resourceTrackingMethod and
// instanceLabelKey rather than assuming either -- a resource marked under
// one method is not detected under another. If the ConfigMap exists but
// does not set resourceTrackingMethod, both the annotation and the label
// are checked: this function only ever feeds a warning, so treating an
// ambiguous default as "check both" costs nothing and a missed true
// positive would.
//
// Flux: it marks nothing on the resource itself -- ownership lives in the
// owning Kustomization's .status.inventory -- but every object Flux
// applies also carries a managedFields entry for the
// "flux-client-side-apply" field manager (architecture.md's own
// reason for naming it), which is one field read on the object already
// in hand instead of listing every Kustomization and searching its
// inventory for this one entry.
func isTracked(ctx context.Context, k8sClient client.Client, argoCDNamespace string, obj client.Object) (tracked bool, by string, err error) {
	for _, mf := range obj.GetManagedFields() {
		if mf.Manager == fluxFieldManager {
			return true, "Flux", nil
		}
	}

	var cm corev1.ConfigMap

	err = k8sClient.Get(ctx, types.NamespacedName{Namespace: argoCDNamespace, Name: argoConfigMapName}, &cm)
	if apierrors.IsNotFound(err) {
		return false, "", nil
	}

	if err != nil {
		return false, "", fmt.Errorf("reading %s/%s: %w", argoCDNamespace, argoConfigMapName, err)
	}

	method := cm.Data[resourceTrackingMethodKey]

	checkAnnotation := method == "" || method == "annotation" || method == "annotation+label"
	checkLabel := method == "" || method == "label" || method == "annotation+label"

	if checkAnnotation {
		if _, ok := obj.GetAnnotations()[argoTrackingAnnotation]; ok {
			return true, "Argo CD", nil
		}
	}

	if checkLabel {
		labelKey := cm.Data[applicationInstanceLabelKey]
		if labelKey == "" {
			labelKey = argoDefaultInstanceLabelKey
		}

		if _, ok := obj.GetLabels()[labelKey]; ok {
			return true, "Argo CD", nil
		}
	}

	return false, "", nil
}
