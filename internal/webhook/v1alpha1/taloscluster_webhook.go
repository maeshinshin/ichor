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
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/mod/semver"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/release"
)

var talosclusterlog = logf.Log.WithName("taloscluster-resource")

// SetupTalosClusterWebhookWithManager registers the webhook for TalosCluster in the manager.
func SetupTalosClusterWebhookWithManager(mgr ctrl.Manager, s ...*settings.Reader) error {
	d := &TalosClusterDefaulter{}
	if len(s) > 0 {
		d.Settings = s[0]
	}

	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.TalosCluster{}).
		WithDefaulter(d).
		WithValidator(&TalosClusterCustomValidator{
			APIReader: mgr.GetAPIReader(), Can: rightCheckFor(mgr.GetClient()), ControllerUser: controllerUsername(), AutoPatchUser: autoPatchUsername(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-taloscluster,mutating=false,failurePolicy=fail,timeoutSeconds=5,sideEffects=None,groups=ichor.maesh.dev,resources=talosclusters,verbs=create;update;delete,versions=v1alpha1,name=vtaloscluster-v1alpha1.kb.io,admissionReviewVersions=v1

// TalosClusterCustomValidator struct is responsible for validating the TalosCluster resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type TalosClusterCustomValidator struct {
	// APIReader counts approved workers for the schedulable check, uncached
	// for the same reason as TalosMachineCustomValidator.APIReader.
	APIReader client.Reader
	// Can lets a user holding the override verb on the cluster override the
	// last-worker guard. nil means no override.
	Can RightCheck
	// ControllerSource finds the address the controller reaches a cluster endpoint from, for the
	// firewall preflight. nil asks the routing table.
	ControllerSource SourceResolver
	// ControllerUser is the controller's own username: it never writes a TalosCluster spec, and an update of one by
	// it that changes the spec is refused. AutoPatchUser is the ServiceAccount the automatic patcher
	// acts as; its one write is a newer patch of the running minor in spec.talosVersion or spec.kubernetesVersion,
	// and anything else it writes is refused. Empty means no user is limited (tests).
	ControllerUser, AutoPatchUser string
}

// serviceAccountUsername is the username a ServiceAccount authenticates as.
func serviceAccountUsername(ns, sa string) string {
	if ns == "" || sa == "" {
		return ""
	}

	return "system:serviceaccount:" + ns + ":" + sa
}

// controllerUsername is the controller's username and autoPatchUsername the automatic patcher's, from the downward
// API values the Deployment sets; empty when they are not set.
func controllerUsername() string {
	return serviceAccountUsername(os.Getenv("POD_NAMESPACE"), os.Getenv("POD_SERVICE_ACCOUNT"))
}

func autoPatchUsername() string {
	return serviceAccountUsername(os.Getenv("POD_NAMESPACE"), ichorv1alpha1.AutoPatchServiceAccount)
}

// limitController refuses a spec change by the controller itself, and limits the automatic patcher's identity to
// the one write it exists for: a newer patch of the same minor in the two version fields, and nothing
// else in the object changes.
func (v *TalosClusterCustomValidator) limitController(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosCluster) error {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil //nolint:nilerr // no request in context: not a webhook call we can attribute
	}

	if v.ControllerUser != "" && req.UserInfo.Username == v.ControllerUser &&
		!apiequality.Semantic.DeepEqual(oldObj.Spec, newObj.Spec) {
		return errors.New("the controller never writes a TalosCluster spec")
	}

	if v.AutoPatchUser == "" || req.UserInfo.Username != v.AutoPatchUser {
		return nil
	}

	refuse := func(why string) error {
		return fmt.Errorf("the automatic patcher may only write a newer patch of the running minor to spec.talosVersion or spec.kubernetesVersion: %s", why)
	}

	a, b := oldObj.Spec.DeepCopy(), newObj.Spec.DeepCopy()
	a.TalosVersion, b.TalosVersion = "", ""
	a.KubernetesVersion, b.KubernetesVersion = "", ""

	if !apiequality.Semantic.DeepEqual(a, b) ||
		!apiequality.Semantic.DeepEqual(oldObj.Labels, newObj.Labels) ||
		!apiequality.Semantic.DeepEqual(oldObj.Annotations, newObj.Annotations) ||
		!apiequality.Semantic.DeepEqual(oldObj.Finalizers, newObj.Finalizers) {
		return refuse("something else changes")
	}

	for _, f := range [][3]string{
		{"talosVersion", oldObj.Spec.TalosVersion, newObj.Spec.TalosVersion},
		{"kubernetesVersion", oldObj.Spec.KubernetesVersion, newObj.Spec.KubernetesVersion},
	} {
		if f[1] == f[2] {
			continue
		}

		if !semver.IsValid(f[1]) || !semver.IsValid(f[2]) || semver.MajorMinor(f[1]) != semver.MajorMinor(f[2]) || semver.Compare(f[2], f[1]) <= 0 {
			return refuse(fmt.Sprintf("%s %s to %s is not a newer patch of the same minor", f[0], f[1], f[2]))
		}
	}

	return nil
}

// isUnschedulable reports spec.controlPlane.schedulable == "false".
func isUnschedulable(c *ichorv1alpha1.TalosCluster) bool {
	return c.Spec.ControlPlane.Schedulable == "false"
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateCreate(ctx context.Context, obj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon creation", "name", obj.GetName())

	if err := refuseSystemNamespace("TalosCluster", obj.Namespace); err != nil {
		return nil, err
	}

	if err := validateCNICreate(obj); err != nil {
		return nil, err
	}

	if err := validateEndpoint(nil, obj); err != nil {
		return nil, err
	}

	if err := validateCiliumValues(nil, obj); err != nil {
		return nil, err
	}

	warns, err := v.validateSecurityCreate(ctx, obj)
	if err != nil {
		return nil, err
	}

	if err := v.validateCreateVersions(ctx, obj); err != nil {
		return nil, err
	}

	return warns, nil
}

// validateCreateVersions checks the versions of a new cluster: each must be a semantic version, and
// the Talos and Kubernetes pair must be compatible. Any version is allowed, the release
// set's included: it is the first-install default, not a limit. An incompatible pair is refused
// unless the caller holds the override verb.
func (v *TalosClusterCustomValidator) validateCreateVersions(ctx context.Context, c *ichorv1alpha1.TalosCluster) error {
	if err := validateVersion("talosVersion", c.Spec.TalosVersion); err != nil {
		return err
	}

	if err := validateVersion("kubernetesVersion", c.Spec.KubernetesVersion); err != nil {
		return err
	}

	if err := validateContract(c.Spec.VersionContract); err != nil {
		return err
	}

	return v.Can.checkCompat(ctx, release.TalosKubernetesError(c.Spec.TalosVersion, c.Spec.KubernetesVersion),
		"talosclusters", c.Name, c.Namespace)
}

// validateVersion refuses a Talos or Kubernetes version that is not a semantic version. The CRD
// pattern lets "v01.2.3" through, which the upgrade comparisons would misorder.
func validateVersion(field, v string) error {
	if err := version.CheckSemver(v); err != nil {
		return fmt.Errorf("spec.%s: %w", field, err)
	}

	return nil
}

// checkCompat lets an incompatible version pair (err, from pkg/release) through when the caller holds
// the override verb, the way the last-worker guard is overridden. Without the verb, or
// without a checker, err stands. The override is logged; the uiapi records it as an Event.
func (c RightCheck) checkCompat(ctx context.Context, err error, resource, name, namespace string) error {
	// A version nothing knows cannot be operated, so no override makes it one.
	if err == nil || c == nil || errors.Is(err, release.ErrUnknownVersion) {
		return err
	}

	allowed, aerr := c(ctx, OverrideVerb, resource, "", name, namespace)
	if aerr != nil {
		return fmt.Errorf("%w (and the %s right could not be checked: %w)", err, OverrideVerb, aerr)
	}

	if !allowed {
		return err
	}

	talosclusterlog.Info("version compatibility refusal overridden", "resource", resource, "name", name, "reason", err.Error())

	return nil
}

// validateContract refuses a version contract that is not a semantic version. A contract is "vX.Y",
// which version.CheckSemver (all three numbers) would refuse.
func validateContract(v string) error {
	if !semver.IsValid(v) {
		return fmt.Errorf("spec.versionContract %q is not a version contract (vX.Y)", v)
	}

	return nil
}

// presetRemovedMessage is the refusal for a removed preset.
const presetRemovedMessage = "spec.cni.preset %q was removed: " +
	"Cilium is the only CNI; delete this cluster and recreate it with cilium"

// removedPreset reports whether a stored preset is a removed one. The CRD default makes an
// unset preset cilium.
func removedPreset(c *ichorv1alpha1.TalosCluster) bool {
	p := c.Spec.CNI.Preset

	return p != "" && p != "cilium" && p != presetUnmanaged
}

// presetUnmanaged is spec.cni.preset for an adopted cluster whose CNI Ichor leaves alone.
const presetUnmanaged = "unmanaged"

// validateCNICreate refuses a new cluster that asks for a removed CNI: preset flannel
// or custom, or spec.cni.custom.
func validateCNICreate(c *ichorv1alpha1.TalosCluster) error {
	switch cni := c.Spec.CNI; {
	case removedPreset(c):
		return fmt.Errorf(presetRemovedMessage, cni.Preset)
	case cni.Preset == presetUnmanaged && c.GetAnnotations()[ichorv1alpha1.TalosClusterAdoptedAnnotation] != annotationTrue:
		return fmt.Errorf("spec.cni.preset %q is only for clusters brought in by `ichor adopt`: "+
			"the object must carry the annotation %s=\"true\"", presetUnmanaged, ichorv1alpha1.TalosClusterAdoptedAnnotation)
	case cni.Custom != nil:
		return errors.New("spec.cni.custom is deprecated and unused: Cilium is the only CNI")
	}

	return nil
}

// validateEndpoint checks that the endpoint's address has the shape its type needs. A new cluster is always checked;
// an update only when it changes the type or the address, so a stored object with a pair that predates the check
// (a load balancer an earlier adopt called a vip) stays editable in every other respect.
func validateEndpoint(oldObj, newObj *ichorv1alpha1.TalosCluster) error {
	if oldObj != nil && oldObj.Spec.Endpoint == newObj.Spec.Endpoint {
		return nil
	}

	if err := newObj.Spec.Endpoint.CheckAddress(); err != nil {
		return fmt.Errorf("spec.endpoint: %w", err)
	}

	return nil
}

// validateNotRemoved refuses to drop spec.network or spec.cni once it is set. Their CEL transition rules
// compare oldSelf with self, and CEL does not run when the field is absent, so a JSON patch that removes
// the field would pass them. Setting a field that was unset stays allowed.
func validateNotRemoved(oldObj, newObj *ichorv1alpha1.TalosCluster) error {
	if oldObj.Spec.Network != nil && newObj.Spec.Network == nil {
		return errors.New("spec.network is fixed once the cluster exists and cannot be removed")
	}

	if !apiequality.Semantic.DeepEqual(oldObj.Spec.CNI, ichorv1alpha1.TalosClusterCNI{}) &&
		apiequality.Semantic.DeepEqual(newObj.Spec.CNI, ichorv1alpha1.TalosClusterCNI{}) {
		return errors.New("spec.cni is fixed once the cluster exists and cannot be removed")
	}

	return nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon update", "name", newObj.GetName())

	if err := v.limitController(ctx, oldObj, newObj); err != nil {
		return nil, err
	}

	if err := validateNotRemoved(oldObj, newObj); err != nil {
		return nil, err
	}

	// A stored flannel/custom cluster keeps its metadata and deletion, but no spec change: its
	// controllers refuse to render it, so a spec edit could never take effect.
	if (removedPreset(oldObj) || removedPreset(newObj)) && !apiequality.Semantic.DeepEqual(oldObj.Spec, newObj.Spec) {
		return nil, fmt.Errorf(presetRemovedMessage, cmp.Or(newObj.Spec.CNI.Preset, oldObj.Spec.CNI.Preset))
	}

	if err := validateEndpoint(oldObj, newObj); err != nil {
		return nil, err
	}

	// The Cilium values are checked as the UI checks them, when this update changes what that check reads.
	if err := validateCiliumValues(oldObj, newObj); err != nil {
		return nil, err
	}

	// A version is checked when this update changes it, so a stored object that predates the
	// check can still be edited for something else.
	if err := v.validateChangedVersions(ctx, oldObj, newObj); err != nil {
		return nil, err
	}

	warns, err := v.validateSecurityUpdate(ctx, oldObj, newObj)
	if err != nil {
		return nil, err
	}

	// Only the transition to "false" is checked: a cluster created
	// unschedulable has no workers yet (they are discovered afterwards), and
	// unrelated edits to one that lost its workers must not be blocked.
	if !isUnschedulable(oldObj) && isUnschedulable(newObj) {
		workers, err := deleteguard.CountApprovedWorkers(ctx, v.APIReader, newObj.Namespace, newObj.Name)
		if err != nil {
			return nil, err
		}

		return warns, v.Can.checkLastWorker(ctx, workers, "talosclusters", newObj.Name, newObj.Namespace)
	}

	return warns, nil
}

// validateChangedVersions checks only the version fields an update changes: each changed value must
// be a version, and must not be a downgrade. Any newer version is allowed, the release
// set's included. Whether a Talos or Kubernetes upgrade is a valid step is the plan's and the
// executors' rule (pkg/release CheckTalosTarget, CheckKubernetesTarget), not the webhook's.
func (v *TalosClusterCustomValidator) validateChangedVersions(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosCluster) error {
	talosChanged := oldObj.Spec.TalosVersion != newObj.Spec.TalosVersion
	k8sChanged := oldObj.Spec.KubernetesVersion != newObj.Spec.KubernetesVersion

	if talosChanged {
		if err := validateVersion("talosVersion", newObj.Spec.TalosVersion); err != nil {
			return err
		}
	}

	if k8sChanged {
		if err := validateVersion("kubernetesVersion", newObj.Spec.KubernetesVersion); err != nil {
			return err
		}
	}

	// The pair is checked when either side moves. A Kubernetes move also needs the Cilium
	// the cluster runs, as the status shows it; an unobserved Cilium is not checked.
	if talosChanged || k8sChanged {
		var ciliumDetected string
		if k8sChanged && newObj.Status.CNI != nil {
			ciliumDetected = release.CiliumRelease(newObj.Status.CNI.Detected, newObj.Status.CNI.DetectedVersion)
		}

		var stepErr error
		if k8sChanged {
			stepErr = release.CheckKubernetesStep(newObj.Spec.TalosVersion, newObj.Spec.KubernetesVersion, ciliumDetected)
		} else {
			stepErr = release.TalosKubernetesError(newObj.Spec.TalosVersion, newObj.Spec.KubernetesVersion)
		}

		if err := v.Can.checkCompat(ctx, stepErr, "talosclusters", newObj.Name, newObj.Namespace); err != nil {
			return err
		}
	}

	if oldObj.Spec.VersionContract != newObj.Spec.VersionContract {
		if err := validateContract(newObj.Spec.VersionContract); err != nil {
			return err
		}
	}

	if err := release.CheckUpdate("talosVersion", oldObj.Spec.TalosVersion, newObj.Spec.TalosVersion); err != nil {
		return err
	}

	if err := release.CheckUpdate("kubernetesVersion", oldObj.Spec.KubernetesVersion, newObj.Spec.KubernetesVersion); err != nil {
		return err
	}

	return release.CheckUpdate("versionContract", oldObj.Spec.VersionContract, newObj.Spec.VersionContract)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type TalosCluster.
func (v *TalosClusterCustomValidator) ValidateDelete(_ context.Context, obj *ichorv1alpha1.TalosCluster) (admission.Warnings, error) {
	talosclusterlog.Info("Validation for TalosCluster upon deletion", "name", obj.GetName())

	return nil, deleteguard.CheckDelete(obj.GetAnnotations())
}
