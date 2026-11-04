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
	"slices"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
)

var talosconfigpatchlog = logf.Log.WithName("talosconfigpatch-resource")

// SetupTalosConfigPatchWebhookWithManager registers the dry-render check for TalosConfigPatch.
// secretNamespace is the controller's namespace, where the secrets bundle and registry
// credentials live.
func SetupTalosConfigPatchWebhookWithManager(mgr ctrl.Manager, secretNamespace string) error {
	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.TalosConfigPatch{}).
		WithValidator(&TalosConfigPatchCustomValidator{
			Client: mgr.GetClient(), SecretNamespace: secretNamespace,
		}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-talosconfigpatch,mutating=false,failurePolicy=fail,timeoutSeconds=5,sideEffects=None,groups=ichor.maesh.dev,resources=talosconfigpatches,verbs=create;update,versions=v1alpha1,name=vtalosconfigpatch-v1alpha1.kb.io,admissionReviewVersions=v1

// TalosConfigPatchCustomValidator refuses a TalosConfigPatch that would make the render of a
// machine it applies to fail. It renders every approved machine of the patch's cluster that the
// patch selects, with the controller's own render path, and refuses with the render error. A machine
// that already fails to render without this patch is skipped: the patch did not break it, and a
// fix for the other cause must not be blocked.
//
// NOTE: +kubebuilder:object:generate=false is implied: this struct is not a Kubernetes object.
type TalosConfigPatchCustomValidator struct {
	Client          client.Client
	SecretNamespace string
}

// ValidateCreate implements admission.Validator.
func (v *TalosConfigPatchCustomValidator) ValidateCreate(ctx context.Context, obj *ichorv1alpha1.TalosConfigPatch) (admission.Warnings, error) {
	return v.validate(ctx, obj)
}

// validate runs the Talos API access rule, then the dry render.
func (v *TalosConfigPatchCustomValidator) validate(ctx context.Context, p *ichorv1alpha1.TalosConfigPatch) (admission.Warnings, error) {
	return talosAPIAccessWarnings(p), v.dryRender(ctx, p)
}

// ValidateUpdate implements admission.Validator.
func (v *TalosConfigPatchCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosConfigPatch) (admission.Warnings, error) {
	if !newObj.DeletionTimestamp.IsZero() {
		return nil, nil // a finalizer removal must not be refused
	}

	// The Talos API access rule is for what the update writes: a patch stored before it existed
	// can still be edited for something else.
	if apiequality.Semantic.DeepEqual(oldObj.Spec.Documents, newObj.Spec.Documents) {
		return nil, v.dryRender(ctx, newObj)
	}

	return v.validate(ctx, newObj)
}

// ValidateDelete implements admission.Validator; deleting a patch is always allowed.
func (v *TalosConfigPatchCustomValidator) ValidateDelete(context.Context, *ichorv1alpha1.TalosConfigPatch) (admission.Warnings, error) {
	return nil, nil
}

func (v *TalosConfigPatchCustomValidator) dryRender(ctx context.Context, p *ichorv1alpha1.TalosConfigPatch) error {
	if len(p.Spec.Documents) == 0 {
		return nil
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := v.Client.Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: p.Spec.ClusterRef.Name}, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // nothing to render against yet
		}

		return err
	}

	bundle, err := secretsbundle.Load(ctx, v.Client, v.SecretNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		talosconfigpatchlog.Info("not dry-rendering the patch: the secrets bundle is unavailable", "patch", p.Name)

		return nil
	}

	sys, err := render.ResolveSystemInputs(ctx, v.Client, v.SecretNamespace, &cluster)
	if err != nil {
		talosconfigpatchlog.Info("not dry-rendering the patch: system inputs are unavailable", "patch", p.Name, "error", err.Error())

		return nil
	}

	machines, err := deleteguard.ApprovedMachines(ctx, v.Client, p.Namespace, cluster.Name, deleteguard.MachineFilter{})
	if err != nil {
		return err
	}

	var others ichorv1alpha1.TalosConfigPatchList
	if err := v.Client.List(ctx, &others, client.InNamespace(p.Namespace)); err != nil {
		return err
	}

	base := slices.DeleteFunc(others.Items, func(o ichorv1alpha1.TalosConfigPatch) bool { return o.Name == p.Name })
	with := append(slices.Clone(base), *p)

	for i := range machines {
		m := &machines[i]
		if m.Spec.Role == "" {
			continue
		}

		mine, err := render.SelectPatches(&cluster, m, []ichorv1alpha1.TalosConfigPatch{*p})
		if err != nil {
			return fmt.Errorf("patch %q: %w", p.Name, err)
		}

		if len(mine) == 0 {
			continue
		}

		if _, _, err := render.Render(&cluster, m, bundle, with, sys); err == nil {
			continue
		} else if _, _, baseErr := render.Render(&cluster, m, bundle, base, sys); baseErr != nil {
			continue // already failing without this patch
		} else {
			return fmt.Errorf("the patch would make the configuration of machine %q fail to render: %w", m.Name, err)
		}
	}

	return nil
}
