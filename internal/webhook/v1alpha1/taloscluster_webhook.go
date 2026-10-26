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
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// nolint:unused
// log is for logging in this package.
var talosclusterlog = logf.Log.WithName("taloscluster-resource")

// SetupTalosClusterWebhookWithManager registers the webhook for TalosCluster in the manager.
func SetupTalosClusterWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.TalosCluster{}).
		WithValidator(&TalosClusterCustomValidator{APIReader: mgr.GetAPIReader()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-taloscluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosclusters,verbs=update;delete,versions=v1alpha1,name=vtaloscluster-v1alpha1.kb.io,admissionReviewVersions=v1

// TalosClusterCustomValidator struct is responsible for validating the TalosCluster resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type TalosClusterCustomValidator struct {
	// APIReader counts approved workers for the schedulable check, uncached
	// for the same reason as TalosMachineCustomValidator.APIReader.
	APIReader client.Reader
}

// isUnschedulable reports spec.controlPlane.schedulable == "false".
func isUnschedulable(c *ichorv1alpha1.TalosCluster) bool {
	return c.Spec.ControlPlane.Schedulable == "false"
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateCreate(_ context.Context, obj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon creation", "name", obj.GetName())

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon update", "name", newObj.GetName())

	// Only the transition to "false" is checked: a cluster created
	// unschedulable has no workers yet (they are discovered afterwards), and
	// unrelated edits to one that lost its workers must not be blocked.
	if !isUnschedulable(oldObj) && isUnschedulable(newObj) {
		workers, err := deleteguard.CountApprovedWorkers(ctx, v.APIReader, newObj.Namespace, newObj.Name)
		if err != nil {
			return nil, err
		}

		return nil, deleteguard.CheckLastWorker(true, workers)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateDelete(_ context.Context, obj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon deletion", "name", obj.GetName())

	return nil, deleteguard.CheckDelete(obj.GetAnnotations())
}
