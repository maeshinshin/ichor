---
sidebar_group: "Cluster lifecycle"
sidebar_order: 13
---

# Nodes

![The life of a machine. A machine in maintenance mode is discovered and waits as Pending for a person to approve or reject it. Approved, it gets its role, disks and network, is provisioned (install, reboot, join) and becomes Ready. A removed node is drained, leaves etcd, is wiped and has its Node deleted, then reappears as pending after a reboot. A node that already runs another cluster's Talos creates nothing in discovery until a reclaim, which needs its UUID typed. A node whose disk was wiped or replaced is provisioned again by itself.](../../images/machine-lifecycle.light.drawio.svg)

*A person decides at approval, reject, reclaim and removal; the controller does the rest.*

## Discovery

A node in maintenance mode is registered by IP or found by scanning a subnet for
the Talos API port (the only option: Talos offers no beacon or mDNS; an
unconfigured node runs DHCP on every link). The controller reads hardware
identity, disks and NICs insecurely and creates a pending `TalosMachine`.

- **Identity** is the tuple of SMBIOS UUID, serial number and permanent MACs,
  not the UUID alone: Talos says the UUID "may repeat or be filled with zeros",
  and a maintenance connection is read-only, so Ichor cannot detect or repair a
  collision from the node.
- **No auto-registration on a weak identity.** An empty UUID, or one that maps to
  a live `TalosMachine`, gives a pending entry an operator resolves, with address,
  MACs, serial and manufacturer shown. The durable record is
  `TalosMachine.spec.identity`.
- A reset of STATE and EPHEMERAL (reclaim, or remove without a wipe mode) keeps
  META, so the node is not installed again. A whole-system-disk reset
  (`--wipe-mode system-disk` or `all`) removes it, and the node installs afresh.

### A machine that already runs Talos

Its API demands a client certificate, so discovery reports `AlreadyConfigured`
(an Event, and in the add-by-IP response) and creates nothing. **Reclaim** resets
STATE and EPHEMERAL; it is never automatic, and both paths share `pkg/reclaim`
and its refusals:

| Refused | Override |
|---|---|
| Already an Ichor machine or cluster | none |
| A control plane whose removal breaks its cluster's etcd quorum | `--force`, second confirmation |
| The last control plane of a reachable cluster | `--force`, second confirmation |

The reset is graceful unless the foreign cluster is dead (`--force-ungraceful`).
User disks are untouched but listed, encrypted ones called out. Consent is the
node's UUID, typed. After the reboot the add-by-IP flow registers the machine as
pending.

| Path | How |
|---|---|
| CLI | `ichor reclaim node ADDRESS --talosconfig FILE`: writes to the node with the operator's own credential, which never leaves their machine. |
| UI | Add node and Machines, "Reset and add", for users with the `reclaim` verb on `machinereclaims`. The UI API creates a `MachineReclaim` and a credential Secret as the user; the controller inspects the node, stops at `AwaitingConfirmation` and resets only after the UUID is typed. See [Trust](trust.md). |

A machine carrying an Ichor cluster's own CAs (joined with the user's tooling
after an adoption, or its `TalosMachine` was deleted) needs no reset and reclaim
refuses it. `ichor adopt --cluster NAME --add-machine ADDRESS` registers it: the
adoption's read-only plan, drift classes, hold and zero-drift release, for the
listed addresses only, checking that its configuration names this cluster's ID
and Talos API CA. The UI has no button for it yet.

## Approval

In the UI or `ichor approve node`, with typed forms: role, labels, disk roles,
basic networking (NIC, DHCP or static, VLAN).

- "Which NIC" is a `LinkAliasConfig` whose CEL selector matches the discovered
  permanent MAC, which also fixes kernel interface names changing across boots.
  Bond members take names or aliases, so one alias is emitted per member.
- Render refuses a VIP endpoint a static-address control plane cannot hold: it
  must lie inside one of the machine's subnets and not be the node's own address
  (`TalosClusterEndpoint.CheckVIPHeldBy`, also run by the webhook). A DHCP
  machine is not checked.
- The controller applies the config insecurely and follows the node. The apply
  RPC returns **before** the install starts (Talos reacts to stored config
  asynchronously), so the flow watches `MachineStatus`'s stage (maintenance,
  installing, rebooting, running).
- A converged machine that then answers only in maintenance mode with the
  approved identity (disk wiped or replaced) is provisioned again by itself, with
  a Warning Event. The first provisioning's checks apply: identity, SecureBoot,
  install disk, cluster lock. A control plane's stale etcd member is removed
  through a surviving one first; with none, nothing is sent and the message points
  to `ichor restore cluster`.

## Disks

Identified by WWID, serial, by-id path, then bus path, not `/dev/sdX`. All can be
absent (Talos fills WWID and serial from sysfs, udev only for `ata`/`scsi`: NVMe
is reliable, plain `virtio-blk` usually has neither). "Nothing stable to key on"
is a state the form shows, not an error.

| Role | Becomes |
|---|---|
| `system` | `spec.install.disk` (WWID, serial, by-id, bus path, or device path last): `UnattendedInstallConfig` (v1.14 contract) or `machine.install`. Install target plus EPHEMERAL; one dedicated disk recommended. |
| `ephemeral` | `VolumeConfig`, EPHEMERAL on its own disk |
| `userVolume` | `UserVolumeConfig` / `RawVolumeConfig` |
| `storage` | prepared by a storage preset; not done in `spec.disks` |
| `unused` | nothing; not done in `spec.disks` |

Non-system documents select their disk with a CEL expression over reported
fields. `spec.disks` sets `ephemeral` and `userVolume` at approval; `spec.network`
the primary NIC, DHCP or static, and a VLAN; bonds are not done.

- Roles are fixed at install: Talos ignores volume changes afterwards. Changing
  `system` or `ephemeral` is a reset-and-reinstall; adding `storage` to an
  `unused` disk is not. A disk with partitions or filesystems needs confirmation.
- A changed selector on an existing volume does nothing and nothing says so (Talos
  finds EPHEMERAL by partition label first). Ichor reports "accepted, no effect".
- A lowered `maxSize` is ignored; partitions only grow.
- A negative `maxSize` is *space to leave free* (`PartitionSpec.ResolveMaxSize`,
  Talos v1.14.3): the volume gets `available - value`; `-25%` of 200 GiB
  available takes 150. More than available fails ("partition size cannot be
  negative") without clamping, so the volume does not provision. This is how a
  disk is shared: EPHEMERAL reserves, the user volume takes the rest.
- `machine.install.wipe` defaults to **true**: the install target's confirmation
  guards against loss. Any other disk is never provisioned over an existing
  partition table or filesystem, so its confirmation only explains why.
- A single-disk node sharing with storage is not built; it would need EPHEMERAL
  (minimum 2 GiB) to claim its region first, and a warning on control planes.

## Removal

A finalizer drives it, in order:

1. Storage evacuation hook, if configured.
2. Cordon and drain with a timeout (`k8s.io/kubectl`'s drain helper; a timeout is
   an error, the "pause, don't escalate" signal).
3. A control plane's etcd member is removed. Refused if quorum would break, the
   count would fall below `spec.controlPlane.count`, or it is the last one.
4. Graceful Talos reset.
5. The Node is deleted.

The machine reappears as pending (or, removed with power off, when powered on).
`delete node` always wipes STATE and EPHEMERAL (`--wipe-mode` erases more),
`--reboot` reboots afterwards, and it needs the override permission and the typed
node name. A node that cannot be dialed is not wiped, the Event says so, and a
control plane's etcd member is removed through a surviving sibling.

Control planes are replaced add-then-remove: 3 to 4 to 3, never 3 to 2 (four
members tolerate one failure, two none).

Rules from Talos's behaviour (ADRs 0010, 0042):

- The reset names STATE and EPHEMERAL: Talos's default wipes the whole system
  disk, a reinstall, not a return to maintenance mode.
- Refusals are Ichor's alone, in shared code ([principle 10](principles.md));
  Talos checks no quorum.
- Ichor's drain and survivor-side member removal exist to refuse *before* Talos is
  asked and for a node that cannot be dialed; a graceful reset already drains and
  leaves etcd on a live node.
- Status records that a reset was requested before the controller can crash:
  Reset is fire-and-forget and must not be fired twice. Other steps are idempotent.

Removing the finalizer by hand mid-sequence deletes the resource and orphans a
possibly cordoned node, possibly still an etcd voter. Refusing it except through
override belongs with the other dangerous-operation checks.

## Topology

- Control planes: 1, 3 or 5. Two is rejected (etcd needs both); four tolerates
  what three does. An even count is allowed only transiently, shown as
  "transitioning", with the cluster lock blocking other disruptive work.
- Workers can be zero.
- `controlPlane.schedulable` is `true` or `false`, required, no `auto`: the
  cluster exists before any machine, and the preset is an `ichor init` flag, not
  a field. Adding workers never retaints control planes; unschedulable control
  planes with zero workers are rejected.

| Preset | Shape |
|---|---|
| `single` | 1+0, schedulable |
| `compact-ha` | 3+0, schedulable |
| `standard` | 3+n, tainted (recommended) |
| `large` | 5+n, tainted |

Small clusters: a single-node upgrade cannot drain, so the UI warns of downtime
and asks; a missing backup configuration is a warning; a three-node cluster
without workers gets a capacity check before each drain.
