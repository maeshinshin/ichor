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

// TalosClusterDiskEncryptionConfirmAnnotation confirms that enabling or changing
// spec.security.diskEncryption on a cluster that already has machines is understood to need a
// wipe and rejoin of every node (ADR 0108). Talos encrypts a volume only when it creates it;
// Ichor never wipes a node for this.
const TalosClusterDiskEncryptionConfirmAnnotation = "ichor.maesh.dev/confirm-disk-encryption-wipe"

// TalosClusterSecurity holds the opt-in hardening of the cluster Ichor creates (ADR 0108). Every
// option is off unless set, for new and existing clusters alike; an unset spec.security renders
// exactly what it did before the field existed. docs/guides/hardening.md lists each option, what
// it does and what applying it costs.
type TalosClusterSecurity struct {
	// firewall turns on Talos's ingress firewall in block mode with the rules a Talos cluster
	// needs, plus the sources you list. No reboot.
	// +optional
	Firewall *TalosClusterFirewall `json:"firewall,omitempty"`

	// wireGuard turns on Cilium's transparent WireGuard encryption of pod traffic between nodes.
	// A rolling restart of the Cilium agents, no node reboot.
	// +optional
	WireGuard *TalosClusterWireGuard `json:"wireGuard,omitempty"`

	// podSecurity sets the Pod Security admission levels of the kube-apiserver. Restarts the
	// kube-apiserver on every control plane, no node reboot. Needs versionContract v1.14 or later.
	// +optional
	PodSecurity *TalosClusterPodSecurity `json:"podSecurity,omitempty"`

	// audit selects a kube-apiserver audit policy preset. Restarts the kube-apiserver on every
	// control plane, no node reboot. Needs versionContract v1.14 or later.
	// +optional
	Audit *TalosClusterAudit `json:"audit,omitempty"`

	// diskEncryption encrypts the STATE and EPHEMERAL volumes with LUKS2. Talos encrypts a
	// volume only when it creates it, so on a cluster that already has machines it needs the
	// wipe-and-rejoin path (see docs/guides/hardening.md) and the confirm annotation.
	// +optional
	DiskEncryption *TalosClusterDiskEncryption `json:"diskEncryption,omitempty"`
}

// TalosClusterFirewall is spec.security.firewall. Enabled renders NetworkDefaultActionConfig
// ingress: block and one NetworkRuleConfig per service Talos and Cilium need: apid (50000) from
// controllerCIDRs, talosAPICIDRs and the control planes; trustd (50001) between nodes; kubelet
// (10250) from the control planes and the pod network; etcd (2379-2380) between control planes;
// the Kubernetes API (6443) from controllerCIDRs, apiServerCIDRs, the nodes and the pod and
// service networks; Cilium's tunnel, health check and (if wireGuard is on) WireGuard ports
// between nodes; KubeSpan's port if kubeSpan is set.
// +kubebuilder:validation:XValidation:rule="!self.enabled || (has(self.controllerCIDRs) && self.controllerCIDRs.size() > 0 && has(self.nodeCIDRs) && self.nodeCIDRs.size() > 0)",message="controllerCIDRs and nodeCIDRs need at least one CIDR when enabled: without them Ichor or the nodes would lose access"
type TalosClusterFirewall struct {
	// enabled turns the firewall on.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// controllerCIDRs are the addresses Ichor's controller connects from. They are always allowed
	// to reach apid and the Kubernetes API. The admission webhook refuses a value that does not
	// contain the address the controller reaches the cluster endpoint from.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) && cidr(c).prefixLength() > 0)",message="controllerCIDRs must be valid CIDRs with a prefix length above 0"
	// +listType=atomic
	// +optional
	ControllerCIDRs []string `json:"controllerCIDRs,omitempty"`

	// nodeCIDRs are the addresses of all the cluster's machines, control planes and workers.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) && cidr(c).prefixLength() > 0)",message="nodeCIDRs must be valid CIDRs with a prefix length above 0"
	// +listType=atomic
	// +optional
	NodeCIDRs []string `json:"nodeCIDRs,omitempty"`

	// controlPlaneCIDRs are the addresses of the control planes only; etcd and the control planes'
	// own reach to apid and the kubelet are limited to them. Unset means nodeCIDRs.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c) && cidr(c).prefixLength() > 0)",message="controlPlaneCIDRs must be valid CIDRs with a prefix length above 0"
	// +listType=atomic
	// +optional
	ControlPlaneCIDRs []string `json:"controlPlaneCIDRs,omitempty"`

	// talosAPICIDRs are further sources allowed to reach apid (50000), for operators using talosctl.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c))",message="talosAPICIDRs must be valid CIDRs"
	// +listType=atomic
	// +optional
	TalosAPICIDRs []string `json:"talosAPICIDRs,omitempty"`

	// apiServerCIDRs are further sources allowed to reach the Kubernetes API (6443), for users and
	// load balancers.
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c))",message="apiServerCIDRs must be valid CIDRs"
	// +listType=atomic
	// +optional
	APIServerCIDRs []string `json:"apiServerCIDRs,omitempty"`

	// kubeSpan allows KubeSpan's WireGuard port (UDP 51820) from anywhere; set it when the
	// cluster uses KubeSpan.
	// +optional
	KubeSpan bool `json:"kubeSpan,omitempty"`

	// allow opens further host ports, for services running with host networking (an ingress
	// controller on 80 and 443, a node exporter). Everything not listed is blocked.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	// +optional
	Allow []TalosClusterFirewallRule `json:"allow,omitempty"`
}

// TalosClusterFirewallRule is one extra firewall rule of spec.security.firewall.allow.
type TalosClusterFirewallRule struct {
	// name identifies the rule; it becomes ichor-fw-allow-<name> in the machine config.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]{0,40}[a-z0-9])?$`
	// +required
	Name string `json:"name"`

	// protocol is tcp or udp.
	// +kubebuilder:validation:Enum=tcp;udp
	// +required
	Protocol string `json:"protocol"`

	// ports are single ports ("443") or inclusive ranges ("8000-8100").
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:Pattern=`^[0-9]{1,5}(-[0-9]{1,5})?$`
	// +listType=atomic
	// +required
	Ports []string `json:"ports"`

	// cidrs are the sources allowed to reach the ports.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:XValidation:rule="self.all(c, isCIDR(c))",message="cidrs must be valid CIDRs"
	// +listType=atomic
	// +required
	CIDRs []string `json:"cidrs"`
}

// TalosClusterWireGuard is spec.security.wireGuard: Cilium's encryption.type wireguard (Cilium
// 1.18 encryption-wireguard docs).
type TalosClusterWireGuard struct {
	// enabled sets encryption.enabled=true and encryption.type=wireguard in the managed Cilium
	// values. It wins over any encryption.* the user values set. When the firewall is on, UDP
	// 51871 is opened between nodes.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// nodeEncryption also encrypts node-to-node traffic that is not pod traffic
	// (encryption.nodeEncryption).
	// +optional
	NodeEncryption bool `json:"nodeEncryption,omitempty"`
}

// TalosClusterPodSecurity is spec.security.podSecurity: the defaults of the kube-apiserver's
// PodSecurity admission plugin. A level left unset keeps Talos's default (enforce baseline,
// audit and warn restricted).
type TalosClusterPodSecurity struct {
	// enforce is the level a pod must meet to be admitted.
	// +kubebuilder:validation:Enum=privileged;baseline;restricted
	// +optional
	Enforce string `json:"enforce,omitempty"`

	// audit is the level above which violations are recorded in the audit log.
	// +kubebuilder:validation:Enum=privileged;baseline;restricted
	// +optional
	Audit string `json:"audit,omitempty"`

	// warn is the level above which violations produce a warning to the client.
	// +kubebuilder:validation:Enum=privileged;baseline;restricted
	// +optional
	Warn string `json:"warn,omitempty"`

	// exemptNamespaces are added to the exempt namespaces (kube-system stays exempt).
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=63
	// +listType=set
	// +optional
	ExemptNamespaces []string `json:"exemptNamespaces,omitempty"`
}

// Audit policy presets of spec.security.audit.policy.
const (
	AuditPolicyMetadata   = "metadata"
	AuditPolicyRBACChange = "rbacChanges"
	AuditPolicyWrites     = "writes"
)

// TalosClusterAudit is spec.security.audit.
type TalosClusterAudit struct {
	// policy is the preset. metadata records who did what, without bodies, for every request
	// (Talos's own default). rbacChanges is metadata plus the full request and response of
	// changes to roles and bindings. writes is metadata plus the request body of every change.
	// Secrets, configmaps, service account tokens and token reviews are never logged above
	// metadata in any preset.
	// +kubebuilder:validation:Enum=metadata;rbacChanges;writes
	// +required
	Policy string `json:"policy"`
}

// Disk encryption key types of spec.security.diskEncryption.type.
const (
	DiskEncryptionTPM    = "tpm"
	DiskEncryptionNodeID = "nodeID"
	DiskEncryptionKMS    = "kms"
)

// TalosClusterDiskEncryption is spec.security.diskEncryption. It renders VolumeConfig documents
// for STATE and EPHEMERAL, below every TalosConfigPatch.
// +kubebuilder:validation:XValidation:rule="self.type == 'kms' ? has(self.kmsEndpoint) && size(self.kmsEndpoint) > 0 : !has(self.kmsEndpoint)",message="kmsEndpoint is required for type kms and not allowed otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'tpm' || !has(self.requireSecureBoot)",message="requireSecureBoot applies to type tpm only"
type TalosClusterDiskEncryption struct {
	// type is where the key comes from. tpm seals it in the machine's TPM 2.0 (bound to PCR 7);
	// nodeID derives it from the node UUID, which protects only against a removed disk; kms has
	// a KMS server seal and unseal it.
	// +kubebuilder:validation:Enum=tpm;nodeID;kms
	// +required
	Type string `json:"type"`

	// kmsEndpoint is the KMS server, as a URL with a scheme (https://kms.example.com:4443). A
	// scheme other than https is refused.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	KMSEndpoint string `json:"kmsEndpoint,omitempty"`

	// requireSecureBoot makes enrolling a TPM key fail unless SecureBoot is enabled in the
	// firmware. Without SecureBoot a TPM key gives no boot-chain protection.
	// +optional
	RequireSecureBoot *bool `json:"requireSecureBoot,omitempty"`
}
