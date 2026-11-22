---
sidebar_group: "Cluster lifecycle"
sidebar_order: 13
---

# Nodes

![The life of a machine. A machine in maintenance mode is discovered and waits as Pending for a person to approve or reject it. Approved, it gets its role, disks and network, is provisioned (install, reboot, join) and becomes Ready. A removed node is drained, leaves etcd, is wiped and has its Node deleted, then reappears as pending after a reboot. A node that already runs another cluster's Talos creates nothing in discovery until a reclaim, which needs its UUID typed. A node whose disk was wiped or replaced is provisioned again by itself.](../../images/machine-lifecycle.light.drawio.svg)

*A person decides at approval, reject, reclaim and removal; the controller does the rest.*

## Discovery

A node in maintenance mode is registered by IP or found by scanning a subnet for
the Talos API port. Scanning is the only option (Talos offers no beacon or mDNS),
and it works because an unconfigured node runs a DHCP client on every physical
link by itself.

The controller reads hardware identity, disks and NICs over an insecure
connection and creates a pending `TalosMachine`. The identity is the tuple of
**SMBIOS UUID, serial number and permanent MAC addresses**, not the UUID alone:
Talos itself says the UUID "may repeat or be filled with zeros", and a
maintenance connection is read-only, so Ichor cannot detect or repair a
collision from the node's side. MACs are hardware-level, present when the UUID is
not, and survive a reset.

**No auto-registration on a weak identity.** An empty UUID, or one that already
maps to a live `TalosMachine`, produces a pending entry an operator resolves (the
same machine returning, or a different one), with address, MACs, serial and
manufacturer shown. Writing a disambiguating token to the node needs an
authenticated connection Ichor has only after approval. The durable record is
`TalosMachine.spec.identity`.

A reset of `STATE` and `EPHEMERAL` (reclaim, and a remove without a wipe mode)
keeps META, and a node that keeps META is not installed again. A reset that
erases the whole system disk (`--wipe-mode system-disk` or `all`) removes it, and
the node installs afresh.

### A machine that already runs Talos

Its API demands a client certificate, so discovery reports `AlreadyConfigured`
(an Event, and in the add-by-IP response) and creates nothing.

**Reclaim** resets STATE and EPHEMERAL, is never automatic, and runs the same
code (`pkg/reclaim`) and refusals on both paths:

- A machine that is already an Ichor machine or cluster.
- A control plane whose removal would break its cluster's etcd quorum, and the
  last control plane of a reachable cluster. `--force` overrides these two, with
  a second confirmation.

The reset is graceful unless the foreign cluster is dead
(`--force-ungraceful`). User disks are untouched but listed, encrypted ones called
out. Consent is the node's UUID, typed. After the reboot the machine answers in
maintenance mode with the same UUID and the add-by-IP flow registers it as pending.

- CLI: `ichor reclaim node ADDRESS --talosconfig FILE`. The CLI writes to the node
  with the operator's own credential, which never leaves their machine; like
  `init` it acts on a node that belongs to no Ichor cluster.
- UI: the Add node page and Machines, "Reset and add", for users with the
  `reclaim` verb on `machinereclaims`. The UI API creates a `MachineReclaim` and a
  credential Secret as the signed-in user; the controller inspects the node, stops
  at `AwaitingConfirmation`, and resets only after the UUID is typed. See
  [Trust](trust.md).

A machine that carries an Ichor cluster's own CAs (it joined with the user's
tooling after an adoption, or its `TalosMachine` was deleted) needs no reset and
is refused by reclaim. `ichor adopt --cluster NAME --add-machine ADDRESS`
registers it: the adoption's read-only plan, three classes of drift, held
`TalosMachine` and zero-drift release, for the listed addresses only, with a check
that the machine's configuration names this cluster's ID and Talos API CA. The UI
has no button for it yet.

## Approval

In the UI or `ichor approve node`, through typed forms, no YAML: role, labels,
disk roles and basic networking (which NIC, DHCP or static, VLAN).

- "Which NIC" is a `LinkAliasConfig` whose CEL selector matches the permanent MAC
  discovered earlier; other documents refer to the alias, which also fixes
  kernel interface names changing across boots. Bond members take names or
  aliases, so a form that picks members by MAC emits one alias per member.
- The renderer refuses a VIP endpoint a control plane cannot hold: with a static
  address it must lie inside one of the machine's subnets and not be the node's
  own address (`TalosClusterEndpoint.CheckVIPHeldBy`, also run by the webhook). A
  DHCP machine names no subnet and is not checked.

The controller renders the config, applies it insecurely, and follows the node
through install, reboot and join. The apply RPC returns **before** the install
starts, because Talos reacts to the stored config asynchronously, so the flow
watches `MachineStatus`'s stage (maintenance, installing, rebooting, running) and
treats the return as only "the config was stored".

An approved, converged machine that then answers only in maintenance mode with
the approved identity (disk wiped or replaced) is sent its configuration again,
on its own, with a Warning Event and a Ready message saying it re-installed. The
identity checks, SecureBoot requirement, install disk and control plane's cluster
lock are the first provisioning's. A control plane's stale etcd member is removed
through a surviving control plane first; with none surviving, nothing is sent and
the message points to `ichor restore cluster`.

## Disks

A disk is identified by WWID, serial, by-id path, then bus path, not `/dev/sdX`.
All can be absent: Talos fills WWID and serial from sysfs, with a udev fallback
only for `ata` and `scsi`, so NVMe is reliable and plain `virtio-blk` usually has
neither (and then no by-id link). "Nothing stable to key on" is a state the
approval form shows, not an error.

| Role | Becomes |
|---|---|
| `system` | `TalosMachine.spec.install.disk` (WWID, serial, by-id, bus path, or device path as last resort): `UnattendedInstallConfig` from the v1.14 contract, `machine.install` before. Install target plus EPHEMERAL; one dedicated disk is recommended. |
| `ephemeral` | `VolumeConfig`, EPHEMERAL on its own disk |
| `storage` | prepared for a storage system by a preset |
| `userVolume` | `UserVolumeConfig` / `RawVolumeConfig` |
| `unused` | nothing |

Each non-system document selects its disk with a CEL expression over the disk's
reported fields; all three document kinds are on the mirror allow-list.
`TalosMachine.spec.disks` sets `ephemeral` and `userVolume` at approval and
`spec.network` the primary NIC, DHCP or static, and a VLAN. `storage`, `unused`
and bonds are not done.

Disk roles are fixed at install because Talos ignores volume config changes after
provisioning. Changing `system` or `ephemeral` later is a reset-and-reinstall;
adding `storage` to an `unused` disk needs no reset. A disk with existing
partitions or filesystems needs explicit confirmation.

**Editing a role afterwards** (Talos's volume manager):

- A changed selector on an existing volume does nothing and nothing says so:
  Talos finds EPHEMERAL by its partition label before consulting the selector, and
  validation accepts the edit. Ichor detects this and reports "accepted, no effect".
- A *lowered* `maxSize` is silently ignored; partitions only grow.
- A negative `maxSize` is *space to leave free* (`PartitionSpec.ResolveMaxSize`,
  Talos v1.14.3): the volume gets `available - value`, and a relative value is a
  percentage of available, so `-25%` of 200 GiB available leaves 50 GiB free and
  takes 150. A reservation larger than available fails with "partition size cannot
  be negative" and does not clamp, so the volume does not provision. This is how
  a disk is shared: EPHEMERAL reserves with a negative `maxSize` and the user
  volume provisions in the rest.

**Wipe confirmation.** `machine.install.wipe` defaults to **true**, so Talos wipes
the install disk unless told not to: the install target's confirmation guards
against loss. For every other disk Talos refuses to provision over an existing
partition table or filesystem, so the confirmation explains why provisioning will
not happen.

A single-disk node sharing the disk with storage is not built. When it is, it
needs EPHEMERAL to claim its region first (partition waves; EPHEMERAL's minimum is
2 GiB) and a warning, especially on control planes where etcd would share I/O with
storage.

## Removal

A finalizer drives the sequence, in this order:

1. Storage evacuation hook, if configured.
2. Cordon and drain, with a timeout (`k8s.io/kubectl`'s drain helper, which
   already encodes the DaemonSet, mirror-pod, local-storage and PDB rules, and
   reports a timeout as an error: the "pause, don't escalate" signal).
3. A control plane's etcd member is removed. Refused if quorum would break, if the
   count would fall below `spec.controlPlane.count`, or if it is the last one.
4. Graceful Talos reset.
5. The Node is deleted.

The machine reappears as pending and can be reused (or, removed with power off,
returns when powered on). Remove and reset are one action: `delete node` always
wipes STATE and EPHEMERAL (`--wipe-mode` erases more), and `--reboot` reboots
afterwards; it needs the override permission and the typed node name. A node that
does not answer is removed without the wipe, its Node deleted, and the Event says
it was not wiped; dead hardware skips drain and reset.

Control planes are replaced add-then-remove, never the reverse: a 3-node cluster
goes 3 to 4 to 3, never 3 to 2, because four members tolerate one failure and two
tolerate none.

Four rules follow from how Talos behaves (ADRs 0010 and 0042 record the source):

- **The reset names the partitions**, STATE and EPHEMERAL. Talos's default wipes the
  whole system disk, a reinstall, not a return to maintenance mode.
- **Ichor's refusals are Ichor's alone.** Talos does not check quorum, so the
  three refusals above live in shared code ([principle 10](principles.md)).
- **Ichor's drain and member removal are for dead hardware**, where the node cannot
  run its own sequence, and for refusing *before* Talos is asked; a graceful reset
  already drains and leaves etcd on a live node.
- **Status records that a reset was requested before the controller can crash**,
  because Reset is fire-and-forget: a resumed reconcile must not fire a second.
  Every other step is idempotent.

The finalizer cannot defend against a person removing it by hand mid-sequence:
that deletes the resource at once and orphans a possibly cordoned node, possibly
still an etcd voter, with nothing to resume it. Refusing finalizer removal except
through override belongs with the other dangerous-operation checks.

## Topology

- Control-plane count is 1, 3 or 5. Two is rejected (etcd needs both, so it is
  less available than one); four tolerates what three does. An even count is
  allowed only transiently, shown as "transitioning", while the cluster lock
  blocks other disruptive work.
- Workers can be zero; control-plane-only clusters are supported.
- `controlPlane.schedulable` is `true` or `false`, required, with no `auto`:
  a cluster is created before any machine exists, and the preset that carries
  the intent is an `ichor init` flag, not a field. `init` sets it from the
  preset, and adding workers never retaints running control planes. Unschedulable
  control planes with zero workers are rejected.

| Preset | Shape |
|---|---|
| `single` | 1+0, schedulable |
| `compact-ha` | 3+0, schedulable |
| `standard` | 3+n, tainted (recommended) |
| `large` | 5+n, tainted |

Small clusters: a single-node upgrade cannot drain anywhere, so the UI says there
will be downtime and asks; a missing backup configuration is a warning; a
three-node cluster without workers gets a capacity check before each drain.
