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

// Values for IchorInstallationSpec.ManagementMode.
const (
	// IchorManagementModeIchor means upgrades run through the installer Job (ADR 0058).
	IchorManagementModeIchor = "ichor"

	// IchorManagementModeExternal means Argo CD owns Ichor's manifests: no installer Job
	// ever runs, and an upgrade is a pull request that bumps the chart version.
	IchorManagementModeExternal = "external"
)

// IchorInstallationName is the one name the singleton may have.
const IchorInstallationName = "ichor"

// IchorInstallationSpec defines the desired state of IchorInstallation: which version of
// Ichor should be installed, and who is allowed to change it. It holds the target; the
// TalosOperation of kind ichor holds the execution (ADR 0058), so the target version is
// written in exactly one place.
type IchorInstallationSpec struct {
	// managementMode says who owns Ichor's own manifests. ichor: upgrades run through the
	// installer Job a TalosOperation of kind ichor starts. external: Argo CD owns them, and
	// the controller never launches an installer.
	// +kubebuilder:validation:Enum=ichor;external
	// +kubebuilder:default=ichor
	// +optional
	ManagementMode string `json:"managementMode,omitempty"`

	// version is the Ichor version the installation should run, as a semantic version with a
	// leading v (v0.4.0). The installer Job runs the image for this version, which carries
	// that version's own manifests.
	// +kubebuilder:validation:Pattern=`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`
	// +required
	Version string `json:"version"`

	// installerRepository is the image repository of the installer image; the Job runs
	// <installerRepository>:<version>. Point it at a mirror in an air-gapped cluster.
	// +kubebuilder:default="ghcr.io/maeshinshin/ichor-installer"
	// +kubebuilder:validation:MinLength=1
	// +optional
	InstallerRepository string `json:"installerRepository,omitempty"`
}

// IchorInstallationStatus defines the observed state of IchorInstallation.
type IchorInstallationStatus struct {
	// installedVersion is the version of the running controller, reported by the controller
	// itself: the single source of "what is installed", which is also what the CLI compares
	// itself against (ADR 0058).
	// +optional
	InstalledVersion string `json:"installedVersion,omitempty"`

	// conditions represent the current state of the IchorInstallation resource. UpToDate is
	// True when installedVersion equals spec.version. ExternalSuggested is True when
	// managementMode is ichor but the controller's own Deployment is tracked by Argo CD.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'ichor'",message="the singleton must be named ichor"
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=".spec.managementMode"
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="Installed",type=string,JSONPath=".status.installedVersion"

// IchorInstallation is the Schema for the ichorinstallations API: Ichor's own version and
// management mode, a cluster-scoped singleton.
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
		s.AddKnownTypes(SchemeGroupVersion, &IchorInstallation{}, &IchorInstallationList{})
		return nil
	})
}
