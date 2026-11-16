# 0167. Rook/Ceph is a second `TalosStorage` preset, `rook-v1`: it renders no Talos volume and has no evacuation hook

Status: Accepted, amended · Date: 2026-11-16 · Design section: Storage · Supersedes in part: 0023, 0025

Decided: `TalosStorage` stays and `longhorn-v1` is not deprecated; `rook-v1` joins it as a second preset that renders no Talos volume document, reads no Rook object, and implements no evacuation hook; `diskSelector` becomes required per preset rather than for every `TalosStorage`

## Context

ADR 0023 made `TalosStorage` a thin layer-1 resource with one preset, `longhorn-v1`, and named the other storage systems only as a roadmap (`longhorn-v2`, `raw` "for Rook/Ceph-style consumers", `filesystem`). ADR 0025 gave layer 4 an `EvacuationHook` that every preset was expected to implement.

The maintainer decided on 2026-11-16: `TalosStorage` stays, `longhorn-v1` stays, and Rook/Ceph is prepared for as a second preset with a page in the web UI. This ADR records the preset's behaviour, the two places where the current code and CRD cannot express "a preset that writes nothing", and the one place where Rook already provides the safety that a Ceph hook would otherwise provide.

The name is `rook-v1`. The preset tests already name it: `pkg/storage/storage_test.go` and `pkg/storage/evacuation_test.go` assert that `NewPreset("rook-v1")` and `NewEvacuationHook("rook-v1")` are refused today, which is the test for an unknown preset and not a statement of the decision. `docs/design/architecture/storage.md` named the roadmap entry `raw`; this ADR replaces that name with `rook-v1` and the storage page is updated to match.

### What Rook needs from the node

Rook's prerequisites at v1.19.6 (https://github.com/rook/rook/blob/v1.19.6/Documentation/Getting-Started/Prerequisites/prerequisites.md) say:

> To configure the Ceph storage cluster, at least one of these local storage types is required:
> * Raw devices (no partitions or formatted filesystems)
> * Raw partitions (no formatted filesystem)
> * LVM Logical Volumes (no formatted filesystem)
> * Persistent Volumes available from a storage class in `block` mode

and, on LVM:

> Ceph OSDs have a dependency on LVM in the following scenarios:
> * If encryption is enabled (`encryptedDevice: "true"` in the cluster CR)
> * A `metadata` device is specified
> * `osdsPerDevice` is greater than 1

So an OSD needs an unformatted device or an unformatted partition, and LVM only for the three cases above. Ichor's layer 1 must not put a filesystem on the disk Rook will use.

### What Talos v1.14 provides

The Talos v1.14 `UserVolumeConfig` reference says `filesystem: type: xfs # Filesystem type. Default is xfs.` (https://github.com/siderolabs/talos/blob/v1.14.0/website/content/v1.14/reference/configuration/block/uservolumeconfig.md). A `UserVolumeConfig` therefore formats the disk, and Rook cannot use it. `longhorn-v1` renders one because Longhorn needs a formatted mount.

The Talos v1.14 raw volume page (https://github.com/siderolabs/docs/blob/main/public/talos/v1.14/configure-your-talos-cluster/storage-and-disk-management/disk-management/raw.mdx) says that "Raw Volumes allow allocating an unformatted partition", that the label is `r-<volume-name>`, and carries this note:

> Note: Ceph will not create a partition if the partition label contains the substring `ceph`. Avoid using such names for your labels.

Removing a raw volume's configuration does not wipe the disk. The same page says "The actual disk data hasn't been removed yet, so you can re-apply the raw volume configuration back and it will be re-provisioned on the same disk", and wiping is a separate `talosctl wipe disk ... --drop-partition` step.

### Rook's own drain gate

The `rook-ceph-cluster` chart's `values.yaml` at v1.19.6 sets `disruptionManagement.managePodBudgets: true` (https://github.com/rook/rook/blob/v1.19.6/deploy/charts/rook-ceph-cluster/values.yaml). Rook's cluster CRD documentation at the same tag says that with it set, "the operator will create and manage PodDisruptionBudgets for OSD, Mon, RGW, and MDS daemons ... The operator will block eviction of OSDs by default and unblock them safely when drains are detected" (https://github.com/rook/rook/blob/v1.19.6/Documentation/CRDs/Cluster/ceph-cluster-crd.md).

Rook's managed-PDB design at the same tag (https://github.com/rook/rook/blob/v1.19.6/design/ceph/ceph-managed-disruptionbudgets.md) describes the mechanism: a default `rook-ceph-osd` budget with `maxUnavailable=1`; once a node is drained and an OSD goes down, the operator creates blocking budgets "(`maxUnavailable=0`) for all other failure domains" and deletes the default one; once the drained OSDs are back and "all the pgs are `active+clean`", it "Restores the default PDB" and removes the blocking ones. Drain is detected from the node being unschedulable: "If a node on which the OSD is suppose to run is `unscheduleable` then the operator considers that node to be draining."

Ichor's drain cordons the node first and then evicts through the Eviction API, so PodDisruptionBudgets apply: `newDrainHelper` in `internal/controller/drain.go` says so, and a refused eviction is retried until the step's timeout.

### What the current code does

- `pkg/storage/longhornv1.go`, `longhornV1.TalosPatches`, returns the error `storage: diskSelector is required` when the selector is empty (line 86). A preset that needs no disk selector cannot pass through this check.
- `api/v1alpha1/talosstorage_types.go` gives `spec.diskSelector` the field-level rule `self != ''` with the message "diskSelector is required" (line 50). `spec.diskPreset` is `Enum=longhorn-v1` (line 43). Adding a preset therefore needs a CRD change, and the rule as written forces a selector on every preset.
- `pkg/render/system.go`, `injectStorage`, calls `preset.TalosPatches(chosen.Spec.DiskSelector)` for whichever preset governs the machine, so the selector reaches every preset.
- `pkg/storage.NewEvacuationHook` is `NewPreset` followed by a type assertion to `EvacuationHook` (`pkg/storage/evacuation.go`). A preset with no hook has no value to return there.
- `TalosStorage` has no admission webhook: nothing under `api/` or `internal/` registers one for it.

### Two wording discrepancies in the existing code

- Matching. `storage.Select` is called with `m.Spec.NodeLabels`, the labels declared on the `TalosMachine` (`pkg/render/system.go`, `StorageFor`). `TalosConfigPatch` targets match the same map (`selectorMatches` in `pkg/render/render.go`). The field comments in `talosstorage_types.go` (`nodes`) and `talosconfigpatch_types.go` (`labelSelector`) both say "Kubernetes Node labels", which is what the evacuation path matches (`internal/controller/evacuation.go`, `storage.Select(cluster, storages.Items, n.Labels)`). Layer 1 and layer 4 match different label sources, as ADR 0050 decided. The comments are wrong for layer 1 and this ADR does not change the code.
- `TalosConfigPatch` declares a status with conditions (`TalosConfigPatchStatus`), but no controller writes it. The status writer that `TalosStorage` has is not built for patches, and nothing in the maintainer's own setup needs one: their Argo CD orders Rook by sync wave and does not read Ichor's patch state.

## Decision

1. **`TalosStorage` stays.** Its shape, `clusterRef`, `nodes`, `diskPreset`, `diskSelector` and `evacuationTimeout`, is kept. No field is removed or renamed.

2. **`longhorn-v1` stays.** It is not deprecated and its behaviour is unchanged.

3. **`rook-v1` is added.** The enum gains `rook-v1`. The addition is a new allowed value, so no stored object changes.

4. **`rook-v1` renders no Talos volume document.** It leaves the disk alone and Rook consumes the raw device. It renders no `UserVolumeConfig`, because that formats the disk, and no `RawVolumeConfig`, for two reasons:
   - A raw volume is a partition Talos creates, and its label is `r-<name>`. A preset that creates one would have to choose the name and the disk, and the `ceph` substring rule would be the preset's to enforce. That is an operator decision, made per disk, and the guide puts it in a patch the operator writes.
   - Rook accepts the raw device as it is, so the partition is not needed.

   `rook-v1` renders no extension requirement, no kubelet mount, and no Node label or annotation. Rook's data directory `dataDirHostPath` defaults to `/var/lib/rook` (`deploy/charts/rook-ceph-cluster/values.yaml`, under `cephClusterSpec`), and the Talos v1.14 architecture page says `/var` "is writable and used by `etcd` (in the case of control plane nodes), the kubelet, and the CRI (containerd)". Nothing else is mounted. Whether Rook needs the RBD kernel module on Talos is not established by this ADR and is not claimed.

5. **`RawVolumeConfig` stays available, written by the operator in a `TalosConfigPatch`.** The guide documents it, with Talos's `ceph` warning. The preset does not render one.

6. **`diskSelector` is required per preset.** The `Enum` and the field keep their shape. The field-level rule `self != ''` is removed, and an object-level rule on `TalosStorageSpec` replaces it:
   `self.diskPreset == 'rook-v1' || self.diskSelector != ''`, with the message "diskSelector is required for diskPreset longhorn-v1".
   - `longhornV1.TalosPatches` keeps its empty-selector error, since `longhorn-v1` needs a selector.
   - `rook-v1` accepts a selector and ignores it. The field comment says so.
   - This is an API change, so it is recorded here and not made quietly. Relaxing a rule removes no field and refuses no object that was valid before: every object the old rule accepted, the new one accepts. Appending a value to an enum has the same property. The installer's `checkAdditive` compares property names and types and does not compare validation rules or enum values (`internal/installer/crdcheck.go`, `schemaCovers`), so the installer does not refuse this change, and it does not guard the reverse. A downgrade to a build without `rook-v1` would leave stored `rook-v1` objects that the older enum refuses on the next write. That gap is not closed here. Whether the installer should refuse a removed enum value is a decision for the maintainer, and it is recorded as open below.

7. **`rook-v1` implements no evacuation hook.** The node-operation path must treat a preset with no hook as nothing to evacuate and go straight to the drain. It must not pause or fail. The drain is then gated by Rook's own mechanism (decision 9). `NewEvacuationHook("rook-v1")` returns a named error for "no hook" so that the caller can tell it apart from an unknown preset. The implementation changes the test that today asserts `rook-v1` is refused.

8. **The safety of decision 7 depends on `managePodBudgets` being on.** The chart default is `true`. A cluster that sets it to `false` has no Ceph gate on drains, and the operator must not rely on Ichor to supply one. The guide says so.

9. **Ichor runs none of Ceph's own procedures.** `ceph osd out`, waiting for `active+clean`, and purging an OSD belong to Rook and Ceph and to the operator. `docs/design/architecture/motivation.md` lists "Replacing the storage system's own controller" as a non-goal, and the storage page splits layer 2 (install and upgrade the storage system) and layer 3 (volumes, replicas, disks) to the storage system.

10. **Ichor does not choose OSD devices.** The `rook-ceph-cluster` chart defaults to `useAllNodes: true` and `useAllDevices: true` (`cephClusterSpec.storage`). Rook documents `useAllDevices` as "**Not recommended** unless you have a very controlled environment where you will not risk formatting of devices with existing data. When `true`, all devices and partitions will be used." The guide tells the reader to name devices.

## Supersession

This ADR supersedes parts of two earlier records. Their Decision lines are not edited; the Status lines carry the marker, as ADR 0013 and ADR 0099 do.

- **ADR 0023** (`docs/adr/0023-talos-storage-and-longhorn-v1-preset.md`):
  - "`spec.diskPreset` (an enum, `longhorn-v1` the only value for now)": the enum now has `rook-v1` as well (decision 3).
  - "`spec.diskSelector` ... (a Talos disk-selector CEL expression ...)": the selector is required for `longhorn-v1` and not for `rook-v1` (decision 6).
  - "A second preset is additive: implement `Preset`, add a `case` to `NewPreset`, extend the CRD enum": still true. Decision 6 is the one part of that paragraph that the CRD rule does not permit without the change.
  - Every other part stands, including layer 1's scope, the Longhorn facts, and the `longhorn-v1` decisions.
- **ADR 0025** (`docs/adr/0025-storage-evacuation-hook.md`): "A second preset that cannot evacuate live ... is a new `EvacuationHook` implementation": a preset may have no hook at all, and this is `rook-v1` (decision 7). The `EvacuationHook` interface and `longhorn-v1`'s implementation stand.

## Consequences

- Layer 1 has two presets, and `TalosStorage` remains the one resource that prepares a node for either storage system.
- A node governed by `rook-v1` gets no Talos volume, no kubelet mount and no Node label. Its disk is Rook's. The UI can show the preset, and the guide is the operator's path for the raw-partition alternative.
- Ichor's drain is the mechanism that meets Rook's gate. A drain blocked by a Rook budget waits, as the drain code already does, until the step times out and the next reconcile continues.
- The CRD change in decision 6 is an API change. It is additive for every existing object, and the installer does not check it.

## Open

- Whether the installer should refuse a downgrade that would drop an enum value (`rook-v1`) or a relaxed validation. Today it does not check either.
- Whether `rook-v1` needs the RBD kernel module on Talos. Rook's prerequisites say Ceph requires an RBD-capable kernel; this ADR does not establish how Talos's kernel provides it and does not claim it.
- Whether the status writer for `TalosConfigPatch` is wanted. Nothing in the current design asks for it.

## Amendment (2026-11-17): the diskSelector rule is two object-level CEL rules, and `rook-v1` refuses a selector

Decision 6 and the ADR 0023 entry under Supersession say that the rule is `self.diskPreset == 'rook-v1' || self.diskSelector != ''`, with the message "diskSelector is required for diskPreset longhorn-v1", and that `rook-v1` accepts a selector and ignores it. The code does not do that. This amendment replaces those statements; the Decision text above is not edited, and the Context keeps its account of `main` before this ADR was implemented.

The rules on `TalosStorageSpec` in `api/v1alpha1/talosstorage_types.go`, as the generated CRD carries them, are these two and no others:

```text
rule: self.diskPreset != 'longhorn-v1' || (has(self.diskSelector) && size(self.diskSelector) > 0)
message: diskSelector is required for longhorn-v1

rule: self.diskPreset != 'rook-v1' || !has(self.diskSelector)
message: rook-v1 writes no disk config, so a diskSelector would do nothing: remove spec.diskSelector. To move a TalosStorage from longhorn-v1 to rook-v1, change diskPreset and remove diskSelector in one update
```

As implemented, decision 6 reads:

- The field-level rule `self != ''` is gone, and so is the `required` marker. `diskSelector` is `+optional` with `omitempty`, and the schema's `required` list no longer names it. The two object-level rules above are the only checks on the field.
- `longhorn-v1` requires a non-empty selector. An empty string is refused, as `diskSelector: ""` is present and has size zero; a Go client that leaves the field empty omits it and is refused the same way.
- `rook-v1` refuses a selector. A `diskSelector` on a `rook-v1` object is rejected at admission; it is not accepted and ignored. The preset's Go code still returns no documents for any selector, for a caller that does not go through admission.
- Moving a `TalosStorage` from `longhorn-v1` to `rook-v1` is one update that changes `diskPreset` and removes `diskSelector`. Either change alone is refused, so the two cannot be made in separate updates.
- The ADR 0023 supersession entry is corrected accordingly: the selector is required for `longhorn-v1` and refused for `rook-v1`.

Compatibility: before this ADR, the CRD required a non-empty `diskSelector` on every object. The new rules require it on `longhorn-v1` with the same effect, and `rook-v1` had no accepted objects, so no stored object is refused by the new rules. The enum gains a value and loses no field. The statement in decision 6 that every object the old rule accepted, the new one accepts, holds for the rules as implemented.

The message changed from "diskSelector is required" to the two messages quoted above.
