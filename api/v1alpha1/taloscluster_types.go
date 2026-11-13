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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TalosClusterEndpoint is the cluster's control-plane endpoint.
type TalosClusterEndpoint struct {
	// type is the kind of address the endpoint resolves through. vip is an address Ichor itself puts on every
	// control plane that has a spec.network, as a shared IP on its primary link; an address something else holds,
	// such as a hardware load balancer or a VIP the cluster already runs, is loadBalancer.
	// +kubebuilder:validation:Enum=vip;loadBalancer;node
	// +required
	Type string `json:"type"`

	// address is the endpoint's address: an IP for vip/node, or an IP or a hostname for loadBalancer.
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

	// omitDefaultNodeLabels renders the control planes without the labels Talos's generator adds
	// to them (node-role.kubernetes.io/control-plane and
	// node.kubernetes.io/exclude-from-external-load-balancers). `ichor adopt` sets it when the
	// control planes it takes over do not carry them, so that adopting does not change the
	// nodes: the exclude label removes a node from Service type=LoadBalancer membership. A
	// cluster Ichor creates leaves it unset and gets Talos's defaults.
	// +optional
	OmitDefaultNodeLabels bool `json:"omitDefaultNodeLabels,omitempty"`
}

// TalosClusterCNICilium holds the user layer of Cilium's Helm values. Ichor merges
// it over its own defaults and then applies its own keys last.
type TalosClusterCNICilium struct {
	// values is the user layer of Cilium's Helm values. Effective values are Ichor's defaults,
	// then these deep-merged on top, then the Ichor-owned keys (ipam.mode,
	// securityContext.capabilities.ciliumAgent, securityContext.capabilities.cleanCiliumState,
	// cgroup.autoMount.enabled, cgroup.hostRoot, k8sServiceHost, k8sServicePort,
	// kubeProxyReplacement, tunnelProtocol); a value for an owned key is a validation error.
	// The merged values must stay under 64 KiB.
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Values *apiextensionsv1.JSON `json:"values,omitempty"`
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

// TalosClusterCNICustom is deprecated and unused (Cilium is the only CNI); it is
// kept so the CRD never drops a field. It declares a user-supplied CNI's chart/manifest source and the Talos-side
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
// +kubebuilder:validation:XValidation:rule="has(oldSelf.tunnel) ? (has(self.tunnel) && self.tunnel == oldSelf.tunnel) : (!has(self.tunnel) || self.tunnel == 'vxlan')",message="cni.tunnel cannot change once set, and an existing cluster can only record vxlan (its running default); changing the tunnel protocol takes pod networking down"
// +kubebuilder:validation:XValidation:rule="!has(self.tunnel) || self.preset == 'cilium'",message="cni.tunnel only applies to the cilium preset"
// +kubebuilder:validation:XValidation:rule="self.preset != 'unmanaged' || !has(self.cilium)",message="cni.cilium only applies to the cilium preset; Ichor does not manage the CNI when preset is unmanaged"
// +kubebuilder:validation:XValidation:rule="!has(self.unmanaged) || self.preset == 'unmanaged'",message="cni.unmanaged only applies to the unmanaged preset"
type TalosClusterCNI struct {
	// preset selects the CNI this cluster bootstraps. Cilium is the only CNI Ichor installs; unmanaged leaves the CNI
	// of an adopted cluster exactly as it is, and
	// the webhook accepts it on create only for an object carrying the adoption annotation. The
	// values flannel and custom are deprecated, unused and kept so the CRD never drops a value.
	// +kubebuilder:validation:Enum=flannel;cilium;custom;unmanaged
	// +kubebuilder:default=cilium
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

	// tunnel is Cilium's tunnel protocol. It only applies to the cilium preset; empty means
	// Cilium's default (vxlan). Immutable once set.
	// +kubebuilder:validation:Enum=vxlan;geneve
	// +optional
	Tunnel string `json:"tunnel,omitempty"`

	// custom is deprecated, unused; kept so the CRD never drops a field. It declared
	// the chart/manifest source and Talos-side settings for a user-supplied CNI.
	// +optional
	Custom *TalosClusterCNICustom `json:"custom,omitempty"`

	// cilium holds the user layer of Cilium's Helm values.
	// +optional
	Cilium *TalosClusterCNICilium `json:"cilium,omitempty"`

	// unmanaged configures the unmanaged preset. Only valid when preset is unmanaged;
	// when omitted there, talosCNI is default.
	// +optional
	Unmanaged *TalosClusterCNIUnmanaged `json:"unmanaged,omitempty"`
}

// TalosClusterCNIUnmanaged records what an adopted cluster's machine config says about the CNI
// so the render reproduces it.
type TalosClusterCNIUnmanaged struct {
	// talosCNI is default (the machine config has no CNI selection, so Talos runs its own
	// flannel or whatever cluster.network.cni selects by default; render emits nothing about the
	// CNI) or none (render emits cluster.network.cni.name: none).
	// +kubebuilder:validation:Enum=default;none
	// +kubebuilder:default=default
	// +optional
	TalosCNI string `json:"talosCNI,omitempty"`
}

// TalosClusterRegistry declares one private registry mirror this cluster's nodes pull through,
// and where its pull credentials live. The mirror endpoints and (non-secret) username are typed
// fields, same as any other machine-config value; the password-shaped credentials
// (RegistryAuthConfig.{password,auth,identityToken}) are not, because they would let this value
// -- carried on TalosCluster, a CR that lives in Git -- put a registry password in Git (design
// principle 2). authSecretRef points at a Kubernetes Secret
// instead; the render-time injection that resolves it never writes the resolved value back onto
// any CR (pkg/render.ResolveRegistryAuth, pkg/render.Render).
type TalosClusterRegistry struct {
	// host is the registry host (and port, if not the default) this entry's mirror and
	// credentials apply to, matching RegistriesConfig's own map key: the first segment of an
	// image reference, "docker.io" being the default one, or "*" for a fallback mirror. TLS
	// config (RegistryConfig.tls) uses the same key but never the "*" fallback; this field
	// carries both mirror and auth, so "*" is only meaningful when authSecretRef is unset.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="self == '*' || self.matches('^[a-zA-Z0-9.-]+(:[0-9]{1,5})?$')",message="host must be \"*\" or a hostname[:port]"
	// +required
	Host string `json:"host"`

	// endpoints are the mirror endpoint URLs for host (RegistryMirrorConfig.endpoints) -- not
	// secret, so they stay a plain typed field here rather than behind a reference. Checked
	// here, not left to machinery: pkg/machinery/config/types/v1alpha1's own ValidateAtRuntime
	// has no validator at all for RegistryMirrorConfig/RegistryConfig, so an
	// endpoint shaped wrong would otherwise reach a node's machine config unexamined, exactly
	// the gap spec.discovery's own CEL above already closes for its addresses.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="self.all(e, isURL(e))",message="endpoints must be valid URLs"
	// +required
	Endpoints []string `json:"endpoints"`

	// username is this registry's non-secret auth username (RegistryAuthConfig.username). Optional: some private
	// registries authenticate on password/token alone.
	// +optional
	Username string `json:"username,omitempty"`

	// authSecretRef names a Secret, read in the controller's own namespace (the same reasoning
	// as secretsRef below: RBAC cannot scope a Secret read to "wherever this TalosCluster
	// happens to live" below cluster-wide), holding this registry's password-shaped
	// credentials. Unset means an anonymous mirror -- a public registry needing no credentials
	// at all, not an error. Set but unresolvable (Secret missing, or present but carrying none
	// of its three recognized keys) is: pkg/render.ResolveRegistryAuth fails the render rather
	// than silently producing a mirror with no auth, since that would look identical to a
	// working one until the first pull fails on the node. A corev1.LocalObjectReference, not a
	// SecretKeySelector: machinery's RegistryAuthConfig carries three mutually-exclusive
	// credential shapes (password, auth, identityToken, matching docker config.json's own auth
	// forms) that one registry's credentials pick at most one of, so one reference to a Secret
	// with those three well-known keys covers it -- same shape secretsRef already uses for
	// bundleSecretKey, rather than three separate single-key selectors for a value that is, in
	// practice, one secret.
	// +optional
	AuthSecretRef *corev1.LocalObjectReference `json:"authSecretRef,omitempty"`

	// skipFallback stops nodes falling back to host itself when none of the endpoints has the
	// image (RegistryMirrorConfig.skipFallback). Default false, Talos's own default.
	// +optional
	SkipFallback bool `json:"skipFallback,omitempty"`

	// overridePath makes the nodes use every endpoint's path as given, without adding the
	// /v2/ suffix (RegistryMirrorConfig.endpoints[].overridePath). It applies to all of this
	// entry's endpoints; Talos allows it per endpoint, and a mix cannot be recorded here.
	// +optional
	OverridePath bool `json:"overridePath,omitempty"`
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

// TalosClusterControlPlaneMetrics exposes the control-plane metrics Talos binds to localhost by
// default (kube-controller-manager, kube-scheduler, etcd) and restricts who can scrape them with
// Talos's ingress firewall. Unset, or enabled false (the default), changes nothing.
// +kubebuilder:validation:XValidation:rule="!self.enabled || (has(self.allowedCIDRs) && self.allowedCIDRs.size() > 0)",message="allowedCIDRs needs at least one CIDR when enabled: the metrics are never exposed without a source restriction"
type TalosClusterControlPlaneMetrics struct {
	// enabled binds the metrics endpoints on all addresses and adds ingress firewall rules that
	// drop connections to those ports from any source outside allowedCIDRs.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// allowedCIDRs are the only sources that may reach the metrics ports (TCP 10257, 10259 and
	// 2381) on control-plane nodes. Every other port keeps Talos's default firewall action
	// (accept). A prefix length of 0 is rejected: that would be no restriction at all.
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) && cidr(c).prefixLength() > 0)",message="allowedCIDRs must be valid CIDRs with a prefix length above 0"
	// +optional
	AllowedCIDRs []string `json:"allowedCIDRs,omitempty"`
}

// TalosClusterNetwork is the cluster's pod and service address ranges. Both lists
// are optional; an empty list keeps Talos's default for that range (10.244.0.0/16 for pods,
// 10.96.0.0/12 for services). The ranges are fixed when the cluster is created: Kubernetes
// cannot renumber a live cluster, so the whole field is immutable once set (a cluster that
// never declared network may set it once, to record what an adopted cluster already runs).
type TalosClusterNetwork struct {
	// podSubnets are the pod CIDRs, at most one per IP family.
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c))",message="podSubnets must be valid CIDRs"
	// +listType=atomic
	// +optional
	PodSubnets []string `json:"podSubnets,omitempty"`

	// serviceSubnets are the service CIDRs, at most one per IP family.
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c))",message="serviceSubnets must be valid CIDRs"
	// +listType=atomic
	// +optional
	ServiceSubnets []string `json:"serviceSubnets,omitempty"`
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
	// the default. The CRD carries no default: the defaulting webhook fills warnOnly, or the
	// installation's spec.defaults.driftPolicy, at create; an empty value is warnOnly.
	// +kubebuilder:validation:Enum=warnOnly;autoCorrect
	// +optional
	DriftPolicy string `json:"driftPolicy,omitempty"`

	// cni configures which CNI preset this cluster bootstraps and how kube-proxy is split
	// between Talos and the CNI (docs/design/architecture.md, "CNI").
	// +optional
	CNI TalosClusterCNI `json:"cni,omitempty"`

	// network sets the pod and service subnets. Unset keeps Talos's defaults. It
	// cannot change once set.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="network cannot change after it is set; Kubernetes cannot renumber a live cluster"
	// +optional
	Network *TalosClusterNetwork `json:"network,omitempty"`

	// discovery lists maintenance-mode candidates for the controller to probe directly. Empty (the
	// default) disables discovery: the controller opens no connection at all.
	// +optional
	Discovery *TalosClusterDiscovery `json:"discovery,omitempty"`

	// controlPlaneMetrics exposes the control-plane metrics Talos binds locally and restricts
	// scraping to a CIDR. Unset (the default) leaves the Talos defaults alone.
	// +optional
	ControlPlaneMetrics *TalosClusterControlPlaneMetrics `json:"controlPlaneMetrics,omitempty"`

	// certSANs are extra subject alternative names (hostnames, IPs) for the Talos API certificate
	// of every node and for the kube-apiserver serving certificate: machine.certSANs and
	// KubeAPIServerConfig.certExtraSANs, which Talos's generator writes together from one list.
	// The cluster endpoint host is not added for you here; list it if the nodes should serve it.
	// Unset adds none.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=253
	// +optional
	CertSANs []string `json:"certSANs,omitempty"`

	// registries declares this cluster's private registry mirrors and where their pull
	// credentials live, pushed to every node through the system layer (docs/design/architecture.md "Monitoring and
	// registry mirrors"). Empty (the default) configures no
	// mirrors at all -- mirrors stay entirely optional. listType=map on host (rather
	// than a CEL uniqueness rule, same as status.conditions below) is what the structural schema
	// itself rejects a duplicate host under -- a CEL self.exists_one rule over this list was
	// tried first and rejected by envtest's apiserver: with MaxItems=32 its estimated cost
	// exceeds the per-field budget by 32x (quadratic in list length), and still contributed
	// enough to blow the whole-schema budget by 3.3x alongside this CRD's existing CEL rules.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=host
	// +optional
	Registries []TalosClusterRegistry `json:"registries,omitempty"`

	// authentication declares who the kube-apiserver trusts to sign users in with a JWT: Ichor's
	// own issuer and up to three external OIDC providers. It is rendered into Talos's
	// KubeAuthenticationConfig document, so it needs versionContract v1.14 or later, and changing
	// it needs the update verb on talosclusters/authentication. Unset (the default) renders the
	// Talos default: no JWT authenticator, anonymous access to the health endpoints only.
	// +optional
	Authentication *TalosClusterAuthentication `json:"authentication,omitempty"`

	// security holds opt-in hardening of this cluster: an ingress firewall, Cilium
	// WireGuard encryption, Pod Security levels, an audit policy preset and disk encryption.
	// Unset (the default) changes nothing.
	// +optional
	Security *TalosClusterSecurity `json:"security,omitempty"`
}

// TalosClusterAuthentication is spec.authentication. Each enabled entry
// becomes one jwt[] element of the kube-apiserver's AuthenticationConfiguration; issuer URLs
// must be unique across Ichor's and the external ones.
type TalosClusterAuthentication struct {
	// ichor turns on Ichor's own OIDC issuer (ichor-ui) as a kube-apiserver authenticator.
	// Users appear as ichor:<name>, groups as ichor:<role>.
	// +optional
	Ichor *TalosClusterIchorIssuer `json:"ichor,omitempty"`

	// external lists the external OIDC providers the kube-apiserver also trusts. Keep it equal
	// to ichorUI.oidc.*: that is what the UI signs in against, this is what the
	// kube-apiserver accepts.
	// +kubebuilder:validation:MaxItems=3
	// +listType=map
	// +listMapKey=issuerURL
	// +optional
	External []TalosClusterExternalIssuer `json:"external,omitempty"`
}

// TalosClusterIchorIssuer enables Ichor's issuer.
type TalosClusterIchorIssuer struct {
	// enabled renders Ichor's issuer into the kube-apiserver. It needs the trust ConfigMap
	// ichor-oidc-trust (the controller publishes it); without it the render fails and says so.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// url is the external https URL of ichor-ui. It is the issuer (the iss claim of every token).
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^https://[^\s/?#@]+(/[^\s?#]*)?$`
	// +required
	URL string `json:"url"`
}

// TalosClusterExternalIssuer is one external OIDC provider (as the kube-apiserver sees it).
type TalosClusterExternalIssuer struct {
	// issuerURL is the provider's issuer, an https URL without query or fragment.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^https://[^\s/?#@]+(/[^\s?#]*)?$`
	// +required
	IssuerURL string `json:"issuerURL"`

	// audiences are the accepted aud values (the OIDC client IDs).
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=253
	// +required
	Audiences []string `json:"audiences"`

	// usernameClaim is the token claim that names the user.
	// +kubebuilder:default=email
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +optional
	UsernameClaim string `json:"usernameClaim,omitempty"`

	// groupsClaim is the token claim that lists the user's groups.
	// +kubebuilder:default=groups
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +optional
	GroupsClaim string `json:"groupsClaim,omitempty"`

	// usernamePrefix is put in front of every username. It cannot be empty or start with
	// "ichor:", so an external user can never be mistaken for an Ichor user.
	// +kubebuilder:default="oidc:"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('ichor:')",message="the prefix ichor: is reserved for Ichor's own users"
	// +optional
	UsernamePrefix string `json:"usernamePrefix,omitempty"`

	// groupsPrefix is put in front of every group, with the same restrictions as usernamePrefix.
	// +kubebuilder:default="oidc:"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('ichor:')",message="the prefix ichor: is reserved for Ichor's own groups"
	// +optional
	GroupsPrefix string `json:"groupsPrefix,omitempty"`

	// certificateAuthority is a PEM bundle (public certificates only) that verifies the provider's
	// TLS certificate. Empty uses the control plane's system roots.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	CertificateAuthority string `json:"certificateAuthority,omitempty"`
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

	// certificates lists when each cluster certificate authority in the secrets
	// bundle expires, so an approaching expiry is visible before it locks anyone
	// out. Read from the bundle, never from a node.
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=name
	// +optional
	Certificates []CertificateExpiry `json:"certificates,omitempty" patchStrategy:"merge" patchMergeKey:"name"`

	// cni is the observed CNI state that is not a spec ingredient.
	// +optional
	CNI *TalosClusterCNIStatus `json:"cni,omitempty"`
}

// TalosCiliumImageOverride is the Cilium patch whose images an earlier Ichor ran on the embedded chart. Ichor sets
// no new one (amendment 2026-11-04 (3)); a recorded one stays until the embedded
// chart reaches it.
type TalosCiliumImageOverride struct {
	// version is the patch, with a leading v (v1.20.5).
	// +kubebuilder:validation:Pattern=`^v[0-9]+\.[0-9]+\.[0-9]+$`
	// +required
	Version string `json:"version"`

	// images maps each image key to the reference the Job runs: "agent", "operator" and "hubble-relay"
	// are repository:tag@sha256:digest, verified against the Cilium release signature.
	// +required
	Images map[string]string `json:"images"`
}

// TalosClusterCNIStatus holds what the kube-proxy switch has done to the Talos side.
type TalosClusterCNIStatus struct {
	// kubeProxy is the mode the Talos side (cluster.proxy.disabled) is rendered for. Unset until
	// a kube-proxy switch first runs, which means "render whatever the patches say"; once set,
	// pkg/render appends it as the last patch, so it wins over any patch. Only the
	// TalosOperation of kind cni writes it, and only after the new side is verified.
	// +kubebuilder:validation:Enum=enabled;replaced
	// +optional
	KubeProxy string `json:"kubeProxy,omitempty"`

	// availableChartVersion is the Cilium chart version the running Ichor ships when it differs
	// from the installed release; empty when up to date. It backs the
	// CiliumUpgradeAvailable condition.
	// +optional
	AvailableChartVersion string `json:"availableChartVersion,omitempty"`

	// imageOverride is the Cilium patch an earlier Ichor recorded as the images the release runs over the embedded
	// chart. Ichor sets no new one (amendment 2026-11-04 (3)). A recorded patch
	// newer than the embedded chart keeps being rendered, so an Ichor upgrade never steps Cilium back, and the
	// CiliumUpgradeAvailable condition says so; the controller clears it once the embedded chart reaches the patch.
	// +optional
	ImageOverride *TalosCiliumImageOverride `json:"imageOverride,omitempty"`

	// appliedValuesHash is the hash of the effective Cilium values the last successful Helm Job
	// applied. The controller starts a new Job when the hash of
	// spec.cni.cilium.values (merged with Ichor's defaults) differs from it. Written by the
	// controller only.
	// +optional
	AppliedValuesHash string `json:"appliedValuesHash,omitempty"`

	// jobFailures is how many Cilium Helm Jobs failed in a row for the values whose hash is
	// jobFailuresHash, counted when the controller removes a failed Job to try again. Each failure
	// doubles the wait before the next attempt (a minute, up to 30 minutes), so a Job that keeps failing
	// does not hold the cluster operation lock in a near-continuous loop. Other values (another hash)
	// start again at once, and a successful Job clears it. Written by the controller only.
	// +optional
	JobFailures int32 `json:"jobFailures,omitempty"`

	// jobFailuresHash is the effective-values hash jobFailures counts for.
	// +optional
	JobFailuresHash string `json:"jobFailuresHash,omitempty"`

	// detected is the CNI the cluster is observed to run, read-only display and the
	// network-policy gate. Written by the controller only.
	// +kubebuilder:validation:Enum=cilium;flannel;calico;other;none
	// +optional
	Detected string `json:"detected,omitempty"`

	// detectedVersion is the image tag of the detected CNI's DaemonSet where one exposes it.
	// +optional
	DetectedVersion string `json:"detectedVersion,omitempty"`

	// takenOverInlineManifest is the name of the cluster.inlineManifests entry of the adopted
	// machines that held the Cilium DaemonSet Ichor took over, which pkg/render stops rendering
	// once CNIHandover is True. The controller copies it from the
	// TalosClusterCNIInlineManifestAnnotation when the hand-over completes; empty when the
	// adopted Cilium was not in an inline manifest.
	// +optional
	TakenOverInlineManifest string `json:"takenOverInlineManifest,omitempty"`
}

// TalosClusterCNIInlineManifestAnnotation carries, on a TalosCluster that `ichor adopt` created,
// the name of the Talos cluster.inlineManifests entry that holds the Cilium DaemonSet (found by
// content). The controller copies it to status.cni.takenOverInlineManifest once the hand-over has
// completed; render reads only the status, so nothing is dropped from a machine's config before.
const TalosClusterCNIInlineManifestAnnotation = "ichor.maesh.dev/cni-inline-manifest"

// TalosClusterAdoptedAnnotation marks a TalosCluster that `ichor adopt` created. The
// webhook requires it on create for spec.cni.preset: unmanaged.
const TalosClusterAdoptedAnnotation = "ichor.maesh.dev/adopted"

// CNINotManagedMessage is the refusal for anything that would operate the CNI of a cluster whose
// spec.cni.preset is unmanaged.
const CNINotManagedMessage = "the CNI is not managed by Ichor (spec.cni.preset: unmanaged)"

// ConditionCARotationAdvised is True, reason Adopted, on an adopted cluster until the first
// completed CA rotation or the user dismisses the advice. Advice only.
const ConditionCARotationAdvised = "CARotationAdvised"

// ConditionNetworkPoliciesApplied is False, reason CNINotCilium, when the detected CNI is not
// Cilium, so Ichor's CiliumNetworkPolicies are skipped. The message reads
// "Ichor's network policies are not applied: the CNI is <detected>". Absent when the CNI is Cilium.
const ConditionNetworkPoliciesApplied = "NetworkPoliciesApplied"

// TalosClusterCARotationAdviceDismissedAnnotation is set to "true" by the user (UI or CLI) to
// dismiss the CARotationAdvised advice; the controller clears the condition when it sees it.
// It is an annotation, not a spec field.
const TalosClusterCARotationAdviceDismissedAnnotation = "ichor.maesh.dev/ca-rotation-advice-dismissed"

// TalosClusterCNIPresetUnmanaged is the value of spec.cni.preset for a cluster whose CNI Ichor
// leaves exactly as it is.
const TalosClusterCNIPresetUnmanaged = "unmanaged"

// Condition types added to TalosCluster.status.conditions.
const (
	// ConditionCNIUnsupported is True when the cluster's preset is not cilium; the controllers
	// refuse to render it and change nothing on its nodes.
	ConditionCNIUnsupported = "CNIUnsupported"

	// ConditionCiliumUpgradeAvailable is True when status.cni.availableChartVersion is set.
	ConditionCiliumUpgradeAvailable = "CiliumUpgradeAvailable"

	// ConditionCNIValuesApplied is True when the Cilium Helm release is deployed at the effective
	// values status.cni.appliedValuesHash names. False while a Helm Job runs or waits for the
	// cluster operation lock, and with the Job's last log line or reason when it failed.
	ConditionCNIValuesApplied = "CNIValuesApplied"
)

// CertificateExpiry is one certificate authority's end of validity.
type CertificateExpiry struct {
	// name is which authority: talos, kubernetes, kubernetes-aggregator or etcd.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// notAfter is the certificate's last instant of validity.
	// +required
	NotAfter metav1.Time `json:"notAfter"`
}

// TalosClusterConfirmKubeProxySwitchAnnotation, when its value equals the new spec.cni.kubeProxy
// ("replaced" or "enabled"), allows spec.cni.kubeProxy to change on an update that would otherwise
// be rejected. A leftover annotation therefore never admits a different later switch. The guard itself
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
		s.AddKnownTypes(GroupVersion, &TalosCluster{}, &TalosClusterList{})
		return nil
	})
}

// ConditionCertificatesExpiring is the TalosCluster condition that is True while any CA in the
// secrets bundle expires within the controller's warning window, or has expired.
const ConditionCertificatesExpiring = "CertificatesExpiring"

// ConditionKubeletServingCertificates is the TalosCluster condition reported while
// spec.security.kubeletServingCertificates is enabled: True when every machine has
// been issued a kubelet serving certificate by the Kubernetes CA, False naming the machines that
// have not. It is removed when the option is off.
const ConditionKubeletServingCertificates = "KubeletServingCertificates"

// ConditionKubeletServingVerified latches the kube-apiserver's --kubelet-certificate-authority on. Ichor sets it
// True once verifyFromAPIServer is set and KubeletServingCertificates
// is True, and removes it when either is switched off. Render reads it, so a node that joins
// later does not take the flag away again.
const ConditionKubeletServingVerified = "KubeletServingVerified"
