---
sidebar_order: 13
---

# Nodes

Discovery starts from a node in maintenance mode: either registered by IP or
found by scanning a subnet for the Talos API port. Scanning really is the only
option — Talos offers no beacon, mDNS or announcement to listen for — and it is
tractable because an unconfigured node brings up a DHCP client on every
physical link by itself, so it has an address before anyone looks for it.

The controller reads hardware identity, disks and NICs over an insecure
connection and creates a pending `TalosMachine`. The identity is the tuple of
**SMBIOS UUID, serial number and the permanent MAC addresses**, not the UUID
alone. Talos does not trust the UUID either: it ships two META keys to work
around it, and the doc comment on one of them says plainly that the UUID "may
repeat or be filled with zeros". Both need an admin-level connection to write,
which a maintenance-mode connection never has — it is read-only by
construction — so at the one moment Ichor keys a new resource off this
identity, it has no way to detect or repair a collision from the node's side.
The MACs are in the tuple because they are hardware-level, present when the
UUID is not, and survive a reset.

So **Ichor does not auto-register on a weak identity**. An empty UUID, or one
that already maps to a live `TalosMachine`, produces a pending entry that an
operator has to resolve — the same machine returning, or a different one — with
the address, MACs, serial and manufacturer shown to decide on. Writing a
disambiguating token to the node is not a fix: it needs an authenticated
connection Ichor only has after approval. Which resets remove META depends on
what they name. A reset of `STATE` and `EPHEMERAL` (reclaim, and a remove
without a wipe mode) keeps it, and a node that keeps META is not installed
again. A reset that erases the whole system disk (`--wipe-mode system-disk` or
`all`) removes it, and the node installs afresh. The durable record is Ichor's
own `TalosMachine.spec.identity`, which is consistent with the controller owning
durable state rather than the node.

A machine that already runs a configured Talos cannot be discovered this way:
its API demands a client certificate. Discovery reports it as
`AlreadyConfigured` (an Event, and in the add-by-IP response) and creates
nothing. Joining it needs a reset of STATE and EPHEMERAL, which is
called a reclaim and is never automatic. Both paths run the same code
(`pkg/reclaim`) and the same refusals: a machine that is already an Ichor
machine or cluster, a control plane whose removal would break its cluster's
etcd quorum, and the last control plane of a reachable cluster (`--force`
overrides the last two with a second confirmation). The reset is graceful
unless the foreign cluster is dead (`--force-ungraceful`). It sends the
request, built by one shared function; user disks are not touched
but are listed, and encrypted ones are called out. Consent is the node's UUID,
typed. After the reboot the machine answers in maintenance mode with the same
UUID and the normal add-by-IP flow registers it as pending.

A machine that already carries an Ichor cluster's own CAs (it joined with the
user's tooling after an adoption, or its `TalosMachine` was deleted) needs no
reset and is refused by reclaim. `ichor adopt --cluster NAME --add-machine
ADDRESS` registers it: the adoption's read-only plan, three classes of drift,
held `TalosMachine` and zero-drift release, for the listed addresses only, with
a check that the machine's own configuration names this cluster's ID and Talos
API CA. The UI has no button for it yet.

- CLI: `ichor reclaim node ADDRESS --talosconfig FILE`. The CLI writes to
  the node itself, with the operator's own credential, which never leaves the
  operator's machine; like `init` and `adopt` it acts on a node that belongs
  to no Ichor cluster.
- UI: the Add node page and Machines, "Reset and add". Offered to users who
  hold the `reclaim` verb on `machinereclaims`. The UI API creates a `MachineReclaim` and a
  credential Secret as the signed-in user; the controller inspects the node,
  stops at `AwaitingConfirmation`, and resets only after the user has typed
  the UUID. See [Trust](trust.md) for the credential.

Approval, in the UI or `ichor approve node`, sets the role, labels, disk
roles and basic networking (which NIC, DHCP or static, VLAN or bond if
needed) through typed forms; no YAML required. "Which NIC" is expressed as a
`LinkAliasConfig` whose CEL selector matches the permanent MAC discovered
earlier, and the other documents refer to that alias — which is also the fix
for kernel interface names changing across boots. Bond members take names or
aliases rather than selectors, so a form that picks members by MAC emits one
alias per member.

The controller then renders the config, applies it insecurely, and follows the
node through install, reboot and join. "Follows" is load-bearing: the apply RPC
returns **before** the install starts, because Talos's own controllers react to
the stored config asynchronously. The approval flow therefore watches
`MachineStatus`'s stage move from maintenance through installing and rebooting
to running, and treats the RPC returning as nothing more than "the config was
stored".

An approved machine that has converged and whose node then answers only in
maintenance mode with the approved identity (its disk was wiped or replaced) is
sent its configuration again, on its own, with a Warning Event and a Ready
message saying it re-installed the node. The identity checks, the SecureBoot
requirement, the install disk and the control plane's cluster lock are the
first provisioning's; a control plane's stale etcd member is removed through a
surviving control plane first, and with no surviving control plane nothing is
sent and the message points to `ichor restore cluster`.

Disks are identified by WWID, serial or by-id path, not `/dev/sdX` — but all
three can be absent. Talos fills WWID and serial from sysfs, with a udev
fallback only for `ata` and `scsi` transports, so NVMe is reliable while a plain
`virtio-blk` disk usually has neither, and without one udev creates no by-id
link either. So identification falls back through WWID, serial, by-id path
and finally bus path, and "nothing stable to key on" is a state the approval
form has to show rather than treat as an error.

Each disk gets a role: `system` (install target plus EPHEMERAL — the recommended
layout is one dedicated disk for Talos), `ephemeral` (EPHEMERAL on its own
disk), `storage` (prepared for a storage system via a preset), `userVolume`, or
`unused`. A role becomes `machine.install` for `system` and a
`VolumeConfig`/`UserVolumeConfig`/`RawVolumeConfig` document for the rest,
each selecting its disk with a CEL expression over the disk's reported fields.
All three documents are on the mirror allow-list, so a role is expressible
today; `TalosMachine.spec.disks` sets `ephemeral` and `userVolume` at approval
and `spec.network` the primary NIC, DHCP or static, and a VLAN;
`storage`, `unused` and bonds are not done. The `system`
disk is the exception to "a document per role": it is
`TalosMachine.spec.install.disk` (WWID, serial, by-id path, bus path, or
device path as a last resort), rendered as `UnattendedInstallConfig` from the
v1.14 contract and as `machine.install` before it.

Two things about editing a disk role after the fact, both follow from
Talos's volume manager. **A changed selector on a volume that already exists
does nothing, and nothing says so**: Talos locates EPHEMERAL by its partition
label before it ever consults the selector, and config validation accepts the
edit regardless. Ichor has to detect that itself and report "accepted, no
effect" rather than letting it look applied. Likewise a *lowered* `maxSize` is
silently ignored — partitions only ever grow. (A negative `maxSize` is
meaningful rather than invalid, and `PartitionSpec.ResolveMaxSize` in Talos
v1.14.3 says exactly what it means: the value is *space to leave free*, so the
volume gets `available - value`. A relative one is a percentage of available,
not of the disk -- `-25%` of 200 GiB available leaves 50 GiB free and takes
150. If the reservation exceeds what is available the call fails with
"partition size cannot be negative"; it does not clamp, so the volume simply
does not provision. That is the mechanism for sharing a disk: EPHEMERAL
reserves with a negative `maxSize`, and the user volume provisions in what is
left.)

The confirmation before wiping a disk matters most for the install target,
because `machine.install.wipe` defaults to **true** — Talos wipes the install
disk unless told not to. For every other disk Talos is the opposite: it refuses
to provision over any existing partition table or filesystem, so Ichor's
confirmation there explains why provisioning will not happen rather than
guarding against loss.
Single-disk nodes can use `systemDiskLayout: shared`, which caps EPHEMERAL and
leaves the rest for storage — Talos makes that work through partition waves,
with EPHEMERAL claiming its region before user volumes, and EPHEMERAL's own
minimum is 2 GiB; it is supported but discouraged and warned about,
especially on control planes where etcd would share I/O with storage. Disk
roles are fixed at install time because Talos ignores volume config changes
after provisioning; changing `system` or `ephemeral` later is a reset-and-
reinstall operation and is treated as such. Adding a `storage` role to an
`unused` disk doesn't need a reset. Disks with existing partitions or
filesystems require explicit confirmation.

Removal is a finalizer-driven sequence: storage evacuation hook (if
configured) → cordon and drain with a timeout → remove the etcd member if it's
a control plane (refused if quorum would break, if the count would drop below
`spec.controlPlane.count`, or if it's the last one) → graceful Talos reset →
delete the Node, in that order. The machine reappears as pending and can be
reused, or, if it was removed with "power off", comes back when you power it on. Remove and
reset are one action: `delete node` always wipes STATE and EPHEMERAL, and `delete node --reboot`
reboots the node afterwards; the `ichor node reset` command was removed on 2026-11-07. A node that does not answer is removed
without the wipe, its Node is deleted, and the Event says it was not wiped. `forceRemove` skips drain and reset for dead hardware. Control planes
are replaced add-then-remove, never the other way round — a 3-node cluster
replacing a member goes 3→4→3, never 3→2, because four members tolerate one
failure while two tolerate none.

Four things about that sequence, each read from Talos 1.14.3's source
and each easy to get fatally wrong:

- **The reset must name the partitions to wipe.** With its default mode and no
  partitions named, `Reset` wipes the *entire system disk*, boot and EFI
  included, which means a reinstall rather than a return to maintenance mode.
  Passing `STATE` and `EPHEMERAL` explicitly is what leaves the installed
  system in place and brings the node back up with no configuration — which is
  the behaviour the reuse story depends on.
- **Ichor's refusals are Ichor's alone.** Talos's member-removal call requires
  only that a leader exists right now; it does not ask whether the remaining
  members can still reach quorum. The one quorum guard Talos ships refuses at
  exactly two members and only on the upgrade path. So the three refusals above
  live in shared code, as [principle 10](principles.md) requires, and are the thing most worth
  testing before any of this points at real hardware.
- **A graceful reset already drains and already leaves etcd.** Ichor's own
  drain and member removal are therefore belts rather than the trousers for a
  live node: they matter for `forceRemove`, where the node cannot run its own
  sequence, and for refusing *before* Talos is asked to do anything.
- **Reset is fire-and-forget.** It returns an actor id immediately and the
  sequence runs asynchronously, so status has to record that a reset was
  requested *before* the controller can crash — otherwise a resumed reconcile
  fires a second concurrent reset at the same node. Every other step is
  naturally idempotent: re-draining a cordoned node is harmless, and removing
  an already-removed etcd member reports not-found, which counts as success.

The finalizer cannot defend against one thing: a person removing it by hand
mid-sequence. That deletes the resource immediately and orphans whatever state
existed — a cordoned node, possibly still an etcd voter, with nothing left to
resume it. Refusing finalizer removal except through override belongs with
the other dangerous-operation checks.

Draining uses `k8s.io/kubectl`'s own drain helper rather than a hand-written
eviction loop: it already encodes the DaemonSet, mirror-pod, local-storage and
PodDisruptionBudget rules, and it reports a timeout as an error, which is
exactly the "pause, don't escalate" signal the design wants.

Topology. Control-plane count is 1, 3 or 5. Two is rejected: with two
members etcd needs both to be up, so it is strictly less available than one.
Four has the same fault tolerance as three. Even counts are allowed only
transiently during a change and shown as "transitioning", during which the
cluster lock blocks other disruptive work. Workers can be zero; control-plane-
only clusters are a supported configuration, not a corner case.

`controlPlane.schedulable` is `true` or `false`, and required. It has no
`auto`: nothing on the object can resolve one, since a cluster is created
before any machine exists and the topology preset that carries the intent is a
flag on `ichor init`, not a field. `init` sets it from the preset,
and adding workers later never retaints the control planes underneath running
pods. Making control planes unschedulable with zero workers is rejected.
Presets: `single` (1+0, schedulable), `compact-ha` (3+0, schedulable),
`standard` (3+n, tainted, recommended), `large` (5+n, tainted).

Small clusters get specific treatment: single-node upgrades can't drain
anywhere, so the UI says there will be downtime and asks; a missing backup
configuration is a warning; three-node clusters without workers get a capacity
check before each drain.
