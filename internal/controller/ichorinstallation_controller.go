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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
)

// controllerDeploymentName is the controller's own Deployment as config/default names it
// (namePrefix ichor- on controller-manager).
const controllerDeploymentName = "ichor-controller-manager"

// Condition types IchorInstallation reports.
const (
	conditionUpToDate          = "UpToDate"
	conditionExternalSuggested = "ExternalSuggested"
)

// IchorInstallationReconciler reports on Ichor's own installation: the running version, and
// whether the controller finds itself under GitOps. It never changes spec and never launches
// the installer -- that is the kind: ichor executor's job, started by a TalosOperation
// (ADR 0059) -- and, per the design, the controller never upgrades itself.
type IchorInstallationReconciler struct {
	client.Client

	// Direct reads the controller's own Deployment and argocd-cm without an informer:
	// the manager's cached client would need list/watch on every Deployment and
	// ConfigMap, far more than the two single-object reads this needs.
	Direct client.Client

	// ControllerNamespace is where the controller's own Deployment lives (POD_NAMESPACE).
	ControllerNamespace string

	// ArgoCDNamespace is where argocd-cm lives, for the tracking check.
	ArgoCDNamespace string
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,namespace=ichor-system,resources=deployments,verbs=get
// +kubebuilder:rbac:groups="",resources=configmaps,resourceNames=argocd-cm,verbs=get
// +kubebuilder:rbac:groups=kustomize.toolkit.fluxcd.io,resources=kustomizations,verbs=list

// Reconcile writes status.installedVersion and the two conditions.
func (r *IchorInstallationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var inst ichorv1alpha1.IchorInstallation
	if err := r.Get(ctx, req.NamespacedName, &inst); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	orig := inst.DeepCopy()
	inst.Status.InstalledVersion = version.Version

	upToDate := metav1.ConditionFalse
	if inst.Spec.Version == version.Version {
		upToDate = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&inst.Status.Conditions, metav1.Condition{
		Type: conditionUpToDate, Status: upToDate, Reason: "VersionCompared",
		Message:            fmt.Sprintf("running %s, spec.version %s", version.Version, inst.Spec.Version),
		ObservedGeneration: inst.Generation,
	})

	suggested, msg, err := r.externalSuggested(ctx, &inst)
	if err != nil {
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&inst.Status.Conditions, metav1.Condition{
		Type: conditionExternalSuggested, Status: suggested, Reason: "TrackingChecked", Message: msg,
		ObservedGeneration: inst.Generation,
	})

	if !equalIchorStatus(orig.Status, inst.Status) {
		if err := r.Status().Update(ctx, &inst); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// externalSuggested is True when the mode is ichor but a GitOps tool tracks the controller's
// own Deployment: two writers on the same manifests (design, "Ichor under Argo CD").
func (r *IchorInstallationReconciler) externalSuggested(
	ctx context.Context, inst *ichorv1alpha1.IchorInstallation,
) (metav1.ConditionStatus, string, error) {
	if inst.Spec.ManagementMode == ichorv1alpha1.IchorManagementModeExternal {
		return metav1.ConditionFalse, "managementMode is already external", nil
	}

	var dep appsv1.Deployment

	key := types.NamespacedName{Namespace: r.ControllerNamespace, Name: controllerDeploymentName}
	if err := r.Direct.Get(ctx, key, &dep); err != nil {
		if apierrors.IsNotFound(err) {
			return metav1.ConditionFalse, "the controller's Deployment is not found", nil
		}

		return metav1.ConditionUnknown, "", err
	}

	tracked, by, err := gitopstracking.IsTracked(ctx, r.Direct, r.ArgoCDNamespace, &dep)
	if err != nil {
		return metav1.ConditionUnknown, "", err
	}

	if tracked {
		return metav1.ConditionTrue, fmt.Sprintf("the controller's Deployment is tracked by %s; consider managementMode external", by), nil
	}

	return metav1.ConditionFalse, "the controller's Deployment is not tracked", nil
}

func equalIchorStatus(a, b ichorv1alpha1.IchorInstallationStatus) bool {
	if a.InstalledVersion != b.InstalledVersion || len(a.Conditions) != len(b.Conditions) {
		return false
	}

	for i := range a.Conditions {
		x, y := a.Conditions[i], b.Conditions[i]
		if x.Type != y.Type || x.Status != y.Status || x.Message != y.Message || x.ObservedGeneration != y.ObservedGeneration {
			return false
		}
	}

	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *IchorInstallationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.IchorInstallation{}).
		Named("ichorinstallation").
		Complete(r)
}
