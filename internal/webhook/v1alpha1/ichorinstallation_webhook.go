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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
)

// nolint:unused
// log is for logging in this package.
var ichorinstallationlog = logf.Log.WithName("ichorinstallation-resource")

// SetupIchorInstallationWebhookWithManager registers the webhook for IchorInstallation in the manager.
func SetupIchorInstallationWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.IchorInstallation{}).
		WithValidator(&IchorInstallationCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-ichorinstallation,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=ichorinstallations,verbs=create;update,versions=v1alpha1,name=vichorinstallation-v1alpha1.kb.io,admissionReviewVersions=v1

// IchorInstallationCustomValidator refuses a spec.version that is not a semantic version. The
// CRD pattern accepts a numeric pre-release identifier with a leading zero, which the controller's
// downgrade check misorders, so a kubectl write must meet the same parser the CLI and the UI use.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods.
//
// +kubebuilder:object:generate=false
type IchorInstallationCustomValidator struct{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type IchorInstallation.
func (v *IchorInstallationCustomValidator) ValidateCreate(_ context.Context, obj *ichorv1alpha1.IchorInstallation) (admission.Warnings, error) {
	ichorinstallationlog.Info("Validation for IchorInstallation upon creation", "name", obj.GetName())

	return nil, version.CheckSemver(obj.Spec.Version)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type IchorInstallation.
// Only a changed version is checked, so an installation that predates the check can still be edited.
func (v *IchorInstallationCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *ichorv1alpha1.IchorInstallation) (admission.Warnings, error) {
	ichorinstallationlog.Info("Validation for IchorInstallation upon update", "name", newObj.GetName())

	if oldObj.Spec.Version == newObj.Spec.Version {
		return nil, nil
	}

	return nil, version.CheckSemver(newObj.Spec.Version)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type IchorInstallation.
func (v *IchorInstallationCustomValidator) ValidateDelete(_ context.Context, _ *ichorv1alpha1.IchorInstallation) (admission.Warnings, error) {
	return nil, nil
}
