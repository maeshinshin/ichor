# 0023. `TalosStorage` and the `longhorn-v1` disk preset

Status: Accepted · Date: 2026-10-25 · Design section: 4.4 (Storage)

## Context

The design's `### Storage` section splits storage work into four layers and gives Ichor the
first (Talos-side preparation: volumes, extensions, kernel modules, sysctls) and the fourth
(node-operation coordination: evacuate before drain). Layers 2 and 3 — installing the storage
system and operating its volumes/replicas/settings — belong to Argo CD and the storage system's
own controller, never Ichor. This ADR covers only layer 1: a `TalosStorage` CRD and a
`longhorn-v1` preset producing the Talos-side config a node needs before Longhorn can use one of
its disks. Layer 4 (the node-operation hook: mark unschedulable, request evacuation, wait, drain)
is a separate work unit and is not built here — `TalosStorage.spec.evacuationTimeout` exists so
that hook has somewhere to read its timeout from, but nothing in this change calls it.

The design leaves one thing unresolved going in: the exact Longhorn node label/annotation and
evacuation field names per version, listed as an open question. Resolving it (with citations, not
assumption) was this task's other half.

## What was verified, and against what

Checked against Longhorn's official docs (`longhorn.io/docs/1.12.1` and `1.13.0`, the latter
current stable at the time of writing) and `longhorn-manager`'s own source
(`github.com/longhorn/longhorn-manager`, tag `v1.12.1`, `k8s/pkg/apis/longhorn/v1beta2/node.go`) —
not recalled from memory, not guessed:

- **Node registration is two plain Kubernetes Node fields, not a Longhorn CR.** The label
  `node.longhorn.io/create-default-disk` set to `"config"` tells Longhorn's node controller to
  read the disk list from the annotation `node.longhorn.io/default-disks-config` (a JSON array of
  `{path, allowScheduling, [tags]}`) instead of auto-creating a default disk from Longhorn's own
  `default-data-path` setting. This matches the design's "registered ... through its own node
  label and annotation mechanism, not by writing its CRs" directly, and has been stable across
  the two doc versions checked.
- **The matching "is this actually honoured" setting is Longhorn's own `create-default-disk-
  labeled-nodes` setting.** If it is off, Longhorn ignores the label/annotation pair entirely and
  auto-creates (or doesn't) by its own default-path logic — this is the live/consistency check the
  design calls for ("a consistency check confirms the storage system is configured to honour
  that"), and is why `Preset.CheckConsistency` takes a `LiveConfig.CreateDefaultDiskOnLabeledNodes`
  bool rather than assuming the label is enough on its own.
- **Talos-side mount path changed between Talos versions, not Longhorn versions.** Longhorn's
  Talos Linux support doc gives `/var/mnt/longhorn` for Talos ≥1.10 (a `UserVolumeConfig`-based
  setup) and `/var/lib/longhorn` for older Talos. Ichor targets Talos 1.14.3 (ADR 0003), so only
  the newer path applies; `longhornMountPath` in `pkg/storage/longhornv1.go` is the one value, not
  a version switch.
- **The required system extensions are `siderolabs/iscsi-tools` and `siderolabs/util-linux-tools`**,
  per the same support doc. These are Image Factory schematic/installer-image concerns, not a
  `TalosConfigPatch` document (`machine.install.extensions` lists already-baked extension image
  refs — a different mechanism, verified during the v1alpha1.Config walk in ADR 0003 — it does not
  make an extension appear in an image that was not built with it). `longhornV1.CheckExtensions`
  therefore verifies and blocks with a clear error; it does not and cannot generate the extension
  into existence.

**Not resolved, left to a later look if it matters:** the exact `nodes.longhorn.io` CRD field
names for eviction (`NodeSpec.AllowScheduling`/`EvictionRequested`,
`DiskSpec.AllowScheduling`/`EvictionRequested`, per the same source read) are layer 4's concern,
not layer 1's — recorded here since they were read during the same research pass, not because
this change uses them. The open question in `docs/design/architecture.md` is removed because
layer 1's half of it (registration) is now resolved and cited; layer 4's half (eviction) is this
ADR's explicit non-scope, not a remaining unknown to track under the same bullet.

## Decision

- **`TalosStorage`** (`api/v1alpha1/talosstorage_types.go`, scaffolded with
  `kubebuilder create api`) stays exactly as thin as the design asks:
  `spec.clusterRef` (which `TalosCluster`), `spec.nodes` (a `metav1.LabelSelector` over Kubernetes
  Node labels — which nodes get storage prepared), `spec.diskPreset` (an enum, `longhorn-v1` the
  only value for now), `spec.diskSelector` (a Talos disk-selector CEL expression, the same
  language `provisioning.diskSelector.match` already uses in `VolumeConfig`/`UserVolumeConfig`/
  `RawVolumeConfig`), and `spec.evacuationTimeout` (`*metav1.Duration`, read by layer 4, not this
  change). `status.conditions` carries a kstatus-compatible `Ready` condition so, per the design,
  "Argo CD's sync waves can hold the storage Application until the Talos side is ready" — no
  reconciler sets it yet; that is controller work, not scaffolding work, and is not part of this
  change either.
- **`pkg/storage.Preset`** is the same shape `pkg/cni.Preset` already established for the CNI
  hand-over: pure Go, no cluster calls, returns data for a caller to apply.
  `RequiredExtensions`/`CheckExtensions` cover the installer-image gate; `TalosPatches` returns the
  `UserVolumeConfig` document (named `longhorn`, matching Longhorn's own example) plus the `Config`
  document adding the kubelet bind mount at `longhornMountPath` the support doc's example shows
  alongside it — without the mount, the volume is invisible to the kubelet and so to Longhorn;
  `NodeLabels`/`NodeAnnotations` return the two Longhorn fields verified above; `CheckConsistency`
  implements the live-settings check. `NewPreset` is the only switch on `diskPreset`'s string
  value, so a second preset (`longhorn-v2`, `raw`, `filesystem`, all named in the design's roadmap)
  is a new `Preset` implementation and one new `case`, nothing else.
- **Scaffolded, not hand-written.** `kubebuilder create api --group ichor --version v1alpha1
  --kind TalosStorage --resource --controller=false` generated `talosstorage_types.go`'s skeleton,
  `PROJECT`, and the three `kustomization.yaml` entries; `--controller=false` because layer 1's
  reconciler is not part of this change (there is nothing yet for one to do: `Preset` is a library,
  not wired to a controller). `make manifests` generated the CRD, RBAC roles and deepcopy; `dist/
  chart`'s CRD and RBAC templates were synced by hand (the header-reconstruction technique already
  established for `TalosConfigPatch`/`TalosCluster`), never via `kubebuilder edit
  --plugins=helm/v2-alpha`, which would revert unrelated hand-edits already in the chart.

## Consequences

- Layer 1 is complete and tested (`pkg/storage`, 95%+ coverage): given a disk preset name and a
  disk selector, Ichor can produce the exact `TalosConfigPatch` documents and node labels/
  annotations Longhorn needs, and can tell the caller before anything is applied whether the
  node's installer image is missing a required extension.
- Nothing in this change installs, configures, or talks to Longhorn, writes a `nodes.longhorn.io`
  resource, or reconciles `TalosStorage`. Those are, respectively, Argo CD's job, out of scope by
  design, layer 4 (a separate work unit), and controller work for a later change once there is a
  reconciler to write.
- A second preset is additive: implement `Preset`, add a `case` to `NewPreset`, extend the CRD
  enum. Nothing about `TalosStorageSpec` assumes Longhorn specifically.

## Alternatives considered

Writing a Longhorn `nodes.longhorn.io` resource directly instead of using the label/annotation
mechanism. Rejected: the design is explicit that disks are registered "through its own node label
and annotation mechanism, not by writing its CRs," and Longhorn's node controller already owns
reconciling that CRD from the label/annotation pair — writing it directly would race the exact
controller meant to own it.

Giving `TalosStorage` a `--controller=true` scaffold now, even with an empty `Reconcile`. Rejected
as the kind of placeholder ponytail mode rules out: an empty reconciler is not load-bearing, adds
a watch and an RBAC surface for nothing, and the next change that actually needs one can scaffold
it then.
