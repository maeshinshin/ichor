# 0101. Argo CD and Kokumi are no longer part of Ichor

Status: Accepted · Date: 2026-10-29 · Design section: Bootstrap, CNI · Supersedes in part: 0099

Decided: Argo CD and Kokumi are not requirements and Ichor no longer installs, publishes or manages either; the whole standard stack (the stack reconciler, the `ichor-stack` Jobs and RBAC, `pkg/stack`, the hidden `ichor stack apply`, the stack wait and Kokumi password print in `ichor init`, the stack network policies) is deleted, while Cilium stays Ichor's own Helm release run by the `ichor-cni` Job, and the CRD fields it left unused (`TalosCluster.spec.gitops`, `TalosCluster.spec.cni.handover`, `IchorInstallation.spec.managementMode`, `spec.stack`, `status.stack`) are removed, not deprecated, through the installer's explicit retired-field list; GitOps tools, Flux included, are permanently out of scope

## Context

ADR 0099 made Argo CD and Kokumi the standard stack: `ichor init` always installed them, the controller published their bundles into Kokumi's in-cluster registry, and Argo CD synced from there. Neither is a requirement. Storage, monitoring and any GitOps tool are the operator's to install, as the design said before ADR 0099. ADR 0099 also made Cilium the only CNI and Ichor's own Helm release; that part is wanted and untouched.

## Decision

Removed:

- The stack reconciler in the `IchorInstallation` controller (`internal/controller/stack.go`, `stack_job.go`) and its wiring: `StackEnabled`, `RegistryHost`, the `status.stack` writes and the requeue they drove.
- `internal/stackapply` and the hidden command `ichor stack apply`; the `ichor-stack` Jobs.
- `pkg/stack`: the embedded Argo CD chart and Kokumi manifest, the bundle builder, the OCI push, and the bundle policies. The Makefile targets `argocd-chart` and `kokumi-install`. The dependencies that became unused (`go mod tidy`).
- The `ichor-stack` ServiceAccount and ClusterRole (`config/rbac/ichor-stack`, the chart's `rbac/ichor-stack.yaml`), the controller's rights on Kokumi `orders` and Argo CD `applications`, the `stack` CiliumNetworkPolicy, and the controller's egress to Kokumi's registry. The controller's ClusterRole still holds no escalation, CRD or workload-create rights.
- In `ichor init`: the wait for `StackReady`, the Kokumi admin password print, and `spec.stack` on the `IchorInstallation` it creates. The wait for `CNIHandover` stays (`internal/cli/init_handover.go`), as does the `IchorInstallation`.
- The Argo CD and Kokumi sections of `README.md`, `docs/design/architecture.md` and the ui-demo README.

Kept:

- Cilium as Ichor's Helm release: the inline bootstrap manifest, the `ichor-cni` Job in `kube-system` and its RBAC, `spec.cni.cilium.values`, upgrades through `cni` operations, the kube-proxy switch, the `CNIHandover` condition and `ichor init` waiting for it. Everything in ADR 0099 and its amendments about Cilium stands.
- `IchorInstallation.spec.stack` and `status.stack` were first kept deprecated; they are now removed (see "Retired CRD fields"). Nothing reads or writes them. The `StackReady` condition is no longer set; an installed `IchorInstallation` that carries a stale one keeps it until the object is rewritten, and nothing depends on it.
- The detection of Ichor's own CRs and Deployment being tracked by a user's Argo CD or Flux, and the `argocd-cm` read behind it, which concern a user's GitOps tool, not an Ichor-installed one. (The GitOps, Git-tracking and PR-mode code is handled separately; see the GitOps section.)

## What remains of ADR 0099

ADR 0099 is superseded in part. These decisions stand: Cilium is the only CNI; it boots from the inline manifest and is then owned by Ichor as a Helm release run by an installer-style Job; the user's values layer lives on `TalosCluster.spec.cni.cilium.values`; `ichor-system` network policies (ADR 0100) are unchanged apart from losing the Kokumi egress rule and the `stack` policy. These do not: decisions about Kokumi, Argo CD, the in-cluster registry, Orders, bundles, `spec.stack`, `ichor-stack` Jobs and the stack's network policies.

## Consequences

- Existing clusters that already run the stack keep their Argo CD and Kokumi; Ichor just stops managing them. Removing them is the operator's job.
- A new `ichor init` finishes after the Cilium hand-over and installs nothing else.
- The CRD fields the stack left unused are removed through the installer's retired-field list (see "Retired CRD fields").

## GitOps

GitOps is no longer a requirement, so Ichor stops knowing about GitOps tools at all. The user may still run Argo CD or Flux over Ichor's custom resources; Ichor neither detects it nor reacts to it. This supersedes the tracked-detection bullet under "Kept" above: that code is gone too.

Removed:

- Detection of CRs tracked by Argo CD or Flux (`pkg/gitopstracking`, the `argocd-cm` read, the Flux inventory scan) and every refusal built on it: the tracked-write gate in `internal/uiapi` (machines, patches, discovery, install, operations, node actions, Cilium values), the same refusals in the CLI's in-process fallback, and the tracked-CR denial in the override webhook. The webhook keeps its other jobs: removing an `ichor.maesh.dev/*` finalizer, resetting a node, restoring etcd, the last-worker override, `spec.authentication` and secret-reference changes. It no longer exempts Argo CD or Flux service accounts by name; it still exempts Ichor's controller and the Kubernetes namespace and garbage collectors.
- Pull-request mode (`pkg/prmode`, `pkg/gitops`) and strict mode: no write opens a pull request, and approving a node always writes the `TalosMachine`. Override no longer means "write a tracked CR and open the matching pull request". It stays as the verb that guards the destructive operations.
- `GitOpsService`, `proto/ichor/v1alpha1/gitops.proto`, the external-links registry (`ichorUI.links`, `ICHOR_LINK_*`), `ichorUI.argocdNamespace` / `ICHOR_ARGOCD_NAMESPACE` and the CLI's `--argocd-namespace`.
- The UI GitOps page, its route, navigation entry and strings; the pull-request notice and override dialogs; the Cilium page's tracked-by banner and read-only state.
- The Argo CD health-check ConfigMap (ADR 0046): `argocdHealth`, `templates/argocd`, the Lua files and their test; and `examples/argocd-application.yaml`.
- RBAC: `argocd-cm` get, Flux `kustomizations` list and `applications` get for `ichor-ui`; `argocd-cm` get and Flux `kustomizations` list for the manager.
- The `IchorInstallation` `ExternalSuggested` condition and the controller's read of its own Deployment.

Retired CRD fields (a deliberate exception to the append-only rule: these fields are gone for good):

- `TalosCluster.spec.gitops` (`mode`, `repository` and everything under it) and its Go types and CEL rules.
- `TalosCluster.spec.cni.handover` and its enum (ADR 0099 had already made it unused: the hand-over is always to Ichor).
- `IchorInstallation.spec.managementMode` and its enum. `ichor` was the only behaviour.
- `IchorInstallation.spec.stack` and `status.stack`, with `IchorStackSpec`, `IchorStackComponentSpec`, `IchorStackStatus`, `IchorStackComponentStatus` and the promotion enum (ADR 0099 added them).

Kept and not retired: `TalosCluster.spec.cni.custom` and the `flannel` and `custom` values of `spec.cni.preset`. They stay deprecated and refused by the webhook and the controller.

The retired-field mechanism: `internal/installer/crdcheck.go` refuses an install whose target CRD would drop a property the installed CRD has (a downgrade). It now carries `retiredFields`, an explicit list of `{crd, path}` entries (the CRD name and the dotted property path from the object root, such as `spec.gitops`, each with a comment citing the ADR). The check lets exactly those paths disappear, on exactly that CRD, and keeps refusing every other removal. The rule: a field is retired only by naming it in that list, one ADR per removal; there is no wildcard and no per-version or prefix match. Enum value changes are not checked by the installer, so dropping an enum alongside its field needs no entry.

Stored objects: once the field is out of the schema the API server prunes it on the next write, and no validation or webhook rejects an object that still carries it. The proto fields stay, deprecated, for wire compatibility.

Flux CD is permanently out of scope, like every GitOps tool. Ichor holds no Flux code, RBAC rule or wording.

Deprecated, kept (the append-only rule for wire formats):

- Proto fields, marked `deprecated = true` and never set: `tracked_by` (`GetCiliumValuesResponse`), `pull_request_url` and `notice` on the approve, reject, set-install, add-discovery, update-patch, delete-patch and create-operation responses, `override` on the approve, reject, set-install, add-discovery, update-patch and delete-patch requests (the server ignores it), `opens_pull_request` on `PlanOperationResponse` and `management_mode` on `GetIchorInstallationResponse`. `override` on node-action and etcd-restore requests is unchanged.

Superseded ADRs: 0015, 0028, 0037, 0038, 0044, 0046, 0051, 0056, 0062 in full; 0059 (decision 5 and the external mode) and 0065 (the tracked-write part; the verb and the destructive-operation checks stand) in part.

Consequences:

- A user who keeps Ichor's CRs in Git with Argo CD or Flux can still do so. Ichor writes them directly from the UI and CLI, and the user's tool may revert those writes. Ichor no longer warns about this.
- The UI backend and CLI have one write path, no Git token and no outbound call to a Git host.
