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
installer image, create the `UserVolumeConfig` Longhorn expects), followed by
`longhorn-v2`, `raw` (for Rook/Ceph-style consumers) and `filesystem`.
Missing extensions are reported by the informational `ExtensionsPresent`
condition on `TalosStorage`, read from the Nodes' `extensions.talos.dev/*`
labels; it never gates `Ready`. Adding one is an installer-image
change: a new schematic, which the Talos upgrade treats as an upgrade even when
the version does not move.

Disks are registered with the storage system through its own node label and
annotation mechanism, not by writing its CRs. A consistency check confirms the
storage system is configured to honour that.

The node-operation hook is shared by removal, upgrades and reboot-requiring
config changes: mark the node unschedulable in the storage system, request
evacuation, wait until no replica lives only there (with a timeout that pauses
the rollout rather than proceeding), then drain; afterwards, re-enable
scheduling. The hook is an interface so other integrations can use it.

For `longhorn-v1` the write side of that hook is settled, from
longhorn-manager v1.9.1's own type declarations rather than from the UI's
labels: `nodes.longhorn.io/v1beta2`'s `NodeSpec` carries `allowScheduling`
(bool) and `evictionRequested` (bool), and `DiskSpec` carries the same two
under `spec.disks[<name>]`, so a hook can evict a whole node or one disk.
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
`spec.nodeID` (whole-node eviction) or `spec.diskID` (one-disk eviction)
still naming the target" -- an absence of objects, not a counter reaching
zero. `EvacuationStatus` (`pkg/storage/evacuation.go`) takes that count
already read by the caller, plus elapsed time against
`TalosStorage.spec.evacuationTimeout`, and returns done, still-waiting, or
paused -- paused, never proceeding to drain, once the timeout is reached
with replicas still remaining.

`TalosStorage` reports health, so whatever installs the storage system can wait
until the Talos side is ready.
