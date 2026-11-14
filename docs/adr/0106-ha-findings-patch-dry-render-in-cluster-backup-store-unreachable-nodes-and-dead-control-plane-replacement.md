# 0106. HA findings: a patch is dry-rendered at admission, a restore refuses an in-cluster backup store, an unreachable node says so, and a dead control plane can be removed

Status: Accepted · Date: 2026-10-30 · Design section: Configuration, Backup and restore, Nodes · Touches: 0003, 0065

Decided: a `TalosConfigPatch` is dry-rendered by a validating webhook (create and update) against every approved machine of its cluster that it selects, with the controller's own `render.Render`, and refused with the render error when the render would fail and did not already fail without the patch; the generated `Config` mirror makes `machine`, `cluster`, `machine.type` and `machine.certSANs` optional (an additive loosening) so a sparse `Config` document no longer repeats them and no longer reads as "needs a reboot"; `ichor restore --live` and the `TalosEtcdBackup` condition `StoreInCluster` refuse or warn when the backup endpoint is inside the cluster being restored; a `TalosMachine` whose node cannot be read reports `Ready=False` `NodeUnreachable` with the endpoint and, when the Kubernetes Node shows another InternalIP, the likely new address (spec is never changed); `ichor delete node` of a control plane below the declared count is allowed when its etcd member is unhealthy or unreachable.

## Context

Four gaps in an Ichor-made three control plane cluster are closed here (P2, P3, P4, H7). Each section below is one decision.

## Decisions

### 1. A patch that cannot render is refused at admission (P2)

A `Config` patch that set `cluster.apiServer.*` was accepted, then every machine went `RenderFailed`: "kube-apiserver config is already set in v1alpha1 config (.cluster.apiServer)". Under a multi-document contract the base config already carries a `KubeAPIServerConfig` document and Talos refuses the legacy block next to it. The controller hides the cause in the condition message, so the author saw only "rendering machine configuration failed".

The webhook `vtalosconfigpatch-v1alpha1.kb.io` (`internal/webhook/v1alpha1/talosconfigpatch_webhook.go`) reads the cluster, its secrets bundle and system inputs the way the controller does, renders each approved machine the patch selects with and without the patch, and refuses when only the render with the patch fails, with the render error in the message. It does not check when the secrets bundle is not readable yet, or the cluster is missing (nothing to render against), nor a machine that already fails without the patch (the patch did not break it, and the fix for the other cause must not be blocked). Delete is not checked. The cost is two renders per selected machine per write; renders take milliseconds.

CRD validation forced `machine.type` and `machine.certSANs` on a partial `Config` document because the mirror generator (ADR 0003) marks every machinery field without `omitempty` as required, and machinery tags those because a full machine config always has them. A patch is a sparse overlay, and the strategic merge ignores empty values, so `internal/tools/mirrorgen` now has a small table, `sparseFields`, that emits `Config.machine`, `Config.cluster`, `MachineConfig.type` and `MachineConfig.certSANs` as optional. This only relaxes a requirement; no field is removed or renamed. Other required fields (for example `controlPlane.endpoint` inside a `controlPlane` block) stay required, because they are required once their parent is present.

The "applying it needs a reboot" note came from the same cause: the zero-valued `machine.type`, `machine.certSANs` and the document `version` were counted as set paths, and `pkg/fieldmeta` treats an unknown path as reboot-required. `patchPaths` now drops the `version` leaf and empty or null leaves. The `cluster.apiServer.*` paths themselves are an API server restart, not a reboot (`pkg/fieldmeta`), and the preview now says so by reporting no reboot.

### 2. A restore refuses a backup store inside the cluster (P3)

`ichor restore --live` with the S3 store in the same cluster (a minio Service) deadlocked: the restore marker is written to the bucket after the Kubernetes API, and so the in-cluster S3, is down. `etcdbackup.InClusterReason` (`internal/etcdbackup/incluster.go`) says a destination is in the cluster when the endpoint host is `*.svc` or `*.svc.cluster.local`, or an IP inside the pod or service ranges of `spec.network` (Talos's defaults, `10.244.0.0/16` and `10.96.0.0/12`, for a range the cluster did not declare). The etcd-restore plan sets `Blocked` to "... the backup store must live outside the cluster being restored ..."; `CreateOperation` refuses a blocked plan and `--override` is not consulted, because no reason can make the marker writable. The same check sets the warning condition `StoreInCluster` (True) on the `TalosEtcdBackup`, removed when the endpoint moves out; `Ready` is unaffected, backups still work. Not covered: a short name such as `minio.minio` or a host that resolves to a cluster address (only the literal forms are recognised), and a `TalosOperation` written directly with `kubectl`, which the controller does not re-check.

### 3. An unreachable node says so (P4)

A node whose IP changed kept `Ready=True` `Converged` with the stale endpoint. The machine controller already re-probes a converged machine every `reachabilityRecheck` (two minutes) and a failed read already sets `Ready=False` `CurrentConfigUnavailable`, but that reason names neither the endpoint nor the error, and it never changes however long the node stays away. The first failed read now sets a `NodeReachable=False` condition (its `lastTransitionTime` is when the failures began; the first successful read removes it, so no new status field is needed). Within `nodeUnreachableGrace` (60 seconds, long enough for a reboot) the reason stays `CurrentConfigUnavailable`; after it, `Ready=False` reason `NodeUnreachable` with "the node at <endpoint> has not answered for <duration>: <dial error>". When the Kubernetes Node of the machine (by `spec.hostname` or `status.nodeName`, else by `systemUUID` equal to the machine UUID) reports an InternalIP other than the dialled host, the message adds "spec.endpoint may need updating to <ip:port>". `spec` is never edited; a machine with a hostname is still followed automatically through `status.observedEndpoint` (ADR 0086), which this does not change.

### 4. A dead control plane can be removed below the declared count (H7)

`ichor delete node <dead cp>` was refused because removing it takes the cluster below `spec.controlPlane.count`, and "approve a replacement first" cannot finish while the dead etcd member is still listed (the new control plane's etcd stays in Preparing). Three places refuse, and two now make an exception for a control plane whose member is dead; the quorum and last-control-plane rules never bend (a two control plane cluster still cannot lose one, dead or not):

- The removal in the controller (`removal.go`, authoritative) reads etcd through a healthy sibling (`deadEtcdMember`, `removal_dead.go`): the sibling must list the member, and the departing machine must be unreachable, its etcd status must fail, or it must see no leader. A member that answers with a leader is healthy and the refusal stands, now saying so. Names that cannot be told, a sibling that cannot be dialed, or a member etcd does not list leave the refusal in place (nothing proves it dead); the one exception is a removal that already proceeded for a dead member (the `Removal` condition reason `DeadMemberRemoval` is the marker), so a round that resumes after the member left etcd goes on. When it proceeds it emits a Warning Event `DeadMemberRemoved` saying the count is below the declared one and a replacement should be approved, and it skips the storage evacuation and the drain, which wait for a kubelet that is not there and would never finish.
- The plan (`internal/uiapi/node_action.go`, `ichor delete node`, UI) cannot read etcd, so it allows the removal when the machine's `Ready` is False with reason `NodeUnreachable` (section 3), with a warning in the plan; the controller still decides. `node reset` keeps the count check, because a reset needs the node.
- The admission webhook (`ValidateDelete`) only ever checked quorum, not the declared count, and is unchanged.

### 5. An endpoint that answers as another node is reported

A machine whose endpoint answers as another node is reported as `EndpointIdentityMismatch`.

## Consequences

- The cluster keeps probing every `spec.discovery.addresses` entry after the machines are approved and emits an `AlreadyConfigured` Warning Event on the TalosCluster for each address every few minutes, including the stale address of a node that has left. On a network where addresses are reused it would probe an unrelated host. This is not changed here.
