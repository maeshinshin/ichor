# 0106. HA findings: a patch is dry-rendered at admission, a restore refuses an in-cluster backup store, an unreachable node says so, and a dead control plane can be removed

Status: Accepted · Date: 2026-10-30 · Design section: Configuration, Backup and restore, Nodes · Touches: 0003, 0065

Decided: a `TalosConfigPatch` is dry-rendered by a validating webhook (create and update) against every approved machine of its cluster that it selects, with the controller's own `render.Render`, and refused with the render error when the render would fail and did not already fail without the patch; the generated `Config` mirror makes `machine`, `cluster`, `machine.type` and `machine.certSANs` optional (an additive loosening) so a sparse `Config` document no longer repeats them and no longer reads as "needs a reboot"; `ichor restore --live` and the `TalosEtcdBackup` condition `StoreInCluster` refuse or warn when the backup endpoint is inside the cluster being restored; a `TalosMachine` whose node cannot be read reports `Ready=False` `NodeUnreachable` with the endpoint and, when the Kubernetes Node shows another InternalIP, the likely new address (spec is never changed); `ichor node remove` of a control plane below the declared count is allowed when its etcd member is unhealthy or unreachable.

## Context

A hardware run on an Ichor-made three control plane cluster found four gaps (P2, P3, P4, H7). Each section below is one decision; later sections are added by the commit that implements them.

## Decisions

### 1. A patch that cannot render is refused at admission (P2)

A `Config` patch that set `cluster.apiServer.*` was accepted, then every machine went `RenderFailed`: "kube-apiserver config is already set in v1alpha1 config (.cluster.apiServer)". Under a multi-document contract the base config already carries a `KubeAPIServerConfig` document and Talos refuses the legacy block next to it. The controller hides the cause in the condition message, so the author saw only "rendering machine configuration failed".

The webhook `vtalosconfigpatch-v1alpha1.kb.io` (`internal/webhook/v1alpha1/talosconfigpatch_webhook.go`) reads the cluster, its secrets bundle and system inputs the way the controller does, renders each approved machine the patch selects with and without the patch, and refuses when only the render with the patch fails, with the render error in the message. It does not check when the secrets bundle is not readable yet, or the cluster is missing (nothing to render against), nor a machine that already fails without the patch (the patch did not break it, and the fix for the other cause must not be blocked). Delete is not checked. The cost is two renders per selected machine per write; renders take milliseconds.

CRD validation forced `machine.type` and `machine.certSANs` on a partial `Config` document because the mirror generator (ADR 0003) marks every machinery field without `omitempty` as required, and machinery tags those because a full machine config always has them. A patch is a sparse overlay, and the strategic merge ignores empty values, so `internal/tools/mirrorgen` now has a small table, `sparseFields`, that emits `Config.machine`, `Config.cluster`, `MachineConfig.type` and `MachineConfig.certSANs` as optional. This only relaxes a requirement; no field is removed or renamed. Other required fields (for example `controlPlane.endpoint` inside a `controlPlane` block) stay required, because they are required once their parent is present.

The "applying it needs a reboot" note came from the same cause: the zero-valued `machine.type`, `machine.certSANs` and the document `version` were counted as set paths, and `pkg/fieldmeta` treats an unknown path as reboot-required. `patchPaths` now drops the `version` leaf and empty or null leaves. The `cluster.apiServer.*` paths themselves are an API server restart, not a reboot (`pkg/fieldmeta`), and the preview now says so by reporting no reboot.
