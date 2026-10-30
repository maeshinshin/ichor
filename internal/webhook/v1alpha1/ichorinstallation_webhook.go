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
	"fmt"

	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
)

// nolint:unused
// log is for logging in this package.
var ichorinstallationlog = logf.Log.WithName("ichorinstallation-resource")

// SetupIchorInstallationWebhookWithManager registers the webhook for IchorInstallation in the manager.
func SetupIchorInstallationWebhookWithManager(mgr ctrl.Manager, opts ...IchorInstallationOptions) error {
	v := &IchorInstallationCustomValidator{}

	if len(opts) > 0 {
		direct, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
		if err != nil {
			return fmt.Errorf("creating the uncached client: %w", err)
		}

		v.Settings = &SettingsGate{
			Client: direct, ReadOnly: opts[0].SettingsReadOnly, ExemptUsers: opts[0].ExemptUsers, Recorder: opts[0].Recorder,
		}
	}

	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.IchorInstallation{}).
		WithValidator(v).
		Complete()
}

// IchorInstallationOptions turn on the settings gate.
type IchorInstallationOptions struct {
	SettingsReadOnly bool
	ExemptUsers      []string
	Recorder         record.EventRecorder
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-ichorinstallation,mutating=false,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=ichor.maesh.dev,resources=ichorinstallations,verbs=create;update,versions=v1alpha1,name=vichorinstallation-v1alpha1.kb.io,admissionReviewVersions=v1

// IchorInstallationCustomValidator refuses a spec.version that is not a semantic version. The
// CRD pattern accepts a numeric pre-release identifier with a leading zero, which the controller's
// downgrade check misorders, so a kubectl write must meet the same parser the CLI and the UI use.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods.
//
// +kubebuilder:object:generate=false
type IchorInstallationCustomValidator struct {
	// Settings guards the settings fields; nil turns the guard off.
	Settings *SettingsGate
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type IchorInstallation.
func (v *IchorInstallationCustomValidator) ValidateCreate(ctx context.Context, obj *ichorv1alpha1.IchorInstallation) (admission.Warnings, error) {
	ichorinstallationlog.Info("Validation for IchorInstallation upon creation", "name", obj.GetName())

	if err := v.Settings.check(ctx, nil, obj); err != nil {
		return nil, err
	}

	return deprecatedReclaim(obj), version.CheckSemver(obj.Spec.Version)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type IchorInstallation.
// Only a changed version is checked, so an installation that predates the check can still be edited.
func (v *IchorInstallationCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ichorv1alpha1.IchorInstallation) (admission.Warnings, error) {
	ichorinstallationlog.Info("Validation for IchorInstallation upon update", "name", newObj.GetName())

	if err := v.Settings.check(ctx, oldObj, newObj); err != nil {
		return nil, err
	}

	if oldObj.Spec.Version == newObj.Spec.Version {
		return deprecatedReclaim(newObj), nil
	}

	return deprecatedReclaim(newObj), version.CheckSemver(newObj.Spec.Version)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type IchorInstallation.
func (v *IchorInstallationCustomValidator) ValidateDelete(_ context.Context, _ *ichorv1alpha1.IchorInstallation) (admission.Warnings, error) {
	return nil, nil
}

// deprecatedReclaim warns about the removed installation switch: it is kept in the
// schema, ignored, and RBAC alone decides who may reset and add a machine.
func deprecatedReclaim(obj *ichorv1alpha1.IchorInstallation) admission.Warnings {
	if obj.Spec.Reclaim == nil {
		return nil
	}

	return admission.Warnings{"spec.reclaim.enabled is deprecated and ignored: who may reset and add a machine is decided by RBAC alone (the reclaim verb on machinereclaims)"}
}
