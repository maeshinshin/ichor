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

package v1alpha1

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// nolint:unused
// log is for logging in this package.
var talosclusterlog = logf.Log.WithName("taloscluster-resource")

// SetupTalosClusterWebhookWithManager registers the webhook for TalosCluster in the manager.
func SetupTalosClusterWebhookWithManager(mgr ctrl.Manager, s ...*settings.Reader) error {
	d := &TalosClusterDefaulter{}
	if len(s) > 0 {
		d.Settings = s[0]
	}

	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.TalosCluster{}).
		WithDefaulter(d).
		WithValidator(&TalosClusterCustomValidator{APIReader: mgr.GetAPIReader(), Can: rightCheckFor(mgr.GetClient())}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-taloscluster,mutating=false,failurePolicy=fail,timeoutSeconds=5,sideEffects=None,groups=ichor.maesh.dev,resources=talosclusters,verbs=create;update;delete,versions=v1alpha1,name=vtaloscluster-v1alpha1.kb.io,admissionReviewVersions=v1

// TalosClusterCustomValidator struct is responsible for validating the TalosCluster resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type TalosClusterCustomValidator struct {
	// APIReader counts approved workers for the schedulable check, uncached
	// for the same reason as TalosMachineCustomValidator.APIReader.
	APIReader client.Reader
	// Can lets a user holding the override verb on the cluster override the
	// last-worker guard (ADR 0065, amended). nil means no override.
	Can RightCheck
	// ControllerSource finds the address the controller reaches a cluster endpoint from, for the
	// firewall preflight (ADR 0108). nil asks the routing table.
	ControllerSource SourceResolver
}

// isUnschedulable reports spec.controlPlane.schedulable == "false".
func isUnschedulable(c *ichorv1alpha1.TalosCluster) bool {
	return c.Spec.ControlPlane.Schedulable == "false"
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateCreate(ctx context.Context, obj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon creation", "name", obj.GetName())

	if err := refuseSystemNamespace("TalosCluster", obj.Namespace); err != nil {
		return nil, err
	}

	if err := validateCNICreate(obj); err != nil {
		return nil, err
	}

	warns, err := v.validateSecurityCreate(ctx, obj)
	if err != nil {
		return nil, err
	}

	if err := validateVersion("talosVersion", obj.Spec.TalosVersion); err != nil {
		return nil, err
	}

	return warns, validateVersion("kubernetesVersion", obj.Spec.KubernetesVersion)
}

// validateVersion refuses a Talos or Kubernetes version that is not a semantic version. The CRD
// pattern lets "v01.2.3" through, which the upgrade comparisons would misorder (ADR 0059).
func validateVersion(field, v string) error {
	if err := version.CheckSemver(v); err != nil {
		return fmt.Errorf("spec.%s: %w", field, err)
	}

	return nil
}

// presetRemovedMessage is the refusal for a preset ADR 0099 removed.
const presetRemovedMessage = "spec.cni.preset %q was removed in ADR 0099 (docs/adr/0099-argo-cd-and-kokumi-become-the-standard-stack.md): " +
	"Cilium is the only CNI; delete this cluster and recreate it with cilium"

// removedPreset reports whether a stored preset is one ADR 0099 removed. The CRD default makes an
// unset preset cilium.
func removedPreset(c *ichorv1alpha1.TalosCluster) bool {
	p := c.Spec.CNI.Preset

	return p != "" && p != "cilium" && p != presetUnmanaged
}

// presetUnmanaged is spec.cni.preset for an adopted cluster whose CNI Ichor leaves alone (ADR 0103).
const presetUnmanaged = "unmanaged"

// validateCNICreate refuses a new cluster that asks for a removed CNI (ADR 0099): preset flannel
// or custom, or spec.cni.custom.
func validateCNICreate(c *ichorv1alpha1.TalosCluster) error {
	switch cni := c.Spec.CNI; {
	case removedPreset(c):
		return fmt.Errorf(presetRemovedMessage, cni.Preset)
	case cni.Preset == presetUnmanaged && c.GetAnnotations()[ichorv1alpha1.TalosClusterAdoptedAnnotation] != "true":
		return fmt.Errorf("spec.cni.preset %q is only for clusters brought in by `ichor adopt` (ADR 0103): "+
			"the object must carry the annotation %s=\"true\"", presetUnmanaged, ichorv1alpha1.TalosClusterAdoptedAnnotation)
	case cni.Custom != nil:
		return errors.New("spec.cni.custom is deprecated and unused (ADR 0099): Cilium is the only CNI")
	}

	return nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon update", "name", newObj.GetName())

	// A stored flannel/custom cluster keeps its metadata and deletion, but no spec change: its
	// controllers refuse to render it, so a spec edit could never take effect (ADR 0099).
	if (removedPreset(oldObj) || removedPreset(newObj)) && !apiequality.Semantic.DeepEqual(oldObj.Spec, newObj.Spec) {
		return nil, fmt.Errorf(presetRemovedMessage, cmp.Or(newObj.Spec.CNI.Preset, oldObj.Spec.CNI.Preset))
	}

	// A version is checked when this update changes it, so a stored object that predates the
	// check can still be edited for something else.
	if err := validateChangedVersions(oldObj, newObj); err != nil {
		return nil, err
	}

	warns, err := v.validateSecurityUpdate(ctx, oldObj, newObj)
	if err != nil {
		return nil, err
	}

	// Only the transition to "false" is checked: a cluster created
	// unschedulable has no workers yet (they are discovered afterwards), and
	// unrelated edits to one that lost its workers must not be blocked.
	if !isUnschedulable(oldObj) && isUnschedulable(newObj) {
		workers, err := deleteguard.CountApprovedWorkers(ctx, v.APIReader, newObj.Namespace, newObj.Name)
		if err != nil {
			return nil, err
		}

		return warns, v.Can.checkLastWorker(ctx, workers, "talosclusters", newObj.Name, newObj.Namespace)
	}

	return warns, nil
}

// validateChangedVersions checks only the version fields an update changes.
func validateChangedVersions(oldObj, newObj *ichorv1alpha1.TalosCluster) error {
	if oldObj.Spec.TalosVersion != newObj.Spec.TalosVersion {
		if err := validateVersion("talosVersion", newObj.Spec.TalosVersion); err != nil {
			return err
		}
	}

	if oldObj.Spec.KubernetesVersion != newObj.Spec.KubernetesVersion {
		return validateVersion("kubernetesVersion", newObj.Spec.KubernetesVersion)
	}

	return nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateDelete(_ context.Context, obj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon deletion", "name", obj.GetName())

	return nil, deleteguard.CheckDelete(obj.GetAnnotations())
}
