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
var talosmachinelog = logf.Log.WithName("talosmachine-resource")

// SetupTalosMachineWebhookWithManager registers the webhook for TalosMachine in the manager.
func SetupTalosMachineWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.TalosMachine{}).
		WithValidator(&TalosMachineCustomValidator{APIReader: mgr.GetAPIReader()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-talosmachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosmachines,verbs=delete,versions=v1alpha1,name=vtalosmachine-v1alpha1.kb.io,admissionReviewVersions=v1

// TalosMachineCustomValidator struct is responsible for validating the TalosMachine resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type TalosMachineCustomValidator struct {
	// APIReader lists sibling TalosMachines to count live control planes
	// for the quorum check, bypassing the manager's cache: a cached read
	// can still report a control plane deleted moments ago, drifting the
	// count high -- the dangerous direction, since it is what lets a
	// second, concurrent deletion look safe when it is not. ValidateDelete is the only method that uses it, via
	// deleteguard.CountApprovedControlPlanes, the one place in the
	// codebase this count is computed.
	APIReader client.Reader
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type TalosMachine.
func (v *TalosMachineCustomValidator) ValidateCreate(_ context.Context, obj *ichorv1alpha1.TalosMachine) (admission.Warnings, error) {
	talosmachinelog.Info("Validation for TalosMachine upon creation", "name", obj.GetName())

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type TalosMachine.
func (v *TalosMachineCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *ichorv1alpha1.TalosMachine) (admission.Warnings, error) {
	talosmachinelog.Info("Validation for TalosMachine upon update", "name", newObj.GetName())

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type TalosMachine.
func (v *TalosMachineCustomValidator) ValidateDelete(ctx context.Context, obj *ichorv1alpha1.TalosMachine) (admission.Warnings, error) {
	talosmachinelog.Info("Validation for TalosMachine upon deletion", "name", obj.GetName())

	if err := deleteguard.CheckDelete(obj.GetAnnotations()); err != nil {
		return nil, err
	}

	// Only an approved control plane has ever joined etcd (design: approval is
	// what lets the controller render and apply config); any other machine
	// cannot affect quorum, so the annotation check above is the whole story.
	if obj.Spec.Role != "controlplane" || !obj.Spec.IsApproved() {
		return nil, nil
	}

	// shortcut: two concurrent deletes of different control planes in the
	// same cluster can both List before either delete commits, so both see
	// the pre-deletion count and both get admitted, together breaking
	// quorum. This is not a lock problem: admission is never serialized
	// with the commit it's gatekeeping, so no lock around this List closes
	// it -- counting objects can't enforce a cross-request invariant by
	// itself.
	//
	// The real fix -- design's "Nodes" ordered removal sequence -- now
	// exists (internal/controller/removal.go): a finalizer intercepts the
	// actual deletion and recounts under the per-cluster lock, where a
	// concurrent sibling removal has to wait its turn rather than racing.
	// This check stays anyway (not replaced):
	// it is the only one of the two that can refuse synchronously, at
	// admission time, with a message `kubectl delete` shows immediately,
	// and it still correctly refuses the single-delete case on its own.
	// removal.go's check is the authoritative one for the case this one
	// cannot see -- concurrent deletes of different control planes -- and
	// is what actually decides whether the object ever stops existing.
	controlPlanes, err := deleteguard.CountApprovedControlPlanes(ctx, v.APIReader, obj.Namespace, obj.Spec.ClusterRef.Name)
	if err != nil {
		return nil, err
	}

	return nil, deleteguard.CheckControlPlaneQuorum(controlPlanes)
}
