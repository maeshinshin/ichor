---
title: "TalosMachine"
description: "TalosMachine is the Schema for the talosmachines API"
---

# TalosMachine

This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.

TalosMachine is the Schema for the talosmachines API

| | |
|---|---|
| Group | `ichor.maesh.dev` |
| Kind | `TalosMachine` (list kind `TalosMachineList`) |
| Resource | `talosmachines` |
| Short names | `tm` |
| Scope | Namespaced |

## v1alpha1

Version v1alpha1: has a `status` subresource.

### spec

| Field | Type | Required | Description |
|---|---|---|---|
| `spec` | `object` | yes | spec defines the desired state of TalosMachine |
| `spec.approvalDecision` | `object` | no | approvalDecision records who made the current approvalState decision, when, and why. It is kept separate from approvalState, not folded into it, so approvalState alone remains this machine's single source of truth for approval status -- this struct asserts no approved/ rejected state of its own, only the record behind whichever state approvalState already carries. Unset until the first approve/reject. |
| `spec.approvalDecision.actor` | `string` | no | actor identifies who made the decision. Fixed to the backend's own identity until OIDC login lands; the field is added now rather than later so every decision from here on has an actor recorded, instead of leaving earlier decisions with none once a real per-person identity exists to fill it. |
| `spec.approvalDecision.at` | `string (date-time)` | no | at is when the decision was made. |
| `spec.approvalDecision.reason` **(deprecated)** | `string` | no | reason is deprecated: unused, kept so the schema never drops a field. It is no longer asked for or written; decisions made before it carry the text they were given. |
| `spec.approvalState` | `string` | no | approvalState records the human decision on this machine: pending (the default, awaiting a decision), approved (the controller renders and applies configuration), or rejected (an operator explicitly turned it away; re-approving reverses it). This replaces an earlier plain "approved" bool, which had no way to represent "rejected" without a second field claiming the same truth -- one field is the source of truth for this machine's approval status. Allowed values: `pending`, `approved`, `rejected`. Default: `"pending"`. |
| `spec.clusterRef` | `object` | yes | clusterRef names the TalosCluster this machine belongs to. |
| `spec.clusterRef.name` | `string` | no | Name of the referent. This field is effectively required, but due to backwards compatibility is allowed to be empty. Instances of this type with an empty value here are almost certainly wrong. More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names Default: `""`. |
| `spec.disks` | `[]object` | no | disks assign roles to disks other than the system disk (spec.install): ephemeral puts EPHEMERAL on its own disk, userVolume provisions a named user volume. |
| `spec.disks[].disk` | `object` | yes | disk selects the disk. |
| `spec.disks[].disk.busPath` | `string` | no | busPath is the disk's bus path, e.g. /pci0000:00/0000:00:17.0/ata1/host0/target0:0:0/0:0:0:0. |
| `spec.disks[].disk.byIDPath` | `string` | no | byIDPath is a /dev/disk/by-id/ symlink of the disk. |
| `spec.disks[].disk.devPath` | `string` | no | devPath is the kernel device path, e.g. /dev/sda. Not stable across boots; prefer the others. |
| `spec.disks[].disk.serial` | `string` | no | serial is the disk's serial number. |
| `spec.disks[].disk.wwid` | `string` | no | wwid is the disk's World Wide Identifier. |
| `spec.disks[].maxSize` | `string` | no | maxSize caps a user volume, e.g. 200GiB. Unset lets it use the whole disk. |
| `spec.disks[].name` | `string` | no | name is the user volume's name; it is mounted at /var/mnt/\<name>. |
| `spec.disks[].role` | `string` | yes | role is ephemeral (EPHEMERAL on this disk) or userVolume (a user volume on it). Allowed values: `ephemeral`, `userVolume`. |
| `spec.endpoint` | `string` | yes | endpoint is the machine's Talos API address. |
| `spec.hostname` | `string` | no | hostname overrides the Kubernetes Node name; Talos derives one from the machine if unset. It must be an RFC 1123 DNS label or subdomain (lower-case alphanumerics, '-' and '.'). |
| `spec.identity` | `object` | yes | identity identifies the physical machine backing this resource. |
| `spec.identity.macAddresses` | `[]string` | no | macAddresses are the machine's NIC MAC addresses, used as an identity hint. |
| `spec.identity.serial` | `string` | no | serial is the machine's SMBIOS serial number, used as an identity hint. |
| `spec.identity.uuid` | `string` | yes | uuid is the machine's SMBIOS UUID, the primary key for identity. |
| `spec.install` | `object` | no | install is where Talos installs itself on this machine (the design's `system` disk role). Unset renders no install configuration, which leaves a machine that boots from an installed disk alone but gives a maintenance-mode machine nothing to install onto. |
| `spec.install.disk` | `object` | yes | disk selects the system disk by a stable identifier. Required: the v1.14 install document has no meaning without a disk to install onto. |
| `spec.install.disk.busPath` | `string` | no | busPath is the disk's bus path, e.g. /pci0000:00/0000:00:17.0/ata1/host0/target0:0:0/0:0:0:0. |
| `spec.install.disk.byIDPath` | `string` | no | byIDPath is a /dev/disk/by-id/ symlink of the disk. |
| `spec.install.disk.devPath` | `string` | no | devPath is the kernel device path, e.g. /dev/sda. Not stable across boots; prefer the others. |
| `spec.install.disk.serial` | `string` | no | serial is the disk's serial number. |
| `spec.install.disk.wwid` | `string` | no | wwid is the disk's World Wide Identifier. |
| `spec.install.image` | `string` | no | image is the installer image reference. Unset lets Talos use the installer of the schematic it booted from, which only works when it booted an Image Factory asset. |
| `spec.install.wipe` | `boolean` | no | wipe is the install-time wipe flag of the node's configuration (UnattendedInstallConfig provisioning.wipe, machine.install.wipe before v1.14). It is read only when Talos installs, so on a running node it records what the configuration says and does nothing. Default false, which is what Talos's generator writes. |
| `spec.network` | `object` | no | network is the machine's primary network link and how it is addressed. Unset leaves networking to Talos's default (DHCP on every physical link). |
| `spec.network.addresses` | `[]string` | no | addresses are the static addresses with their prefix length, e.g. 192.168.1.10/24. |
| `spec.network.addressing` | `string` | no | addressing is dhcp (the default) or static. Allowed values: `dhcp`, `static`. Default: `"dhcp"`. |
| `spec.network.dnsServers` | `[]string` | no | dnsServers are resolver addresses, in order. Valid with either addressing. |
| `spec.network.gateway` | `string` | no | gateway is the default gateway; unset adds no default route. |
| `spec.network.mac` | `string` | yes | mac is the link's permanent hardware address, as discovery reported it. |
| `spec.network.vlanID` | `integer` | no | vlanID puts the addressing on a VLAN of the link instead of the link itself. |
| `spec.nodeLabels` | `map[string]string` | no | nodeLabels are applied to the Kubernetes Node, not to this resource, which is why the field isn't called "labels". |
| `spec.role` | `string` | no | role is the machine's role in the cluster. Left unset by discovery, which does not know it yet; approval must set it before the configuration reconciler can render anything for this machine. Allowed values: `controlplane`, `worker`. |

### status

| Field | Type | Required | Description |
|---|---|---|---|
| `status` | `object` | no | status defines the observed state of TalosMachine |
| `status.apiServerRestartPaths` | `[]string` | no | apiServerRestartPaths lists the configuration field paths whose pending change needs no reboot but restarts the node's kube-apiserver (pkg/fieldmeta's APIServerRestart class), while Ready is False with reason APIServerRestartRequired; empty otherwise. This reconciler never applies them (it would restart every control plane's API server at about the same time); a config-rollout TalosOperation does, one control plane at a time. |
| `status.appliedConfigHash` | `string` | no | appliedConfigHash is the sha256 of the rendered configuration last confirmed applied to the node (pkg/render's hash format), used for the convergence check on later reconciles. It is a hash only, never the rendered configuration itself (principles 1 and 3). |
| `status.conditions` | `[]object` | no | conditions represent the current state of the TalosMachine resource, kstatus-compatible. |
| `status.conditions[].lastTransitionTime` | `string (date-time)` | yes | lastTransitionTime is the last time the condition transitioned from one status to another. This should be when the underlying condition changed. If that is not known, then using the time when the API field changed is acceptable. |
| `status.conditions[].message` | `string` | yes | message is a human readable message indicating details about the transition. This may be an empty string. |
| `status.conditions[].observedGeneration` | `integer` | no | observedGeneration represents the .metadata.generation that the condition was set based upon. For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date with respect to the current state of the instance. |
| `status.conditions[].reason` | `string` | yes | reason contains a programmatic identifier indicating the reason for the condition's last transition. Producers of specific condition types may define expected values and meanings for this field, and whether the values are considered a guaranteed API. The value should be a CamelCase string. This field may not be empty. |
| `status.conditions[].status` | `string` | yes | status of the condition, one of True, False, Unknown. Allowed values: `True`, `False`, `Unknown`. |
| `status.conditions[].type` | `string` | yes | type of condition in CamelCase or in foo.example.com/CamelCase. |
| `status.configStoredAt` | `string (date-time)` | no | configStoredAt is when the node accepted its initial configuration over the insecure maintenance API. Once set, Ichor never applies to this node insecurely again; it only follows the node to running. Empty for a node that has not been provisioned by Ichor. |
| `status.discoveredDisks` | `[]object` | no | discoveredDisks lists the installable disks (no CD-ROM, no read-only one) the node reported through its maintenance-mode API when discovery last probed it. Approval picks the install disk and disk roles from this list, and DiscoveryWarnings checks a choice against it. Empty until a probe has recorded one. |
| `status.discoveredDisks[].busPath` | `string` | no | busPath is the disk's bus path, as reported. |
| `status.discoveredDisks[].byIDPath` | `string` | no | byIDPath is the disk's /dev/disk/by-id/ symlink, as reported. |
| `status.discoveredDisks[].devPath` | `string` | no | devPath is the kernel device path, as reported. Not stable across boots. |
| `status.discoveredDisks[].model` | `string` | no | model is the disk's model name, as reported. |
| `status.discoveredDisks[].serial` | `string` | no | serial is the disk's serial number, as reported. |
| `status.discoveredDisks[].sizeBytes` | `integer` | no | sizeBytes is the disk size in bytes. |
| `status.discoveredDisks[].transport` | `string` | no | transport is the bus the disk is attached over (sata, nvme, scsi, virtio, ...), as reported. |
| `status.discoveredDisks[].wwid` | `string` | no | wwid is the disk's World Wide Identifier, as reported. |
| `status.discoveredLinks` | `[]object` | no | discoveredLinks lists the physical links with a permanent MAC address the node reported through its maintenance-mode API when discovery last probed it. Approval picks the primary NIC from this list. Empty until a probe has recorded one. |
| `status.discoveredLinks[].driver` | `string` | no | driver is the kernel driver that owns the NIC, as reported. |
| `status.discoveredLinks[].linkUp` | `boolean` | no | linkUp is true when the link reports carrier. |
| `status.discoveredLinks[].name` | `string` | yes | name is the kernel link name (eno1, enp3s0, ...), as reported; it can change between boots. |
| `status.discoveredLinks[].permanentMAC` | `string` | yes | permanentMAC is the hardware address burned into the NIC. |
| `status.discoveredLinks[].speedMbit` | `integer` | no | speedMbit is the negotiated speed in Mbit/s, or 0 when the link does not report one. |
| `status.kubeletServingCertificate` | `object` | no | kubeletServingCertificate is the latest kubelet serving certificate Ichor approved for this machine's node and the Kubernetes CA issued (spec.security.kubeletServingCertificates). Empty while the option is off or no request has been issued yet. |
| `status.kubeletServingCertificate.issuedAt` | `string (date-time)` | no | issuedAt is when the Kubernetes CA signed it (the certificate's NotBefore). |
| `status.kubeletServingCertificate.notAfter` | `string (date-time)` | no | notAfter is when it expires; the kubelet asks for a new one before that. |
| `status.kubeletServingCertificate.request` | `string` | no | request is the name of the CertificateSigningRequest Ichor approved. |
| `status.maintenanceFingerprint` | `string` | no | maintenanceFingerprint is the SPKI fingerprint (SHA-256, base64) of the certificate the node's maintenance-mode Talos API presented when it was discovered. The controller pushes the first machine configuration, which carries the CA keys, only to a node that presents this certificate again. A node that was never discovered through this controller (added by IP, reclaimed) is pinned at its first provisioning probe instead. It is refreshed while the machine is not approved. Talos issues a new certificate when the node reboots or changes address; an operator who has checked the new one accepts it with the TalosMachineTrustMaintenanceFingerprintAnnotation. |
| `status.nodeName` | `string` | no | nodeName is the Kubernetes node name the node reports (its Nodename resource), recorded once it is read so that views can name a machine that has no spec.hostname. Empty until the node has been read. |
| `status.observedEndpoint` | `string` | no | observedEndpoint is the node's Talos API address when it moved away from spec.endpoint (a new DHCP lease, a recreated pod). The controller never changes spec, so it records the address here, found by discovery matching the node's identity or from the node's Kubernetes Node address, and dials it in preference to spec.endpoint. |
| `status.observedEndpointFor` | `string` | no | observedEndpointFor is the spec.endpoint that observedEndpoint was recorded against. When an operator edits spec.endpoint, the two differ and observedEndpoint is ignored. |
| `status.observedGeneration` | `integer` | no | observedGeneration is the most recent generation the controller has reconciled. |
| `status.observedInputsHash` | `string` | no | observedInputsHash hashes the generations of every object that fed the rendered configuration last confirmed to match the node: the TalosCluster, this TalosMachine, and every TalosConfigPatch that applies to it. It is only updated when the node is confirmed converged, so comparing it against the current generations is how the reconciler tells a desired-state change (a generation moved) apart from drift (none did, so the node must have). A hash only, never the generations or config themselves (principle 3). |
| `status.observedReconcileRequest` | `string` | no | observedReconcileRequest is the value of the ichor.maesh.dev/reconcile-requested annotation at the last reconcile. An etcd restore writes a fresh token there and waits for it to appear here, which proves the machine was looked at again after the rollback. |
| `status.rebootPaths` | `[]string` | no | rebootPaths lists the configuration field paths whose pending change needs a reboot, while Ready is False with reason RebootRequired; empty otherwise. A config-rollout TalosOperation applies them; the plan shown before starting one reads this. |
| `status.schematic` | `string` | no | schematic is the Image Factory schematic ID the node's running system was built from, read from the node (its ImageFactorySchematic resource) on every reconcile that reaches it. Empty when the node did not boot an Image Factory asset or was never read. It is what a Talos upgrade and its plan compare spec.schematic against. |
| `status.security` | `object` | no | security is the boot security the node reported on the last reconcile that reached it (its SecurityState and KernelCmdline resources). Unset until a node has been read. |
| `status.security.bootedWithUKI` | `boolean` | no | bootedWithUKI is true when the node booted a Unified Kernel Image. A node that did not (a GRUB install) cannot get SecureBoot without a reinstall. |
| `status.security.kernelLockdown` | `string` | no | kernelLockdown is the lockdown level in effect: the last lockdown= word of the kernel command line, else integrity under SecureBoot (Talos v1.14's implicit level), else none. Allowed values: `none`, `integrity`, `confidentiality`. |
| `status.security.moduleSignatureEnforced` | `boolean` | no | moduleSignatureEnforced is true when the kernel only loads signed modules. |
| `status.security.pcrSigningKeyFingerprint` | `string` | no | pcrSigningKeyFingerprint is the fingerprint of the key that signs the PCR measurements of the booted UKI (SecurityState.pcrSigningKeyFingerprint); empty when the node reports none. |
| `status.security.secureBoot` | `boolean` | no | secureBoot is true when the node booted with UEFI SecureBoot enabled. |
| `status.security.ukiSigningKeyFingerprint` | `string` | no | ukiSigningKeyFingerprint is the UKI signing key fingerprint of Talos's SecurityState. Talos v1.14 does not fill it, so it stays empty; pcrSigningKeyFingerprint is the one it reports. Kept for compatibility (CRD fields are append-only). |
| `status.stage` | `string` | no | stage is the last stage the node was seen in on its way from maintenance mode to running: maintenance, installing, rebooting or running (Talos's MachineStatus stages). installing and rebooting right after the initial apply are inferred (the apply RPC only means "stored"); running is read from the node. |

