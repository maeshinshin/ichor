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

package controller

import (
	"context"
	"reflect"
	"slices"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ImpersonationRoleName is the ClusterRole, bound to ichor-ui's ServiceAccount, that lists the Ichor users
// and the Ichor role groups the backend may act as (ADR 0108). Kubernetes cannot match a name by prefix, so
// "any ichor:* user" cannot be written; the controller keeps the list instead. The chart creates the role
// empty. An empty rule list grants nothing (a rule with no resourceNames would grant every name, so it
// is never written without one).
const ImpersonationRoleName = "ichor-ui-impersonate"

const (
	ichorUserPrefix  = "ichor:"
	impersonateEvery = 2 * time.Minute
)

// ImpersonationRoleReconciler keeps ImpersonationRoleName equal to the IchorUsers that exist: one rule
// letting the backend impersonate the user ichor:<name> of each IchorUser, one for the group ichor:<role>
// of each role any of them has. It holds the verb `escalate` on that one role, by resourceName, so it can
// grant a right it does not hold itself, and no other RBAC power.
type ImpersonationRoleReconciler struct {
	client.Client
	// APIReader reads the ClusterRole without a cluster-wide informer.
	APIReader           client.Reader
	ControllerNamespace string
}

// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames=ichor-ui-impersonate,verbs=get;update;escalate
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorusers,verbs=get;list;watch

// desiredRules is the rule list for users: empty when there are none.
func desiredImpersonationRules(users []ichorv1alpha1.IchorUser) []rbacv1.PolicyRule {
	var names, groups []string

	for i := range users {
		names = append(names, ichorUserPrefix+users[i].Name)

		for _, r := range users[i].Spec.Roles {
			groups = append(groups, ichorUserPrefix+r)
		}
	}

	slices.Sort(names)
	slices.Sort(groups)

	names, groups = slices.Compact(names), slices.Compact(groups)

	var rules []rbacv1.PolicyRule

	if len(names) > 0 {
		rules = append(rules, rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"users"}, Verbs: []string{"impersonate"}, ResourceNames: names})
	}

	if len(groups) > 0 {
		rules = append(rules, rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"groups"}, Verbs: []string{"impersonate"}, ResourceNames: groups})
	}

	return rules
}

// Reconcile writes the role if it differs from what the IchorUsers ask for.
func (r *ImpersonationRoleReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	var users ichorv1alpha1.IchorUserList
	if err := r.List(ctx, &users, client.InNamespace(r.ControllerNamespace)); err != nil {
		return ctrl.Result{}, err
	}

	var role rbacv1.ClusterRole
	if err := r.APIReader.Get(ctx, types.NamespacedName{Name: ImpersonationRoleName}, &role); err != nil {
		if apierrors.IsNotFound(err) {
			// The chart or installer creates it; without it ichor-ui can impersonate no Ichor user.
			logf.FromContext(ctx).Info("the impersonation ClusterRole does not exist; nothing to keep", "name", ImpersonationRoleName)

			return ctrl.Result{RequeueAfter: impersonateEvery}, nil
		}

		return ctrl.Result{}, err
	}

	want := desiredImpersonationRules(users.Items)
	if !reflect.DeepEqual(emptyIfNil(role.Rules), emptyIfNil(want)) {
		role.Rules = want
		if err := r.Update(ctx, &role); err != nil {
			return ctrl.Result{}, err
		}

		logf.FromContext(ctx).Info("updated the impersonation ClusterRole", "users", len(users.Items))
	}

	return ctrl.Result{RequeueAfter: impersonateEvery}, nil
}

func emptyIfNil(r []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	if r == nil {
		return []rbacv1.PolicyRule{}
	}

	return r
}

// SetupWithManager runs the reconciler on every change to an IchorUser and every two minutes (the role
// itself is not watched: that would need list and watch on every ClusterRole).
func (r *ImpersonationRoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	single := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{{Name: ImpersonationRoleName}}
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named("impersonationrole").
		Watches(&ichorv1alpha1.IchorUser{}, single).
		Complete(r)
}
