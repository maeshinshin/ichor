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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// IchorInstallationName is the one name the singleton may have.
const IchorInstallationName = "ichor"

// IchorInstallationSpec defines the desired state of IchorInstallation: which version of
// Ichor should be installed, and who is allowed to change it. It holds the target; the
// TalosOperation of kind ichor holds the execution, so the target version is
// written in exactly one place.
type IchorInstallationSpec struct {
	// version is the Ichor version the installation should run, as a semantic version with a
	// leading v (v0.4.0). The installer Job runs the image for this version, which carries
	// that version's own manifests.
	// +kubebuilder:validation:Pattern=`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`
	// +required
	Version string `json:"version"`

	// installerRepository is the image repository of the installer image; the Job runs
	// <installerRepository>@<digest>, where the digest is installerDigest or the one <version> resolves
	// to in that repository when the Job is created. Point it at a mirror or private registry
	// if you need to; a mirror has to carry the image's signature too (the OCI referrers), or
	// installerSignature has to say skip.
	// +kubebuilder:default="ghcr.io/maeshinshin/ichor-installer"
	// +kubebuilder:validation:MinLength=1
	// +optional
	InstallerRepository string `json:"installerRepository,omitempty"`

	// installerDigest pins the installer image to a digest ("sha256:" and 64 hex digits). Unset, the
	// controller resolves the tag <version> in installerRepository itself when it creates the Job and
	// records the digest in status.installerImage. Set, it never resolves the tag, so the registry
	// is only asked for the signature (none when installerSignature says skip). Change it together with
	// version.
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	// +optional
	InstallerDigest string `json:"installerDigest,omitempty"`

	// installerSignature says how the signature of the installer image is checked before the Job runs.
	// Unset means required.
	// +optional
	InstallerSignature *IchorInstallerSignature `json:"installerSignature,omitempty"`

	// deployment records who deploys this Ichor.
	// +optional
	Deployment *IchorDeploymentSpec `json:"deployment,omitempty"`

	// reclaim is deprecated and ignored: "Reset and add" has no installation switch;
	// RBAC alone (the reclaim verb on machinereclaims) decides who may use it. The field stays so
	// that an installed object keeps validating. The admission webhook warns when it is set.
	// +optional
	Reclaim *IchorReclaimSettings `json:"reclaim,omitempty"`

	// users holds the sign-in lifetimes.
	// +kubebuilder:validation:XValidation:rule="!has(self.tokenTTL) || !has(self.refreshLifetime) || duration(self.tokenTTL) <= duration(self.refreshLifetime)",message="tokenTTL must not exceed refreshLifetime"
	// +optional
	Users *IchorUserSettings `json:"users,omitempty"`

	// defaults holds defaults for objects created later; an existing object is never touched.
	// +optional
	Defaults *IchorDefaultSettings `json:"defaults,omitempty"`

	// updateCheck holds the switch for the check of newer Ichor releases.
	// +optional
	UpdateCheck *IchorUpdateCheckSettings `json:"updateCheck,omitempty"`

	// autoPatch lets the controller apply patch releases of the running minor by itself.
	// Off unless enabled here or by the chart.
	// +optional
	AutoPatch *IchorAutoPatchSettings `json:"autoPatch,omitempty"`
}

// Values of IchorInstallerSignature.Mode.
const (
	// InstallerSignatureRequired is the default: the installer image must carry a keyless cosign signature
	// of this repository's release workflow for spec.version, or the Ichor upgrade pauses.
	InstallerSignatureRequired = "required"
	// InstallerSignatureSkip runs an installer image without checking a signature, for builds nobody signs
	// (development and demo images). The controller records it in status.installerImage and an Event, and
	// the CLI and the UI show it.
	InstallerSignatureSkip = "skip"
)

// IchorInstallerSignature is the signature policy of the installer image.
type IchorInstallerSignature struct {
	// mode is required (the default) or skip.
	// +kubebuilder:validation:Enum=required;skip
	// +kubebuilder:default=required
	Mode string `json:"mode"`

	// reason is deprecated: unused, kept so the schema never drops a field. skip no longer needs
	// one and nothing reads it.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Reason string `json:"reason,omitempty"`
}

// Values of IchorInstallerImageStatus.Verification.
const (
	InstallerImageVerified = "Verified"
	InstallerImageSkipped  = "Skipped"
)

// IchorInstallerImageStatus is the installer image the controller last started an Ichor upgrade with, and
// whether its signature was checked.
type IchorInstallerImageStatus struct {
	// version is the Ichor version the image was resolved for.
	// +optional
	Version string `json:"version,omitempty"`
	// repository is the image repository the Job pulled from.
	// +optional
	Repository string `json:"repository,omitempty"`
	// digest is the image digest the Job runs, "sha256:...".
	// +optional
	Digest string `json:"digest,omitempty"`
	// verification is Verified when a cosign signature of the release workflow for version matched the
	// digest, Skipped when spec.installerSignature said skip.
	// +kubebuilder:validation:Enum=Verified;Skipped
	// +optional
	Verification string `json:"verification,omitempty"`
	// signer is the identity the signature matched (the certificate subject pattern); empty when Skipped.
	// +optional
	Signer string `json:"signer,omitempty"`
	// reason is deprecated: unused, kept so the schema never drops a field. It is no longer written.
	// +optional
	Reason string `json:"reason,omitempty"`
	// resolvedAt is when the controller resolved and checked the image.
	// +optional
	ResolvedAt *metav1.Time `json:"resolvedAt,omitempty"`
}

// Values of IchorAutoPatchSettings.Components.
const (
	AutoPatchTalos      = "talos"
	AutoPatchKubernetes = "kubernetes"
	AutoPatchCilium     = "cilium"
)

// IchorAutoPatchSettings is the switch for automatic patch upgrades.
type IchorAutoPatchSettings struct {
	// enabled turns automatic patching on or off. Unset falls back to the controller flag, which the chart
	// sets from autoPatch.enabled (default false).
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// components are the components whose patches are applied automatically: talos, kubernetes, cilium.
	// Unset or empty means all three. cilium is accepted and does nothing: Cilium moves only to the chart the
	// running Ichor embeds, which a person starts.
	// +kubebuilder:validation:items:Enum=talos;kubernetes;cilium
	// +listType=set
	// +optional
	Components []string `json:"components,omitempty"`

	// window is a daily UTC window "HH:MM-HH:MM" in which an automatic upgrade may start; it may wrap
	// midnight (22:00-02:00). Unset means any time.
	// +kubebuilder:validation:Pattern=`^([01][0-9]|2[0-3]):[0-5][0-9]-([01][0-9]|2[0-3]):[0-5][0-9]$`
	// +optional
	Window string `json:"window,omitempty"`

	// acknowledgedHalt is how a person clears a halt: the name of the operation (namespace/name) that halted
	// automatic patching, as status.autoPatch.haltedOperation shows it. The controller never edits spec, so it
	// reads this and resumes when it equals the halted operation; a later halt names another operation.
	// +optional
	AcknowledgedHalt string `json:"acknowledgedHalt,omitempty"`
}

// IchorUpdateCheckSettings is the switch for the check of newer Ichor releases.
type IchorUpdateCheckSettings struct {
	// enabled turns the check on or off. Unset falls back to the controller flag, which the chart
	// sets from updateCheck.enabled (default true). Off stops the registry request and removes
	// the controller's egress rule for it.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// Values of spec.deployment.manager.
const (
	IchorManagerInstaller = "installer"
	IchorManagerHelm      = "helm"
)

// IchorDeploymentSpec says who deploys Ichor.
type IchorDeploymentSpec struct {
	// manager is installer (the installer Job, `ichor init`) or helm. Omitted means installer.
	// +kubebuilder:validation:Enum=installer;helm
	// +optional
	Manager string `json:"manager,omitempty"`
}

// IchorReclaimSettings is the removed reclaim switch, kept so installed objects validate.
type IchorReclaimSettings struct {
	// enabled is deprecated and ignored.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// IchorUserSettings are the lifetimes ichor-ui applies to the tokens it issues.
type IchorUserSettings struct {
	// tokenTTL is the lifetime of an issued token, 1m to 1h. Unset falls back to the flag.
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('1m') && duration(self) <= duration('1h')",message="tokenTTL must be between 1m and 1h"
	// +optional
	TokenTTL *metav1.Duration `json:"tokenTTL,omitempty"`

	// refreshLifetime is the lifetime of a refresh token, 1h to 720h. Unset falls back to the flag.
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('1h') && duration(self) <= duration('720h')",message="refreshLifetime must be between 1h and 720h"
	// +optional
	RefreshLifetime *metav1.Duration `json:"refreshLifetime,omitempty"`
}

// IchorDefaultSettings are defaults applied at create time.
type IchorDefaultSettings struct {
	// driftPolicy is the default for a new TalosCluster's spec.driftPolicy. It takes the
	// values of that field (warnOnly, autoCorrect) "enforce" is autoCorrect.
	// +kubebuilder:validation:Enum=warnOnly;autoCorrect
	// +optional
	DriftPolicy string `json:"driftPolicy,omitempty"`
}

// IchorInstallationStatus defines the observed state of IchorInstallation.
type IchorInstallationStatus struct {
	// installedVersion is the version of the running controller, reported by the controller
	// itself: the single source of "what is installed", which is also what the CLI compares
	// itself against.
	// +optional
	InstalledVersion string `json:"installedVersion,omitempty"`

	// conditions represent the current state of the IchorInstallation resource. UpToDate is
	// True when installedVersion equals spec.version. An ExternalSuggested condition
	// from an older build is removed.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// effective shows each runtime setting with the value in force and where it came from.
	// Written by the controller.
	// +optional
	Effective *IchorEffectiveSettings `json:"effective,omitempty"`

	// latestRelease is the newest final release the controller found in the chart repository. Unset until the first
	// check, and cleared while the check is off.
	// +optional
	LatestRelease *IchorLatestRelease `json:"latestRelease,omitempty"`

	// upgradeTargets are the Talos and Kubernetes releases the controller found in their image
	// repositories. Unset until the first check, and cleared while the check is off.
	// +optional
	UpgradeTargets *IchorUpgradeTargets `json:"upgradeTargets,omitempty"`

	// autoPatch is what the automatic patcher last did and why it stopped.
	// +optional
	AutoPatch *IchorAutoPatchStatus `json:"autoPatch,omitempty"`

	// installerImage is the installer image the last Ichor upgrade ran, by digest, and whether its
	// signature was checked.
	// +optional
	InstallerImage *IchorInstallerImageStatus `json:"installerImage,omitempty"`
}

// ConditionAutoPatchHalted is True while automatic patching is stopped by an automatic operation that failed
// or paused. A person clears it with spec.autoPatch.acknowledgedHalt, or a later manual operation
// of the same cluster that completes does.
const ConditionAutoPatchHalted = "AutoPatchHalted"

// Annotations the automatic patcher puts on the TalosOperation it creates. The operation
// then runs through the normal executor.
const (
	// OperationAutoPatchAnnotation is "true" on an operation the automatic patcher created.
	OperationAutoPatchAnnotation = "ichor.maesh.dev/auto-patch"
	// AutoPatchStarter is the started-by annotation of such an operation.
	AutoPatchStarter = "ichor-auto-patch"
	// AutoPatchServiceAccount is the ServiceAccount, in the controller's namespace, that the automatic
	// patcher acts as: it writes the version and creates the operation, so the audit log names it.
	AutoPatchServiceAccount = "ichor-auto-patch"
)

// IchorAutoPatchStatus is the state of the automatic patcher.
type IchorAutoPatchStatus struct {
	// lastOperation is the operation (namespace/name) the patcher started last.
	// +optional
	LastOperation string `json:"lastOperation,omitempty"`
	// lastStartedAt is when it started.
	// +optional
	LastStartedAt *metav1.Time `json:"lastStartedAt,omitempty"`
	// haltedOperation is the automatic operation (namespace/name) that failed or paused and stopped
	// automatic patching; empty when it is not halted.
	// +optional
	HaltedOperation string `json:"haltedOperation,omitempty"`
}

// IchorUpgradeTargets is the result of the controller's listing of the Talos and Kubernetes
// releases. The lists are final releases, newest first, and the servers filter them
// per cluster.
type IchorUpgradeTargets struct {
	// checkedAt is when the last listing finished, successful or not.
	// +optional
	CheckedAt *metav1.Time `json:"checkedAt,omitempty"`
	// talos are the final Talos releases, newest first, with a leading v.
	// +optional
	Talos []string `json:"talos,omitempty"`
	// kubernetes are the final Kubernetes releases, newest first, with a leading v.
	// +optional
	Kubernetes []string `json:"kubernetes,omitempty"`
	// cilium is deprecated and unused. It held the Cilium patches whose images verified; Cilium now moves only to the
	// chart the running Ichor embeds (amendment 2026-11-04 (3)), so
	// the controller no longer writes it and clears what an earlier controller wrote. Kept so the schema does
	// not lose a field an installed build had.
	// +optional
	Cilium []string `json:"cilium,omitempty"`
	// error is the failure of the last listing; a list that failed keeps its last good value.
	// +optional
	Error string `json:"error,omitempty"`
	// talosError is why the last listing of the Talos releases failed; empty when it succeeded. The
	// other lists are unaffected.
	// +optional
	TalosError string `json:"talosError,omitempty"`
	// kubernetesError is why the last listing of the Kubernetes releases failed; empty when it succeeded.
	// +optional
	KubernetesError string `json:"kubernetesError,omitempty"`
	// ciliumError is deprecated and unused, like cilium: the controller no longer lists Cilium releases.
	// +optional
	CiliumError string `json:"ciliumError,omitempty"`
}

// IchorLatestRelease is the result of the controller's check for a newer Ichor release.
type IchorLatestRelease struct {
	// version is the highest final release found, with a leading v. A failed check keeps the
	// value of the last good one.
	// +optional
	Version string `json:"version,omitempty"`
	// checkedAt is when the last check finished, successful or not.
	// +optional
	CheckedAt *metav1.Time `json:"checkedAt,omitempty"`
	// error is the failure of the last check; empty after a successful one.
	// +optional
	Error string `json:"error,omitempty"`
}

// ConditionSettingsApplied is True when the controller has seen the generation of the
// settings in spec.
const ConditionSettingsApplied = "SettingsApplied"

// The RBAC subresources the webhook checks with verb update on ichorinstallations are
// IchorSubresourceVersion (spec.version, installerRepository, installerDigest, installerSignature, deployment) and
// IchorSubresourceSettings (users, defaults).
const (
	IchorSubresourceVersion  = "version"
	IchorSubresourceSettings = "settings"
)

// Values of IchorEffectiveValue.Source.
const (
	IchorSourceSpec       = "spec"
	IchorSourceDeployment = "deployment"
)

// IchorEffectiveValue is one setting in force.
type IchorEffectiveValue struct {
	// value is the setting as a string.
	Value string `json:"value"`
	// source is spec when the field is set, deployment when the flag or chart value applies.
	// +kubebuilder:validation:Enum=spec;deployment
	Source string `json:"source"`
}

// IchorEffectiveSettings lists the settings in force.
type IchorEffectiveSettings struct {
	// +optional
	Manager *IchorEffectiveValue `json:"manager,omitempty"`
	// reclaimEnabled is deprecated: the controller no longer reports it.
	// +optional
	ReclaimEnabled *IchorEffectiveValue `json:"reclaimEnabled,omitempty"`
	// +optional
	TokenTTL *IchorEffectiveValue `json:"tokenTTL,omitempty"`
	// +optional
	RefreshLifetime *IchorEffectiveValue `json:"refreshLifetime,omitempty"`
	// +optional
	DriftPolicy *IchorEffectiveValue `json:"driftPolicy,omitempty"`
	// +optional
	UpdateCheck *IchorEffectiveValue `json:"updateCheck,omitempty"`
	// autoPatch is "true" or "false".
	// +optional
	AutoPatch *IchorEffectiveValue `json:"autoPatch,omitempty"`
	// autoPatchComponents is the comma-separated list of components in force.
	// +optional
	AutoPatchComponents *IchorEffectiveValue `json:"autoPatchComponents,omitempty"`
	// autoPatchWindow is the daily UTC window in force; empty means any time.
	// +optional
	AutoPatchWindow *IchorEffectiveValue `json:"autoPatchWindow,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'ichor'",message="the singleton must be named ichor"
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="Installed",type=string,JSONPath=".status.installedVersion"

// IchorInstallation is the Schema for the ichorinstallations API: Ichor's own version, a
// cluster-scoped singleton.
type IchorInstallation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of IchorInstallation
	// +required
	Spec IchorInstallationSpec `json:"spec"`

	// status defines the observed state of IchorInstallation
	// +optional
	Status IchorInstallationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// IchorInstallationList contains a list of IchorInstallation
type IchorInstallationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IchorInstallation `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &IchorInstallation{}, &IchorInstallationList{})
		return nil
	})
}

// IchorHelmManagedMessage is the refusal for an Ichor upgrade operation on an install Helm owns
// (spec.deployment.manager: helm).
const IchorHelmManagedMessage = "this Ichor is installed by Helm; run helm upgrade"

// ReasonManagedByHelm is the reason of the UpToDate condition on an install Helm owns.
const ReasonManagedByHelm = "ManagedByHelm"

// HelmManaged reports that Helm deploys this Ichor (spec.deployment.manager: helm).
func (i *IchorInstallation) HelmManaged() bool {
	return i != nil && i.Spec.Deployment != nil && i.Spec.Deployment.Manager == IchorManagerHelm
}
