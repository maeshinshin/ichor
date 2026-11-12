# 0050. `TalosStorage` layer 1 renders as the system layer, selected by one shared rule, with a status controller

Status: Accepted · Date: 2026-10-26 · Design section: Storage, Patch layers
Decided: Render a `TalosStorage`'s Talos-side preparation as part of the system layer (below every patch), choose the governing `TalosStorage` with one `storage.Select` shared with the evacuation hook, hand `Render` a single `*SystemInputs` instead of a growing argument list, and report `Ready` from the convergence of the machines it governs

## Context

ADR 0023 built the `longhorn-v1` preset but nothing rendered it; ADR 0025/0026 wired layer 4
(evacuation). Layer 1 never reached a node and `TalosStorage.status` was never written, so
Argo CD's sync waves had nothing to wait on. Wiring it to the real Talos 1.14.3 validator also
showed that the preset ADR 0023 shipped could not have worked (below).

## Decisions

**1. Patch layer: system.** The preparation is merged after the base config and the registries
(ADR 0027) and before every `TalosConfigPatch`. It is Ichor-generated and not patch-shaped; a
user who needs a different mount option or disk size overrides it with a patch, and one
who wants no preparation removes the `TalosStorage` (a patch cannot delete the volume).
A machine-layer `UserVolumeConfig` named `longhorn` wins over it. A patch replacing
that document must be complete (Talos then validates it alone: a diskSelector is required).

**2. Which nodes.** `storage.Select(cluster, storages, nodeLabels)` is the only implementation
of the rule: same namespace and `clusterRef`, `spec.nodes` matches, several matches resolve to the
first by name. `LonghornEvacuator.Resolve` (layer 4) and `render.StorageFor` (layer 1, also used
by the input fingerprint and the status controller) both call it. The label *source* differs
on purpose: evacuation matches the live Kubernetes Node (it needs the Node to exist);
rendering matches `TalosMachine.spec.nodeLabels`, the declared labels, because at first render
no Node exists and a render that depended on live Node state would flap. A selector should
therefore use labels set through `spec.nodeLabels`. (When this ADR was written
`spec.nodeLabels` was not rendered onto the Node, so layer 1 and layer 4 could disagree;
ADR 0052 renders it.)

**3. `Render`'s input.** The `auth` parameter became `sys *SystemInputs` (registry
credentials plus the namespace's `TalosStorage`s), built by `render.ResolveSystemInputs`.
It is still one required positional argument, so a caller that forgets to resolve it
does not compile; the next system-layer input extends the struct, not the signature. `nil` means
none and is for tests and `ichor init`. The machine controller resolves it once per reconcile and
feeds the same value to `Render` and to `desiredInputsHash`, which now includes the governing
`TalosStorage`'s generation, so editing one reads as a desired-state change and not as drift.
The machine controller also watches `TalosStorage` so converged nodes pick it up.

**4. Status.** `Ready` (what Argo CD reads) is True when at least one approved machine is governed
by this `TalosStorage` and every such machine is `Ready=True` with an `observedInputsHash` equal
to the one it would compute now (a machine that converged before the `TalosStorage` existed
does not count). False reasons: `NoNodesSelected`, `NodesNotConverged` (lists the
machines), `ClusterNotFound`. `StorageSystemConsistent` is informational and never gates `Ready`:
it reads Longhorn's `settings.longhorn.io` object `create-default-disk-labeled-nodes` (top-level
`value`, longhorn-manager v1.9.1 `setting.go` / `types/setting.go`) through
the preset's `CheckConsistency`. Longhorn or its CRD absent is `Unknown/NotInstalled`, not an error.
It must not gate `Ready`: the setting lives in the storage Application's values, which the held
wave delivers. The read goes through `mgr.GetAPIReader()` (no informer on a possibly absent
CRD) and is repolled every two minutes. Ichor writes no Longhorn object here.
Not done: the extension check (`CheckExtensions`). Nothing in Ichor knows a node's installed
extensions (the cluster only holds an opaque schematic ID); it needs a Talos API read and is
left for that work. Argo CD's Lua health check for `TalosStorage` ships with this (ADR 0046).

**5. No storage system is installed or operated.** Disks register through the Node label and
annotation the preset returns; Longhorn objects are only read (setting) and, by layer 4, written
as ADR 0025 already decided.

## What wiring it to Talos 1.14.3 corrected in the ADR 0023 preset

- `UserVolumeConfig` is rejected without `minSize` or `maxSize`: the preset now sets `maxSize: 100%`.
- Under a v1.14 contract the base config uses standalone `KubeletConfig` and `KubeNodeConfig`
  documents, and Talos rejects the legacy `machine.kubelet` / `machine.nodeLabels` next to them.
  The preset no longer returns the legacy `Config` document: `TalosPatches` is the volume only,
  `KubeletMounts()` is separate data. Render puts the Node label/annotation into a `KubeNodeConfig`
  document under 1.14+ and into `machine.nodeLabels`, `nodeAnnotations` and `kubelet.extraMounts` below it.
- The standalone `KubeletConfig` has no extra-mount field, so under 1.14+ **the kubelet bind mount
  from Longhorn's Talos doc is not rendered**. Talos itself bind-mounts `/var/mnt` into the kubelet
  (`rslave`, read-only); whether Longhorn works on that without the `rshared` read-write mount is
  not known.

## Also fixed

The controller's role had no rule for `talosstorages`, `longhorn.io` nodes/replicas, so layer 4 could not
have run in-cluster. Rules added (cluster-scoped, since the Longhorn namespace is not fixed and a
RoleBinding into a namespace that may not exist would fail the install): `talosstorages` get/list/watch,
`talosstorages/status`, Longhorn `nodes` get/update, `replicas` list, `settings` get restricted to the one
setting name.

## Consequences

- Kernel modules and sysctls: `longhorn-v1` emits none today (ADR 0023); a preset that
  needs them returns them from `TalosPatches` and they arrive the same way.
- Patches do not trigger machine reconciles (only `TalosStorage` does); unchanged here.

## Amendment: the UI backend reads TalosStorage

The UI's effective-config view renders layer 1, so the `ichor-ui` ClusterRole needs `list` on `talosstorages`
(see the amendment in ADR 0018).

## Amendment: the extension check is wired (ExtensionsPresent)

A `longhorn-v1` TalosStorage was Ready on nodes whose schematic lacked
`iscsi-tools` and `util-linux-tools`, and longhorn-manager crash-looped. The "Not done" note above is
superseded. `TalosStorageReconciler` now sets `ExtensionsPresent` (informational, never gates
`Ready`, never blocks the rest of the reconcile): for each governed machine it finds the Kubernetes
Node (`spec.hostname`, else the Node whose address is the machine endpoint) and reads its
`extensions.talos.dev/<name>` labels, mapping each to `siderolabs/<name>` for `Preset.CheckExtensions`.
`False/MissingExtensions` names each node and its missing extensions and says to add them to
`spec.schematic` and run `ichor upgrade talos`; `Unknown/NodesNotObserved` when no governed machine has
a Node yet; `True/AllPresent` otherwise.

Why the Node labels and not the Talos API: Talos itself publishes them from the booted image, the
controller already reads Nodes (no new credentials, no Talos client or per-node dial in a status
controller), and it reflects what is running, not what a schematic ID claims. The cost is that a node
not yet joined has no labels, hence Unknown rather than False. The UI shows the condition in the
generic condition list, and `ExtensionsPresent=False` renders as a failure.
