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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
)

// controllerDeploymentName is the controller's own Deployment as config/default names it
// (namePrefix ichor- on controller-manager).
const controllerDeploymentName = "ichor-controller-manager"

// Condition types IchorInstallation reports.
const (
	conditionUpToDate = "UpToDate"

	// conditionExternalSuggested was reported while Ichor knew a GitOps tool; it is no longer set and
	// is removed from the status of installations that carry it.
	conditionExternalSuggested = "ExternalSuggested"
)

// IchorInstallationReconciler reports on Ichor's own installation: the running version. It never changes spec and never launches
// the installer -- that is the kind: ichor executor's job, started by a TalosOperation -- and, per the design, the
// controller never upgrades itself.
type IchorInstallationReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations/status,verbs=get;update;patch

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

	meta.RemoveStatusCondition(&inst.Status.Conditions, conditionExternalSuggested)

	if !equalIchorStatus(orig.Status, inst.Status) {
		if err := r.Status().Update(ctx, &inst); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
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
