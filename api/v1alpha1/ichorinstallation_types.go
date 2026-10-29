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
	// <installerRepository>:<version>. Point it at a mirror or private registry if you need to.
	// +kubebuilder:default="ghcr.io/maeshinshin/ichor-installer"
	// +kubebuilder:validation:MinLength=1
	// +optional
	InstallerRepository string `json:"installerRepository,omitempty"`

	// deployment records who deploys this Ichor.
	// +optional
	Deployment *IchorDeploymentSpec `json:"deployment,omitempty"`

	// reclaim holds the break-glass setting.
	// +optional
	Reclaim *IchorReclaimSettings `json:"reclaim,omitempty"`

	// users holds the sign-in lifetimes.
	// +kubebuilder:validation:XValidation:rule="!has(self.tokenTTL) || !has(self.refreshLifetime) || duration(self.tokenTTL) <= duration(self.refreshLifetime)",message="tokenTTL must not exceed refreshLifetime"
	// +optional
	Users *IchorUserSettings `json:"users,omitempty"`

	// defaults holds defaults for objects created later; an existing object is never touched.
	// +optional
	Defaults *IchorDefaultSettings `json:"defaults,omitempty"`
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

// IchorReclaimSettings is the break-glass reclaim switch.
type IchorReclaimSettings struct {
	// enabled turns the break-glass reclaim on. Unset falls back to the controller flag.
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
}

// ConditionSettingsApplied is True when the controller has seen the generation of the
// settings in spec.
const ConditionSettingsApplied = "SettingsApplied"

// Writing spec.reclaim.enabled=true needs this annotation on the request, with a value that starts
// with ConfirmReclaimValue and differs from the one on the stored object, so a confirmation left
// over from an earlier enable never counts (the UI API and CLI write "enable-<unique>"; kubectl
// users write e.g. "enable" once, or "enable-2"), so a stray kubectl apply cannot turn break-glass on. The RBAC
// subresources
// the webhook checks with verb update on ichorinstallations are IchorSubresourceVersion (spec.version,
// installerRepository, deployment), IchorSubresourceReclaim and IchorSubresourceSettings (users, defaults).
const (
	ConfirmReclaimAnnotation = "ichor.maesh.dev/confirm-reclaim"
	ConfirmReclaimValue      = "enable"

	IchorSubresourceVersion  = "version"
	IchorSubresourceReclaim  = "reclaim"
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
	// +optional
	ReclaimEnabled *IchorEffectiveValue `json:"reclaimEnabled,omitempty"`
	// +optional
	TokenTTL *IchorEffectiveValue `json:"tokenTTL,omitempty"`
	// +optional
	RefreshLifetime *IchorEffectiveValue `json:"refreshLifetime,omitempty"`
	// +optional
	DriftPolicy *IchorEffectiveValue `json:"driftPolicy,omitempty"`
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
