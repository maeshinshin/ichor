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

// TalosMachineIdentity identifies the physical machine backing a TalosMachine.
type TalosMachineIdentity struct {
	// uuid is the machine's SMBIOS UUID, the primary key for identity.
	// +kubebuilder:validation:MinLength=1
	// +required
	UUID string `json:"uuid"`

	// serial is the machine's SMBIOS serial number, used as an identity hint.
	// +optional
	Serial string `json:"serial,omitempty"`

	// macAddresses are the machine's NIC MAC addresses, used as an identity hint.
	// +optional
	MACAddresses []string `json:"macAddresses,omitempty"`
}

// IsStrongIdentity reports whether m has every element of
// docs/design/architecture.md's "Nodes" identity tuple -- SMBIOS UUID,
// serial number, and at least one permanent MAC address. Any missing
// element is a weak key, and Ichor does not auto-register on one
// (discovery, internal/controller) or let approval depend on discovery
// having already enforced it (internal/uiapi, ADR 0011's backstop) --
// both call this one method rather than each keeping its own copy of the
// rule (ADR 0033), so a change to what counts as "strong" cannot update
// one gate and silently miss the other.
func (m TalosMachineIdentity) IsStrongIdentity() bool {
	return m.UUID != "" && m.Serial != "" && len(m.MACAddresses) > 0
}

// Values for TalosMachineSpec.ApprovalState.
const (
	// MachineApprovalStatePending is the default: discovered, awaiting a
	// human decision. The controller renders and applies nothing for it.
	MachineApprovalStatePending = "pending"

	// MachineApprovalStateApproved lets the controller render and apply
	// configuration (TalosMachineReconciler's own gate, via IsApproved).
	MachineApprovalStateApproved = "approved"

	// MachineApprovalStateRejected means an operator explicitly turned this
	// machine away. It is distinct from pending so a rejected machine does
	// not keep showing up as a fresh approval request
	// (docs/adr/0018): ListMachines reports it, but it is never recreated
	// by discovery (which keys on identity, not approval state) and never
	// rendered. Re-approving a rejected machine is allowed and reverses it.
	MachineApprovalStateRejected = "rejected"
)

// TalosMachineSpec defines the desired state of TalosMachine.
type TalosMachineSpec struct {
	// clusterRef names the TalosCluster this machine belongs to.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="clusterRef.name is required"
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// identity identifies the physical machine backing this resource.
	// +required
	Identity TalosMachineIdentity `json:"identity"`

	// endpoint is the machine's Talos API address.
	// +kubebuilder:validation:MinLength=1
	// +required
	Endpoint string `json:"endpoint"`

	// role is the machine's role in the cluster. Left unset by discovery, which
	// does not know it yet (ADR 0011); approval must set it before the
	// configuration reconciler can render anything for this machine.
	// +kubebuilder:validation:Enum=controlplane;worker
	// +optional
	Role string `json:"role,omitempty"`

	// nodeLabels are applied to the Kubernetes Node, not to this resource, which is why the field isn't called "labels" (ADR 0004).
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// hostname overrides the Kubernetes Node name; Talos derives one from the machine if unset.
	// It must be an RFC 1123 DNS label or subdomain (lower-case alphanumerics, '-' and '.').
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// install is where Talos installs itself on this machine (the design's `system` disk role,
	// ADR 0077). Unset renders no install configuration, which leaves a machine that boots from
	// an installed disk alone but gives a maintenance-mode machine nothing to install onto.
	// +optional
	Install *TalosMachineInstall `json:"install,omitempty"`

	// network is the machine's primary network link and how it is addressed (ADR 0092). Unset
	// leaves networking to Talos's default (DHCP on every physical link).
	// +optional
	Network *TalosMachineNetwork `json:"network,omitempty"`

	// disks assign roles to disks other than the system disk (spec.install): ephemeral puts
	// EPHEMERAL on its own disk, userVolume provisions a named user volume (ADR 0092).
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:XValidation:rule="self.filter(d, d.role == 'ephemeral').size() <= 1",message="at most one disk can have the ephemeral role"
	// +kubebuilder:validation:XValidation:rule="self.all(a, !(a.role == 'userVolume' && has(a.name)) || self.filter(b, b.role == 'userVolume' && has(b.name) && b.name == a.name).size() == 1)",message="userVolume names must be unique"
	// +listType=atomic
	// +optional
	Disks []TalosMachineDiskRole `json:"disks,omitempty"`

	// approvalState records the human decision on this machine: pending (the
	// default, awaiting a decision), approved (the controller renders and
	// applies configuration), or rejected (an operator explicitly turned it
	// away; re-approving reverses it, docs/adr/0018). This replaces an
	// earlier plain "approved" bool, which had no way to represent
	// "rejected" without a second field claiming the same truth -- one
	// field is the source of truth for this machine's approval status.
	// +kubebuilder:validation:Enum=pending;approved;rejected
	// +kubebuilder:default=pending
	// +optional
	ApprovalState string `json:"approvalState,omitempty"`

	// approvalDecision records who made the current approvalState decision,
	// when, and why. It is kept separate from approvalState, not folded
	// into it, so approvalState alone remains this machine's single source
	// of truth for approval status -- this struct asserts no approved/
	// rejected state of its own, only the record behind whichever state
	// approvalState already carries. Unset until the first approve/reject.
	// +optional
	ApprovalDecision *TalosMachineApprovalDecision `json:"approvalDecision,omitempty"`
}

// TalosMachineInstall is the install target of one machine, rendered by pkg/render as the
// UnattendedInstallConfig document from the v1.14 contract and as machine.install before.
type TalosMachineInstall struct {
	// disk selects the system disk by a stable identifier. Required: the v1.14 install document
	// has no meaning without a disk to install onto.
	// +required
	Disk TalosMachineDisk `json:"disk"`

	// image is the installer image reference. Unset lets Talos use the installer of the
	// schematic it booted from, which only works when it booted an Image Factory asset.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Image string `json:"image,omitempty"`

	// wipe is the install-time wipe flag of the node's configuration (UnattendedInstallConfig
	// provisioning.wipe, machine.install.wipe before v1.14). It is read only when Talos
	// installs, so on a running node it records what the configuration says and does nothing.
	// Default false, which is what Talos's generator writes.
	// +optional
	Wipe bool `json:"wipe,omitempty"`
}

// TalosMachineDisk selects one disk. Exactly one field is set. The first four follow the
// design's identification fallback chain (WWID, serial, by-id path, bus path); devPath is the
// last resort, because the kernel can renumber it between boots.
// +kubebuilder:validation:XValidation:rule="(has(self.wwid) ? 1 : 0) + (has(self.serial) ? 1 : 0) + (has(self.byIDPath) ? 1 : 0) + (has(self.busPath) ? 1 : 0) + (has(self.devPath) ? 1 : 0) == 1",message="exactly one of wwid, serial, byIDPath, busPath or devPath must be set"
type TalosMachineDisk struct {
	// wwid is the disk's World Wide Identifier.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +optional
	WWID string `json:"wwid,omitempty"`

	// serial is the disk's serial number.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Serial string `json:"serial,omitempty"`

	// byIDPath is a /dev/disk/by-id/ symlink of the disk.
	// +kubebuilder:validation:Pattern=`^/dev/disk/by-id/[^/\s"]+$`
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ByIDPath string `json:"byIDPath,omitempty"`

	// busPath is the disk's bus path, e.g. /pci0000:00/0000:00:17.0/ata1/host0/target0:0:0/0:0:0:0.
	// +kubebuilder:validation:Pattern=`^/[^"\s]+$`
	// +kubebuilder:validation:MaxLength=256
	// +optional
	BusPath string `json:"busPath,omitempty"`

	// devPath is the kernel device path, e.g. /dev/sda. Not stable across boots; prefer the others.
	// +kubebuilder:validation:Pattern=`^/dev/[^"\s]+$`
	// +kubebuilder:validation:MaxLength=256
	// +optional
	DevPath string `json:"devPath,omitempty"`
}

// Values for TalosMachineNetwork.Addressing and TalosMachineDiskRole.Role.
const (
	MachineAddressingDHCP   = "dhcp"
	MachineAddressingStatic = "static"

	MachineDiskRoleEphemeral  = "ephemeral"
	MachineDiskRoleUserVolume = "userVolume"
)

// TalosMachineNetwork is the primary link of one machine and its addressing. The link is picked
// by permanent MAC, which survives kernel interface renames; pkg/render emits a LinkAliasConfig
// (alias net0) whose CEL selector matches it, and the other documents refer to the alias.
// +kubebuilder:validation:XValidation:rule="(has(self.addressing) ? self.addressing : 'dhcp') == 'static' ? (has(self.addresses) && size(self.addresses) > 0) : (!has(self.addresses) && !has(self.gateway))",message="static addressing needs addresses; dhcp takes no addresses or gateway"
type TalosMachineNetwork struct {
	// mac is the link's permanent hardware address, as discovery reported it.
	// +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`
	// +required
	MAC string `json:"mac"`

	// addressing is dhcp (the default) or static.
	// +kubebuilder:validation:Enum=dhcp;static
	// +kubebuilder:default=dhcp
	// +optional
	Addressing string `json:"addressing,omitempty"`

	// addresses are the static addresses with their prefix length, e.g. 192.168.1.10/24.
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:items:XValidation:rule="isCIDR(self)",message="each address must be an IP with a prefix length, like 192.168.1.10/24"
	// +listType=atomic
	// +optional
	Addresses []string `json:"addresses,omitempty"`

	// gateway is the default gateway; unset adds no default route.
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="gateway must be an IP address"
	// +optional
	Gateway string `json:"gateway,omitempty"`

	// dnsServers are resolver addresses, in order. Valid with either addressing.
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:items:XValidation:rule="isIP(self)",message="each DNS server must be an IP address"
	// +listType=atomic
	// +optional
	DNSServers []string `json:"dnsServers,omitempty"`

	// vlanID puts the addressing on a VLAN of the link instead of the link itself.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=4094
	// +optional
	VLANID int32 `json:"vlanID,omitempty"`
}

// TalosMachineDiskRole gives one non-system disk a role (ADR 0092). The disk is identified the
// same way as spec.install.disk.
// +kubebuilder:validation:XValidation:rule="self.role == 'userVolume' ? has(self.name) : (!has(self.name) && !has(self.maxSize))",message="name is required for userVolume and not allowed for ephemeral, and so is maxSize"
type TalosMachineDiskRole struct {
	// role is ephemeral (EPHEMERAL on this disk) or userVolume (a user volume on it).
	// +kubebuilder:validation:Enum=ephemeral;userVolume
	// +required
	Role string `json:"role"`

	// disk selects the disk.
	// +required
	Disk TalosMachineDisk `json:"disk"`

	// name is the user volume's name; it is mounted at /var/mnt/<name>.
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9-]+$`
	// +kubebuilder:validation:MaxLength=32
	// +optional
	Name string `json:"name,omitempty"`

	// maxSize caps a user volume, e.g. 200GiB. Unset lets it use the whole disk.
	// +kubebuilder:validation:Pattern=`^[1-9][0-9]*(MiB|GiB|TiB)$`
	// +optional
	MaxSize string `json:"maxSize,omitempty"`
}

// TalosMachineApprovalDecision is who decided a TalosMachine's current
// approvalState, when, and why -- not a second place that state can be
// read from (see TalosMachineSpec.ApprovalDecision's own comment).
type TalosMachineApprovalDecision struct {
	// actor identifies who made the decision. Fixed to the backend's own
	// identity until OIDC login lands (ADR 0018); the field is added now
	// rather than later so every decision from here on has an actor
	// recorded, instead of leaving earlier decisions with none once a
	// real per-person identity exists to fill it.
	// +optional
	Actor string `json:"actor,omitempty"`

	// reason is optional free text, symmetric between approve and reject --
	// AuditRecord's own wire comment already documents "reason" as "mainly
	// used for rejections," not exclusive to them.
	// +optional
	Reason string `json:"reason,omitempty"`

	// at is when the decision was made.
	// +optional
	At metav1.Time `json:"at,omitempty"`
}

// IsApproved reports whether the controller may render and apply
// configuration to this machine -- TalosMachineReconciler's own gate, and
// the one place that check should be made rather than comparing
// ApprovalState's string value directly.
func (s TalosMachineSpec) IsApproved() bool {
	return s.ApprovalState == MachineApprovalStateApproved
}

// DialEndpoint is the address to dial the machine's Talos API at: the address the controller
// last observed for it while that still supersedes spec.endpoint, else spec.endpoint itself.
func (m *TalosMachine) DialEndpoint() string {
	if m.Status.ObservedEndpoint != "" && m.Status.ObservedEndpointFor == m.Spec.Endpoint {
		return m.Status.ObservedEndpoint
	}

	return m.Spec.Endpoint
}

// TalosMachineStatus defines the observed state of TalosMachine.
type TalosMachineStatus struct {
	// conditions represent the current state of the TalosMachine resource, kstatus-compatible.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// observedGeneration is the most recent generation the controller has reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// appliedConfigHash is the sha256 of the rendered configuration last confirmed applied to
	// the node (pkg/render's hash format), used for the convergence check on later reconciles.
	// It is a hash only, never the rendered configuration itself (principles 1 and 3).
	// +optional
	AppliedConfigHash string `json:"appliedConfigHash,omitempty"`

	// observedInputsHash hashes the generations of every object that fed the rendered
	// configuration last confirmed to match the node: the TalosCluster, this TalosMachine,
	// and every TalosConfigPatch that applies to it. It is only updated when the node is
	// confirmed converged, so comparing it against the current generations is how the
	// reconciler tells a desired-state change (a generation moved) apart from drift (none
	// did, so the node must have). A hash only, never the generations or config themselves
	// (principle 3).
	// +optional
	ObservedInputsHash string `json:"observedInputsHash,omitempty"`

	// observedReconcileRequest is the value of the ichor.maesh.dev/reconcile-requested annotation
	// at the last reconcile. An etcd restore (ADR 0089) writes a fresh token there and waits for
	// it to appear here, which proves the machine was looked at again after the rollback.
	// +optional
	ObservedReconcileRequest string `json:"observedReconcileRequest,omitempty"`

	// stage is the last stage the node was seen in on its way from maintenance mode to
	// running: maintenance, installing, rebooting or running (Talos's MachineStatus stages).
	// installing and rebooting right after the initial apply are inferred (the apply RPC only
	// means "stored"); running is read from the node. See ADR 0076.
	// +optional
	Stage string `json:"stage,omitempty"`

	// schematic is the Image Factory schematic ID the node's running system was built from,
	// read from the node (its ImageFactorySchematic resource) on every reconcile that reaches
	// it. Empty when the node did not boot an Image Factory asset or was never read. It is what
	// a Talos upgrade and its plan compare spec.schematic against (ADR 0023, Amendment).
	// +optional
	Schematic string `json:"schematic,omitempty"`

	// nodeName is the Kubernetes node name the node reports (its Nodename resource), recorded
	// once it is read so that views can name a machine that has no spec.hostname. Empty until
	// the node has been read.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// configStoredAt is when the node accepted its initial configuration over the insecure
	// maintenance API. Once set, Ichor never applies to this node insecurely again; it only
	// follows the node to running. Empty for a node that has not been provisioned by Ichor.
	// +optional
	ConfigStoredAt *metav1.Time `json:"configStoredAt,omitempty"`

	// observedEndpoint is the node's Talos API address when it moved away from spec.endpoint (a
	// new DHCP lease, a recreated pod). The controller never changes spec, so it records the
	// address here, found by discovery matching the node's identity (ADR 0011) or from the node's
	// Kubernetes Node address, and dials it in preference to spec.endpoint (ADR 0086).
	// +optional
	ObservedEndpoint string `json:"observedEndpoint,omitempty"`

	// observedEndpointFor is the spec.endpoint that observedEndpoint was recorded against. When an
	// operator edits spec.endpoint, the two differ and observedEndpoint is ignored.
	// +optional
	ObservedEndpointFor string `json:"observedEndpointFor,omitempty"`

	// maintenanceFingerprint is the SPKI fingerprint (SHA-256, base64) of the
	// certificate the node's maintenance-mode Talos API presented when it was discovered. The
	// controller pushes the first machine configuration, which carries the CA keys, only to a node
	// that presents this certificate again (ADR 0108). A node that was never discovered through
	// this controller (added by IP, reclaimed) is pinned at its first provisioning probe instead.
	// It is refreshed while the machine is not approved. Talos issues a new certificate when the
	// node reboots or changes address; an operator who has checked the new one accepts it with
	// the TalosMachineTrustMaintenanceFingerprintAnnotation.
	// +optional
	MaintenanceFingerprint string `json:"maintenanceFingerprint,omitempty"`

	// rebootPaths lists the configuration field paths whose pending change needs a reboot, while
	// Ready is False with reason RebootRequired; empty otherwise. A config-rollout
	// TalosOperation (ADR 0080) applies them; the plan shown before starting one reads this.
	// +listType=atomic
	// +optional
	RebootPaths []string `json:"rebootPaths,omitempty"`

	// apiServerRestartPaths lists the configuration field paths whose pending change needs no reboot
	// but restarts the node's kube-apiserver (pkg/fieldmeta's APIServerRestart class), while Ready is
	// False with reason APIServerRestartRequired; empty otherwise. This reconciler never applies them
	// (it would restart every control plane's API server at about the same time); a config-rollout
	// TalosOperation (ADR 0080) does, one control plane at a time.
	// +listType=atomic
	// +optional
	APIServerRestartPaths []string `json:"apiServerRestartPaths,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tm
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=".spec.role"
// +kubebuilder:printcolumn:name="Approval",type=string,JSONPath=".spec.approvalState"
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=".status.nodeName"
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=".spec.endpoint"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TalosMachine is the Schema for the talosmachines API
type TalosMachine struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TalosMachine
	// +required
	Spec TalosMachineSpec `json:"spec"`

	// status defines the observed state of TalosMachine
	// +optional
	Status TalosMachineStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosMachineList contains a list of TalosMachine
type TalosMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &TalosMachine{}, &TalosMachineList{})
		return nil
	})
}

// TalosMachineRemovalFinalizer holds a TalosMachine's deletion until the controller has drained its
// node and, for a control plane, removed its etcd member (ADR 0032). The controller adds it to every
// approved control plane; a worker gets it when a removal or reset is requested for it (ADR 0085).
// TalosMachineReasonNodeUnreachable is the reason of Ready=False once the node has not answered for
// longer than a short grace. A control plane in this state may be removed below the declared count
// (ADR 0106).
const TalosMachineReasonNodeUnreachable = "NodeUnreachable"

const TalosMachineRemovalFinalizer = "ichor.maesh.dev/machine-removal"

// TalosMachineHoldApplyAnnotation, set to "true", makes the TalosMachine reconciler compute and
// report drift but never apply anything to the node (ADR 0103). `ichor adopt` creates adopted
// machines with it and removes it once the live zero-drift check has passed.
const TalosMachineHoldApplyAnnotation = "ichor.maesh.dev/hold-apply"

// TalosMachineTrustMaintenanceFingerprintAnnotation holds the maintenance-mode certificate fingerprint
// an operator has checked and accepts for an approved machine that has not been provisioned yet. It
// takes the place of status.maintenanceFingerprint (ADR 0108).
const TalosMachineTrustMaintenanceFingerprintAnnotation = "ichor.maesh.dev/trust-maintenance-fingerprint"
