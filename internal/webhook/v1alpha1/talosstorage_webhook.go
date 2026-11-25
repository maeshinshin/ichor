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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// SetupTalosStorageWebhookWithManager registers the webhook for TalosStorage in the manager.
func SetupTalosStorageWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.TalosStorage{}).
		WithValidator(&TalosStorageCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-talosstorage,mutating=false,failurePolicy=fail,timeoutSeconds=5,sideEffects=None,groups=ichor.maesh.dev,resources=talosstorages,verbs=create;update,versions=v1alpha1,name=vtalosstorage-v1alpha1.kb.io,admissionReviewVersions=v1

// TalosStorageCustomValidator refuses a spec.nodes selector the storage selection cannot parse: the CRD types
// the operator as a plain string, and the selection error would block every machine of the cluster.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods.
//
// +kubebuilder:object:generate=false
type TalosStorageCustomValidator struct{}

// ValidateCreate implements admission.Validator.
func (v *TalosStorageCustomValidator) ValidateCreate(_ context.Context, obj *ichorv1alpha1.TalosStorage) (admission.Warnings, error) {
	return nil, checkLabelSelector("spec.nodes", &obj.Spec.Nodes)
}

// ValidateUpdate implements admission.Validator.
func (v *TalosStorageCustomValidator) ValidateUpdate(_ context.Context, _, newObj *ichorv1alpha1.TalosStorage) (admission.Warnings, error) {
	if !newObj.DeletionTimestamp.IsZero() {
		return nil, nil // a finalizer removal must not be refused
	}

	return nil, checkLabelSelector("spec.nodes", &newObj.Spec.Nodes)
}

// ValidateDelete implements admission.Validator; deleting is always allowed.
func (v *TalosStorageCustomValidator) ValidateDelete(context.Context, *ichorv1alpha1.TalosStorage) (admission.Warnings, error) {
	return nil, nil
}

// checkLabelSelector parses with the function the renderer and the storage selection call, so the two agree.
func checkLabelSelector(path string, s *metav1.LabelSelector) error {
	if s == nil {
		return nil
	}

	if _, err := metav1.LabelSelectorAsSelector(s); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	return nil
}
