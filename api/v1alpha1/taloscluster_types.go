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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TalosClusterEndpoint is the cluster's control-plane endpoint.
type TalosClusterEndpoint struct {
	// type is the kind of address the endpoint resolves through.
	// +kubebuilder:validation:Enum=vip;loadBalancer;dns;node
	// +required
	Type string `json:"type"`

	// address is the endpoint's address: an IP for vip/node, or a hostname for loadBalancer/dns.
	// +kubebuilder:validation:MinLength=1
	// +required
	Address string `json:"address"`
}

// TalosClusterSchematic references the Image Factory schematic used for this cluster's installer image.
type TalosClusterSchematic struct {
	// id is the Image Factory schematic ID.
	// +kubebuilder:validation:MinLength=1
	// +required
	ID string `json:"id"`
}

// TalosClusterControlPlane configures the control-plane topology.
type TalosClusterControlPlane struct {
	// count is the number of control-plane nodes; 2 and 4 are rejected because they don't improve etcd availability over 1 or 3.
	// +kubebuilder:validation:Enum=1;3;5
	// +required
	Count int32 `json:"count"`

	// schedulable controls whether control planes accept workloads; required with no default because nothing on the
	// object can resolve an auto value before any machine exists.
	// +kubebuilder:validation:Enum="true";"false"
	// +required
	Schedulable string `json:"schedulable"`
}

// TalosClusterCNICustomSource is an Argo CD Application source for a user-supplied CNI: a Helm
// chart or a directory of plain manifests in a Git repository. Exactly one of chart or path is
// set, matching Argo CD's own ApplicationSource union.
// +kubebuilder:validation:XValidation:rule="(has(self.chart) ? 1 : 0) + (has(self.path) ? 1 : 0) == 1",message="exactly one of chart or path must be set"
type TalosClusterCNICustomSource struct {
	// repoURL is the Git or Helm repository Argo CD syncs from.
	// +kubebuilder:validation:MinLength=1
	// +required
	RepoURL string `json:"repoURL"`

	// path is a directory within repoURL holding plain manifests; set instead of chart.
	// +optional
	Path string `json:"path,omitempty"`

	// chart is a Helm chart name within repoURL; set instead of path.
	// +optional
	Chart string `json:"chart,omitempty"`

	// targetRevision is the Git ref or chart version Argo CD syncs.
	// +kubebuilder:validation:MinLength=1
	// +required
	TargetRevision string `json:"targetRevision"`
}

// TalosClusterCNICustom declares a user-supplied CNI's chart/manifest source and the Talos-side
// settings it needs -- the two things a "custom" preset cannot infer on its own, since Ichor
// does not know what the CNI is (docs/design/architecture.md, "CNI").
type TalosClusterCNICustom struct {
	// source points Argo CD at the chart or manifests for this CNI.
	// +required
	Source TalosClusterCNICustomSource `json:"source"`

	// documents are the Talos-side config documents this CNI needs,
	// applied as a cluster-layer patch -- e.g. disabling kube-proxy when kubeProxy is "replaced".
	// +optional
	Documents []TalosConfigPatchDocument `json:"documents,omitempty"`
}

// TalosClusterCNI configures which CNI preset this cluster bootstraps and how kube-proxy is
// split between Talos and the CNI (docs/design/architecture.md, "CNI"). preset is immutable:
// swapping a CNI takes the network down and is out of scope. Switching kubeProxy between
// "enabled" and "replaced" on an existing cluster needs the
// ichor.maesh.dev/confirm-kube-proxy-switch annotation (enforced on TalosCluster itself, below,
// since only there is metadata visible to the rule).
// +kubebuilder:validation:XValidation:rule="self.preset == oldSelf.preset",message="cni.preset cannot change after the cluster is created; swapping a CNI takes the network down"
// +kubebuilder:validation:XValidation:rule="self.preset == 'custom' ? has(self.custom) : !has(self.custom)",message="custom must be set when, and only when, preset is custom"
// +kubebuilder:validation:XValidation:rule="self.preset != 'flannel' || self.kubeProxy == 'enabled'",message="flannel cannot replace kube-proxy"
type TalosClusterCNI struct {
	// preset selects the CNI this cluster bootstraps: flannel (Talos's own default; Ichor does
	// nothing), cilium (Helm, with Talos-recommended values built in), or custom (your chart or
	// manifests; you declare the Talos-side settings).
	// +kubebuilder:validation:Enum=flannel;cilium;custom
	// +kubebuilder:default=flannel
	// +optional
	Preset string `json:"preset,omitempty"`

	// kubeProxy is enabled (Talos's own default) or replaced by the CNI. Replacing it splits the
	// setting across two owners -- Talos (cluster.proxy.disabled, written by Ichor) and the CNI
	// (written by Argo CD) -- so a consistency check reads the CNI's live configuration and
	// reports a mismatch as a health failure.
	// +kubebuilder:validation:Enum=enabled;replaced
	// +kubebuilder:default=enabled
	// +optional
	KubeProxy string `json:"kubeProxy,omitempty"`

	// custom declares the chart/manifest source and Talos-side settings for a user-supplied CNI;
	// set when, and only when, preset is "custom".
	// +optional
	Custom *TalosClusterCNICustom `json:"custom,omitempty"`

	// handover controls how the CNI's cluster.inlineManifests entry is removed once Argo CD reports
	// it Synced and Healthy (docs/design/architecture.md, "GitOps and Argo CD"): automatic (the
	// controller removes it on its own), manual (removed only through an operator-triggered
	// `ichor gitops handoff cni`, not built by this field), or disabled (never removed
	// automatically, for a cluster that does not run Argo CD).
	// +kubebuilder:validation:Enum=automatic;manual;disabled
	// +kubebuilder:default=automatic
	// +optional
	Handover string `json:"handover,omitempty"`
}

// TalosClusterDiscovery lists maintenance-mode candidates for the controller to probe directly.
// Empty (the default, on both fields) disables discovery entirely: the controller opens no
// connection at all. Ichor never finds this range itself -- no subnet scan, no listening for a
// beacon -- it only ever dials what is declared here, each on Talos's fixed maintenance API port. "### Trust"
// already assumes a separate provisioning network exists; this is where
// that network is named.
type TalosClusterDiscovery struct {
	// addresses are specific IPv4 candidates to probe, each on Talos's fixed maintenance API port.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=15
	// +kubebuilder:validation:XValidation:rule="self.all(a, isIP(a) && ip(a).family() == 4)",message="addresses must be IPv4 addresses"
	// +optional
	Addresses []string `json:"addresses,omitempty"`

	// cidrs are IPv4 address ranges to probe, one maintenance-mode connection attempt per address
	// in range. Each must be /24 or smaller (at most 256 addresses).
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:items:MaxLength=18
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) && cidr(c).ip().family() == 4 && cidr(c).prefixLength() >= 24)",message="cidrs must be IPv4 and /24 or smaller"
	// +optional
	CIDRs []string `json:"cidrs,omitempty"`
}

// TalosClusterSpec defines the desired state of TalosCluster.
type TalosClusterSpec struct {
	// endpoint is the cluster's control-plane endpoint.
	// +required
	Endpoint TalosClusterEndpoint `json:"endpoint"`

	// talosVersion is the Talos release this cluster renders configuration for.
	// +kubebuilder:validation:Pattern=`^v\d+\.\d+\.\d+$`
	// +required
	TalosVersion string `json:"talosVersion"`

	// kubernetesVersion is the Kubernetes release this cluster runs.
	// +kubebuilder:validation:Pattern=`^v\d+\.\d+\.\d+$`
	// +required
	KubernetesVersion string `json:"kubernetesVersion"`

	// versionContract is required rather than defaulted, so that a machinery bump cannot silently change what gets
	// applied (principle 4).
	// +kubebuilder:validation:Pattern=`^v\d+\.\d+$`
	// +required
	VersionContract string `json:"versionContract"`

	// schematic is the Image Factory schematic used for this cluster's installer image.
	// +optional
	Schematic *TalosClusterSchematic `json:"schematic,omitempty"`

	// secretsRef names the Secret holding the Talos, Kubernetes and etcd CA bundle. The Secret lives in the
	// controller's own namespace (not necessarily this TalosCluster's), because Kubernetes RBAC cannot restrict
	// Secret reads to "wherever the referencing TalosCluster happens to live" at anything narrower than
	// cluster-wide.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="secretsRef.name is required"
	// +required
	SecretsRef corev1.LocalObjectReference `json:"secretsRef"`

	// controlPlane configures the control-plane topology.
	// +required
	ControlPlane TalosClusterControlPlane `json:"controlPlane"`

	// driftPolicy controls what the reconciler does when a node's live configuration no
	// longer matches what Ichor last rendered for it, and nothing on Ichor's side (this
	// TalosCluster, a TalosConfigPatch, or the TalosMachine) has changed since Ichor last
	// confirmed the two matched: warnOnly leaves the node as it is and reports the drift
	// through a condition; autoCorrect re-applies, but only when every changed field is
	// reboot-free (pkg/fieldmeta) -- a reboot-requiring or install-time-only drift is always
	// just reported, regardless of this setting. Defaults to warnOnly: unlike
	// controlPlane.schedulable, the two values here are not wrong half the
	// time each -- silently changing a node is the one that can surprise, so it is never
	// the default.
	// +kubebuilder:validation:Enum=warnOnly;autoCorrect
	// +kubebuilder:default=warnOnly
	// +optional
	DriftPolicy string `json:"driftPolicy,omitempty"`

	// cni configures which CNI preset this cluster bootstraps and how kube-proxy is split
	// between Talos and the CNI (docs/design/architecture.md, "CNI").
	// +optional
	CNI TalosClusterCNI `json:"cni,omitempty"`
	// discovery lists maintenance-mode candidates for the controller to probe directly. Empty (the
	// default) disables discovery: the controller opens no connection at all.
	// +optional
	Discovery *TalosClusterDiscovery `json:"discovery,omitempty"`
}

// TalosClusterStatus defines the observed state of TalosCluster.
type TalosClusterStatus struct {
	// conditions represent the current state of the TalosCluster resource, kstatus-compatible.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// observedGeneration is the most recent generation the controller has reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// TalosClusterConfirmKubeProxySwitchAnnotation, when present on a TalosCluster, allows
// spec.cni.kubeProxy to change on an update that would otherwise be rejected. The guard itself
// is not a CRD x-kubernetes-validations rule: CRD-embedded CEL only exposes metadata.name and
// metadata.generateName on self/oldSelf, never annotations (confirmed against envtest: a rule
// referencing self.metadata.annotations fails CRD creation with "undefined field
// 'annotations'"). It is a ValidatingAdmissionPolicy instead
// (config/admission/kubeproxy-switch-policy.yaml), which sees the whole object including
// annotations. The controller is not a writer of this annotation and does not
// act on it by itself: it only unblocks the API-level guard; running the switch in the one
// safe order -- new side up on every node, verify, then old side down -- is the reconciler's
// job and out of this package's scope.
const TalosClusterConfirmKubeProxySwitchAnnotation = "ichor.maesh.dev/confirm-kube-proxy-switch"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tcl
// +kubebuilder:printcolumn:name="Talos",type=string,JSONPath=".spec.talosVersion"
// +kubebuilder:printcolumn:name="Kubernetes",type=string,JSONPath=".spec.kubernetesVersion"
// +kubebuilder:printcolumn:name="Control Plane",type=integer,JSONPath=".spec.controlPlane.count"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TalosCluster is the Schema for the talosclusters API
type TalosCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TalosCluster
	// +required
	Spec TalosClusterSpec `json:"spec"`

	// status defines the observed state of TalosCluster
	// +optional
	Status TalosClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosClusterList contains a list of TalosCluster
type TalosClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TalosCluster{}, &TalosClusterList{})
		return nil
	})
}
