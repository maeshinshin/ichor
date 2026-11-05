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
	"errors"
	"fmt"
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

var ichoruserlog = logf.Log.WithName("ichoruser-resource")

// mastersGroup is the group of the secrets-bundle admin certificate `ichor init` writes into the kubeconfig:
// the override credential of ADR 0094, the way back in when the last admin is gone.
const mastersGroup = "system:masters"

// SetupIchorUserWebhookWithManager registers the webhook for IchorUser in the manager. exempt are the exact
// usernames the last-admin rule never stops (DefaultExemptUsers: the controller and the Kubernetes controllers
// that cascade a namespace deletion).
func SetupIchorUserWebhookWithManager(mgr ctrl.Manager, exempt []string) error {
	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.IchorUser{}).
		WithValidator(&IchorUserCustomValidator{Reader: mgr.GetAPIReader(), ExemptUsers: exempt}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-ichoruser,mutating=false,failurePolicy=fail,timeoutSeconds=5,sideEffects=None,groups=ichor.maesh.dev,resources=ichorusers,verbs=create;update;delete,versions=v1alpha1,name=vichoruser-v1alpha1.kb.io,admissionReviewVersions=v1

// IchorUserCustomValidator applies to a hand-written IchorUser the two rules the UI backend applies to
// UserService calls (ADR 0129): a user may grant only the roles they hold (admin: any), and the last enabled
// admin cannot be deleted, disabled or demoted. The rules are in pkg/ichorauth, shared with internal/uiapi.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type IchorUserCustomValidator struct {
	// Reader lists the users uncached, so the last-admin rule sees an admin created a moment ago.
	Reader client.Reader
	// ExemptUsers are exact usernames the last-admin rule never stops.
	ExemptUsers []string
}

// grant is guardGrant: it limits an Ichor-sourced identity (the username ichor:<name>, which only Ichor's own
// issuer can give, ADR 0094). Any other identity is gated by Kubernetes RBAC alone, as in the UI.
func (v *IchorUserCustomValidator) grant(ctx context.Context, current, wanted []string) error {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return errors.New("no admission request in the context")
	}

	if !strings.HasPrefix(req.UserInfo.Username, ichorauth.UserPrefix) {
		return nil
	}

	if r := ichorauth.UngrantedRole(req.UserInfo.Groups, current, wanted); r != "" {
		return fmt.Errorf("you cannot grant the role %q: you do not hold it (ADR 0129)", r)
	}

	return nil
}

// lastAdmin is guardLastAdmin. It is a guard against a slip, not a security boundary (ADR 0094): the override
// certificate (system:masters) and the exempt users, among them the namespace controller that deletes the
// users with their namespace, pass.
func (v *IchorUserCustomValidator) lastAdmin(ctx context.Context, u *ichorv1alpha1.IchorUser) error {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return errors.New("no admission request in the context")
	}

	if slices.Contains(v.ExemptUsers, req.UserInfo.Username) || slices.Contains(req.UserInfo.Groups, mastersGroup) {
		return nil
	}

	var list ichorv1alpha1.IchorUserList
	if err := v.Reader.List(ctx, &list, client.InNamespace(u.Namespace)); err != nil {
		return fmt.Errorf("listing the users to find another admin: %w", err)
	}

	for i := range list.Items {
		o := &list.Items[i]
		if o.Name != u.Name && ichorauth.IsEnabledAdmin(o.Spec.Roles, o.Spec.Disabled) {
			return nil
		}
	}

	return errors.New("this is the last enabled admin; refusing (the override certificate in ~/.ichor/clusters/<name>/ can still change it, ADR 0094)")
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type IchorUser.
func (v *IchorUserCustomValidator) ValidateCreate(ctx context.Context, obj *ichorv1alpha1.IchorUser) (admission.Warnings, error) {
	ichoruserlog.Info("Validation for IchorUser upon creation", "name", obj.GetName())

	return nil, v.grant(ctx, nil, obj.Spec.Roles)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type IchorUser.
func (v *IchorUserCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ichorv1alpha1.IchorUser) (admission.Warnings, error) {
	ichoruserlog.Info("Validation for IchorUser upon update", "name", newObj.GetName())

	if err := v.grant(ctx, oldObj.Spec.Roles, newObj.Spec.Roles); err != nil {
		return nil, err
	}

	// An object that is already being deleted was guarded when the delete was asked for.
	if newObj.DeletionTimestamp != nil ||
		!ichorauth.IsEnabledAdmin(oldObj.Spec.Roles, oldObj.Spec.Disabled) || ichorauth.IsEnabledAdmin(newObj.Spec.Roles, newObj.Spec.Disabled) {
		return nil, nil
	}

	return nil, v.lastAdmin(ctx, oldObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type IchorUser.
func (v *IchorUserCustomValidator) ValidateDelete(ctx context.Context, obj *ichorv1alpha1.IchorUser) (admission.Warnings, error) {
	ichoruserlog.Info("Validation for IchorUser upon deletion", "name", obj.GetName())

	if !ichorauth.IsEnabledAdmin(obj.Spec.Roles, obj.Spec.Disabled) {
		return nil, nil
	}

	return nil, v.lastAdmin(ctx, obj)
}
