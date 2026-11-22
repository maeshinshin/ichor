---
sidebar_group: "Integrations"
sidebar_order: 15
---

# Storage

![The four storage layers: Ichor prepares Talos (1), you install the storage system (2), its controller runs volumes and replicas (3), and Ichor coordinates node operations (4). The node-operation hook for longhorn-v1 sets allowScheduling false, then evictionRequested, waits until no replica object names the node, then drains and operates, and re-enables scheduling afterwards. If the timeout passes with replicas left, the rollout pauses.](../../images/storage.light.drawio.svg)

*Ichor prepares Talos and evacuates before a node operation; the storage system itself is yours.*

## Layers

| | What | Owner |
|---|---|---|
| 1 | Talos-side preparation: volumes, extensions, kernel modules, sysctls | Ichor |
| 2 | Installing and upgrading the storage system | You |
| 3 | Volumes, replicas, disks, settings | The storage system's controller |
| 4 | Coordination around node operations: evacuate before drain, remove or upgrade | Ichor |

`TalosStorage` stays thin: which nodes, which disk preset, how disks reach the
storage system, and evacuation settings. It reports health, so whatever installs
the storage system can wait until the Talos side is ready.

## Presets

- `longhorn-v1`: checks that the `iscsi-tools` and `util-linux-tools`
  extensions are in the installer image and creates the `UserVolumeConfig`
  Longhorn expects. `diskSelector` is required.
- `rook-v1`: renders no Talos volume and leaves the disk to Rook (ADR 0167;
  the [Rook/Ceph guide](../../guides/rook-ceph.md)). `diskSelector` is refused.
- `longhorn-v2` and `filesystem` follow.

Disks are registered through the storage system's own node label and annotation
mechanism, never by writing its CRs. A consistency check confirms the storage
system honours that.

## Missing extensions

The `ExtensionsPresent` condition on `TalosStorage` reads the Nodes'
`extensions.talos.dev/*` labels. A node missing one holds `Ready` False, reason
`ExtensionsMissing`, naming the extensions and the upgrade.

Adding one is an installer-image change: `machine.install.extensions` does
nothing since Talos 1.10, so it takes a new schematic and an upgrade that
reboots every node (Talos treats it as an upgrade even when the version does
not move). Ichor does the first part (ADR 0173):

1. Registers a schematic with the preset's extensions at Image Factory
   (`spec.imageFactoryURL` of the cluster, `https://factory.talos.dev` by
   default; the controller needs https access to it).
2. Writes its ID to `spec.schematic.id` through the ServiceAccount
   `ichor-storage-extensions`.
3. Creates a paused Talos upgrade operation. Nothing reboots until a person
   approves it (`ichor approve operation <name>`, or Approve in the UI).

It does this only over an unset or stock schematic. One Ichor cannot read is
never changed, and `Ready` says which extensions to build in (reason
`SchematicNotMergeable`).

## The node-operation hook

Shared by removal, upgrades and reboot-requiring config changes: mark the node
unschedulable in the storage system, request evacuation, wait until no replica
lives only there, then drain; afterwards re-enable scheduling. The wait has a
timeout that pauses the rollout rather than proceeding. The hook is an interface
so other integrations can use it. A preset may have none: `rook-v1` has none, and
its nodes go straight to the drain, gated by Rook's own disruption budgets
(ADR 0167).

### `longhorn-v1`

Write side, from longhorn-manager v1.9.1's type declarations:
`nodes.longhorn.io/v1beta2` `NodeSpec` carries `allowScheduling` and
`evictionRequested` (bool), and `DiskSpec` the same under `spec.disks[<name>]`,
so a hook can evict a whole node. Per-disk eviction is not built, and will be
when a preset needs it. The order is fixed: Longhorn's documentation says eviction
takes effect only where scheduling is already disabled, so
`allowScheduling: false` lands first.

Read side, from longhorn-manager v1.9.1's controller source: there is no
"eviction done" status field. `DiskStatus.ScheduledReplica` is the scheduler's
capacity bookkeeping and does not reliably reach zero when eviction finishes.
In `VolumeController.cleanupEvictionRequestedReplicas`, once a volume has a
healthy, non-evicting replica elsewhere, it deletes the `Replica` whose
`spec.evictionRequested` is true. So "no replica lives only there" means no
`replicas.longhorn.io` object remains with `spec.nodeID` naming the target:
an absence of objects, not a counter at zero.

`EvacuationStatus` (`pkg/storage/evacuation.go`) takes that count, read by the
caller, and the elapsed time against `TalosStorage.spec.evacuationTimeout`. It
returns done, still-waiting, or paused, and never proceeds to drain once the
timeout passes with replicas remaining.

### Longhorn versions

No supported range is stated, and none is enforced. Ichor does not install
Longhorn (layer 2). The preset and the hook use `nodes.longhorn.io`,
`replicas.longhorn.io` and `settings.longhorn.io` at `v1beta2`, and nothing in
`pkg/storage` or `internal/controller` reads a Longhorn version or gates on one.
The version-specific statements above cite longhorn-manager v1.9.1; ADR 0025's
amendments cite webhook lines in v1.11.0, v1.12.1 and v1.13.0, and a comment in
`pkg/storage/longhornv1.go` claims the API is unchanged since 1.12.1. This page
adopts none of those citations.
