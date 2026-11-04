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
	"time"

	"golang.org/x/mod/semver"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/internal/version"
)

// Condition types IchorInstallation reports.
const (
	conditionUpToDate = "UpToDate"

	// conditionExternalSuggested was reported while Ichor knew a GitOps tool; it is no longer set and
	// is removed from the status of installations that carry it (docs/adr/0101).
	conditionExternalSuggested = "ExternalSuggested"
)

// IchorInstallationReconciler reports on Ichor's own installation: the running version. It never changes spec and never launches
// the installer -- that is the kind: ichor executor's job, started by a TalosOperation
// (ADR 0059) -- and, per the design, the controller never upgrades itself.
type IchorInstallationReconciler struct {
	client.Client

	// DeployedBy is the controller's --deployed-by: the spec.deployment.manager a seeded
	// singleton gets ("installer" when empty).
	DeployedBy string
	// SettingsFallback and SettingsReadOnly are the flag values status.effective reports against (ADR 0105).
	SettingsFallback settings.Fallback
	SettingsReadOnly bool
	// Namespace is the controller's namespace, where the egress policy for the installer image's registry
	// lives (ADR 0122). Empty writes no policy.
	Namespace string
}

// DefaultInstallerImagePolicyName is the CiliumNetworkPolicy that lets the controller reach the registry
// that holds the installer image and its signature (ADR 0122). It exists whenever Ichor's own controller
// policy does, and is maintained from spec.installerRepository.
const DefaultInstallerImagePolicyName = "ichor-controller-installer-image"

// ghcrBlobHost is where ghcr.io redirects the blobs of an image, a signature bundle among them.
const ghcrBlobHost = "pkg-containers.githubusercontent.com"

// installerRegistryRequeue is how often the policy is checked again: Ichor's controller policy may be
// applied after this reconcile ran (ADR 0100 applies it once Cilium is ready).
const installerRegistryRequeue = 5 * time.Minute

// Seed creates the singleton once when it does not exist (ADR 0105, decision 8): spec.version is
// this controller's version and spec.deployment.manager is --deployed-by. It is the one place
// the controller writes a spec, a narrow exception to "the controller never mutates spec": a
// missing object is made with computed defaults, no user intent changes, and an object that
// exists is never read for update or touched. A failure loses nothing (every consumer tolerates
// an absent object), so the caller only logs it.
func (r *IchorInstallationReconciler) Seed(ctx context.Context) error {
	if !semver.IsValid(version.Version) {
		return nil // a dev build has no version the CRD would accept
	}

	manager := ichorv1alpha1.IchorManagerInstaller
	if r.DeployedBy == ichorv1alpha1.IchorManagerHelm {
		manager = ichorv1alpha1.IchorManagerHelm
	}

	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			Version:    version.Version,
			Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: manager},
		},
	}

	return client.IgnoreAlreadyExists(r.Create(ctx, inst))
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations/status,verbs=get;update;patch

// Reconcile writes status.installedVersion and the two conditions.
func (r *IchorInstallationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var inst ichorv1alpha1.IchorInstallation
	if err := r.Get(ctx, req.NamespacedName, &inst); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	orig := inst.DeepCopy()
	inst.Status.InstalledVersion = version.Version

	cond := metav1.Condition{
		Type: conditionUpToDate, Status: metav1.ConditionFalse, Reason: "VersionCompared",
		Message:            fmt.Sprintf("running %s, spec.version %s", version.Version, inst.Spec.Version),
		ObservedGeneration: inst.Generation,
	}

	switch {
	case inst.HelmManaged():
		// spec.version only records the version at creation; Helm moves the controller.
		cond.Status, cond.Reason = metav1.ConditionTrue, ichorv1alpha1.ReasonManagedByHelm
		cond.Message = fmt.Sprintf("installed by Helm, running %s", version.Version)
	case inst.Spec.Version == version.Version:
		cond.Status = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&inst.Status.Conditions, cond)

	meta.RemoveStatusCondition(&inst.Status.Conditions, conditionExternalSuggested)

	r.applySettings(&inst)

	if !apiequality.Semantic.DeepEqual(orig.Status, inst.Status) {
		if err := r.Status().Update(ctx, &inst); err != nil {
			return ctrl.Result{}, err
		}
	}

	if r.Namespace == "" {
		return ctrl.Result{}, nil
	}

	if err := r.allowInstallerRegistry(ctx, &inst); err != nil {
		logf.FromContext(ctx).Info("Writing the egress policy for the installer image's registry failed; "+
			"an Ichor upgrade may not reach it", "error", err.Error())
	}

	return ctrl.Result{RequeueAfter: installerRegistryRequeue}, nil
}

// allowInstallerRegistry writes the egress rule to the host of spec.installerRepository, but only while
// Ichor's own controller policy is installed (a lone egress rule would cut every other flow of the
// controller, ADR 0100) and the install is not Helm's (which refuses Ichor upgrades).
func (r *IchorInstallationReconciler) allowInstallerRegistry(ctx context.Context, inst *ichorv1alpha1.IchorInstallation) error {
	app, err := controllerPolicyApp(ctx, r.Client, r.Namespace, DefaultInstallerImagePolicyName)
	if err != nil {
		return err
	}

	repo := inst.Spec.InstallerRepository
	if app == "" || inst.HelmManaged() || repo == "" {
		return removePolicy(ctx, r.Client, r.Namespace, DefaultInstallerImagePolicyName)
	}

	host, err := releasecheck.Host(repo)
	if err != nil {
		return err
	}

	hosts := []string{host}
	if host == "ghcr.io" {
		hosts = append(hosts, ghcrBlobHost)
	}

	spec := releaseCheckSpec(app, hosts)
	spec["description"] = "Egress of the controller to the registry of the installer image, to resolve its digest and " +
		"verify its signature before an Ichor upgrade (ADR 0122). Written by the controller; edits are overwritten."

	_, err = writePolicy(ctx, r.Client, r.Namespace, DefaultInstallerImagePolicyName, app, spec)

	return err
}

// SetupWithManager sets up the controller with the Manager.
func (r *IchorInstallationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.IchorInstallation{}).
		Named("ichorinstallation").
		Complete(r)
}
