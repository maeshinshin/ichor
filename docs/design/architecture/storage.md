---
sidebar_group: "Integrations"
sidebar_order: 15
---

# Storage

The work splits into four layers; Ichor owns the first and the last.

| | What | Owner |
|---|---|---|
| 1 | Talos-side preparation: volumes, extensions, kernel modules, sysctls | Ichor |
| 2 | Installing and upgrading the storage system | You |
| 3 | Volumes, replicas, disks, settings | The storage system's controller |
| 4 | Coordination around node operations: evacuate before drain, remove or upgrade | Ichor |

`TalosStorage` stays thin: which nodes, which disk preset, how disks are
handed to the storage system, and evacuation settings. The storage system's
own configuration is yours. Presets start with `longhorn-v1`
(verify the `iscsi-tools` and `util-linux-tools` extensions are in the node's
installer image, create the `UserVolumeConfig` Longhorn expects) and `rook-v1`,
which renders no Talos volume and leaves the disk to Rook (ADR 0167; the
[Rook/Ceph guide](../../guides/rook-ceph.md) has the Talos side). `longhorn-v2`
and `filesystem` follow. `diskSelector` is required by `longhorn-v1` and refused
by `rook-v1`.
Missing extensions are reported by the `ExtensionsPresent` condition on
`TalosStorage`, read from the Nodes' `extensions.talos.dev/*` labels. A node that
lacks one holds `Ready` at False, reason `ExtensionsMissing`, naming the extensions
and the upgrade. Adding one is an installer-image change, and
`machine.install.extensions` does nothing since Talos 1.10, so it takes a new
schematic and an upgrade that reboots every node, which the Talos upgrade treats as
an upgrade even when the version does not move. Ichor does the first part (ADR 0173):
it registers a schematic with the preset's extensions at Image Factory (the controller
needs to reach `factory.talos.dev` on 443), writes its ID to `spec.schematic.id`
through the ServiceAccount `ichor-storage-extensions`, and creates a Talos upgrade
operation that is paused. Nothing reboots until a person resumes it
(`ichor resume operation <name>`, or Resume in the UI). It does this only over an
unset or stock schematic: a schematic Ichor cannot read is never changed, and
`Ready` says which extensions to build into it (reason `SchematicNotMergeable`).

Disks are registered with the storage system through its own node label and
annotation mechanism, not by writing its CRs. A consistency check confirms the
storage system is configured to honour that.

The node-operation hook is shared by removal, upgrades and reboot-requiring
config changes: mark the node unschedulable in the storage system, request
evacuation, wait until no replica lives only there (with a timeout that pauses
the rollout rather than proceeding), then drain; afterwards, re-enable
scheduling. The hook is an interface so other integrations can use it. A preset may have
no hook: `rook-v1` has none, and a node it governs goes straight to the drain,
which Rook's own disruption budgets gate (ADR 0167).

For `longhorn-v1` the write side of that hook is settled, from
longhorn-manager v1.9.1's own type declarations rather than from the UI's
labels: `nodes.longhorn.io/v1beta2`'s `NodeSpec` carries `allowScheduling`
(bool) and `evictionRequested` (bool), and `DiskSpec` carries the same two
under `spec.disks[<name>]`, so a hook can evict a whole node. Per-disk eviction
through `spec.disks[<name>]` is not built: nothing writes it, and it will be built
when a preset needs it.
The order is not a preference: Longhorn's own documentation states eviction
only takes effect on a node or disk whose scheduling is already disabled, so
`allowScheduling: false` has to land first.

The read side is now settled too, from longhorn-manager v1.9.1's own
controller source rather than from the UI's replica count: there is no
dedicated "eviction done" status field. `DiskStatus.ScheduledReplica` (the
status map that looked like the obvious candidate) is the scheduler's
capacity bookkeeping -- it is read for free space, not for residency, and
does not reliably go to zero when eviction finishes. The actual mechanism
is in `VolumeController.cleanupEvictionRequestedReplicas`: once a volume
has a healthy, non-evicting replica elsewhere, it deletes the `Replica`
object whose `spec.evictionRequested` is true. So "no replica lives only
there" is "no `replicas.longhorn.io` object remains with
`spec.nodeID` still naming the target (whole-node eviction; the per-disk
`spec.diskID` form is not built) -- an absence of objects, not a counter reaching
zero. `EvacuationStatus` (`pkg/storage/evacuation.go`) takes that count
already read by the caller, plus elapsed time against
`TalosStorage.spec.evacuationTimeout`, and returns done, still-waiting, or
paused -- paused, never proceeding to drain, once the timeout is reached
with replicas still remaining.

**The supported Longhorn version range is not stated.** Longhorn versions appear in this document and in the code without a range. Ichor does not install Longhorn; that is layer 2. The `longhorn-v1` preset and the evacuation hook use the `nodes.longhorn.io`, `replicas.longhorn.io` and `settings.longhorn.io` objects at `v1beta2`, and nothing in `pkg/storage` or `internal/controller` reads a Longhorn version or gates on one. The version-specific statements above cite longhorn-manager v1.9.1 and the Longhorn 1.12.1 and 1.13.0 documentation. ADR 0025's amendments cite webhook lines in v1.11.0, v1.12.1 and v1.13.0. The code states no lower or upper bound, so this document states none. The webhook line numbers in ADR 0025's amendment are that ADR's own citations, and the comment in `pkg/storage/longhornv1.go` that the API is unchanged since 1.12.1 is that comment's claim; this document does not adopt either.

`TalosStorage` reports health, so whatever installs the storage system can wait
until the Talos side is ready.
