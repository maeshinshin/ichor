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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Values for IchorInstallationSpec.ManagementMode.
const (
	// IchorManagementModeIchor means upgrades run through the installer Job.
	IchorManagementModeIchor = "ichor"

	// IchorManagementModeExternal is deprecated, unused: it was Argo CD owning Ichor's manifests,
	// with no installer Job. The value is still accepted, and treated as ichor.
	IchorManagementModeExternal = "external"
)

// IchorInstallationName is the one name the singleton may have.
const IchorInstallationName = "ichor"

// IchorInstallationSpec defines the desired state of IchorInstallation: which version of
// Ichor should be installed, and who is allowed to change it. It holds the target; the
// TalosOperation of kind ichor holds the execution, so the target version is
// written in exactly one place.
type IchorInstallationSpec struct {
	// managementMode is deprecated, unused; kept so the CRD never drops a field.
	// Upgrades always run through the installer Job a TalosOperation of kind ichor starts. The
	// value external, which meant Argo CD owned Ichor's manifests, is still accepted and treated
	// as ichor.
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
	// <installerRepository>:<version>. Point it at a mirror or private registry if you need to.
	// +kubebuilder:default="ghcr.io/maeshinshin/ichor-installer"
	// +kubebuilder:validation:MinLength=1
	// +optional
	InstallerRepository string `json:"installerRepository,omitempty"`

	// stack is deprecated and unused: Argo CD and Kokumi are no longer part of Ichor.
	// It is kept so the CRD never drops a field.
	// +optional
	Stack *IchorStackSpec `json:"stack,omitempty"`
}

// Values for IchorStackSpec.Promotion.
const (
	// IchorStackPromotionAutomatic moves Kokumi's Serving to the newest Ready Preparation.
	IchorStackPromotionAutomatic = "Automatic"

	// IchorStackPromotionManual leaves promotion to a human in Kokumi's UI.
	IchorStackPromotionManual = "Manual"
)

// IchorStackSpec is deprecated and unused; kept so the CRD never drops a field.
type IchorStackSpec struct {
	// argocd is the user layer for the Argo CD bundle.
	// +optional
	ArgoCD *IchorStackComponentSpec `json:"argocd,omitempty"`

	// kokumi is the user layer for the Kokumi bundle. Kokumi has no chart, so its values accept
	// only the keys Ichor maps to patches it controls (memory limits, replica counts).
	// +optional
	Kokumi *IchorStackComponentSpec `json:"kokumi,omitempty"`

	// promotion is Kokumi's Order promotion mode for both components: Automatic (default) or
	// Manual, where a human promotes in Kokumi's UI.
	// +kubebuilder:validation:Enum=Automatic;Manual
	// +kubebuilder:default=Automatic
	// +optional
	Promotion string `json:"promotion,omitempty"`
}

// IchorStackComponentSpec is deprecated and unused; kept so the CRD never drops a field.
type IchorStackComponentSpec struct {
	// values is the user layer; effective values are Ichor's defaults, then these, then the
	// Ichor-owned keys.
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Values *apiextensionsv1.JSON `json:"values,omitempty"`
}

// IchorInstallationStatus defines the observed state of IchorInstallation.
type IchorInstallationStatus struct {
	// installedVersion is the version of the running controller, reported by the controller
	// itself: the single source of "what is installed", which is also what the CLI compares
	// itself against.
	// +optional
	InstalledVersion string `json:"installedVersion,omitempty"`

	// stack is deprecated and unused: nothing writes it any more. It is kept so the
	// CRD never drops a field.
	// +optional
	Stack *IchorStackStatus `json:"stack,omitempty"`

	// conditions represent the current state of the IchorInstallation resource. UpToDate is
	// True when installedVersion equals spec.version. An ExternalSuggested condition
	// from an older build is removed.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// IchorStackStatus is deprecated and unused; kept so the CRD never drops a field.
type IchorStackStatus struct {
	// argocd is the Argo CD bundle's state.
	// +optional
	ArgoCD *IchorStackComponentStatus `json:"argocd,omitempty"`

	// kokumi is the Kokumi bundle's state.
	// +optional
	Kokumi *IchorStackComponentStatus `json:"kokumi,omitempty"`
}

// IchorStackComponentStatus is deprecated and unused; kept so the CRD never drops a field.
type IchorStackComponentStatus struct {
	// publishedTag is the registry tag Ichor last published (<chartVersion>-<hash12>).
	// +optional
	PublishedTag string `json:"publishedTag,omitempty"`

	// digest is the published artifact's OCI digest.
	// +optional
	Digest string `json:"digest,omitempty"`

	// orderReady is whether Kokumi's Order for the component is Ready.
	// +optional
	OrderReady bool `json:"orderReady,omitempty"`

	// applicationHealth is Argo CD's view of the Application Kokumi created: Synced/Healthy,
	// or the reason it is not.
	// +optional
	ApplicationHealth string `json:"applicationHealth,omitempty"`
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
