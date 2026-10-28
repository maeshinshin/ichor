# 0101. Argo CD and Kokumi are no longer part of Ichor

Status: Accepted · Date: 2026-10-29 · Design section: Bootstrap, CNI · Supersedes in part: 0099

Decided: Argo CD and Kokumi are not requirements and Ichor no longer installs, publishes or manages either; the whole standard stack (the stack reconciler, the `ichor-stack` Jobs and RBAC, `pkg/stack`, the hidden `ichor stack apply`, the stack wait and Kokumi password print in `ichor init`, the stack network policies) is deleted, while Cilium stays Ichor's own Helm release run by the `ichor-cni` Job, and the CRD fields `IchorInstallation.spec.stack` and `status.stack` stay, deprecated and unused

## Context

ADR 0099 made Argo CD and Kokumi the standard stack: `ichor init` always installed them, the controller published their bundles into Kokumi's in-cluster registry, and Argo CD synced from there. The user has decided that neither is a requirement. Storage, monitoring and any GitOps tool are the operator's to install, as the design said before ADR 0099. ADR 0099 also made Cilium the only CNI and Ichor's own Helm release; that part is wanted and untouched.

## Decision

Removed:

- The stack reconciler in the `IchorInstallation` controller (`internal/controller/stack.go`, `stack_job.go`) and its wiring: `StackEnabled`, `RegistryHost`, the `status.stack` writes and the requeue they drove.
- `internal/stackapply` and the hidden command `ichor stack apply`; the `ichor-stack` Jobs.
- `pkg/stack`: the embedded Argo CD chart and Kokumi manifest, the bundle builder, the OCI push, and the bundle policies. The Makefile targets `argocd-chart` and `kokumi-install`. The dependencies that became unused (`go mod tidy`).
- The `ichor-stack` ServiceAccount and ClusterRole (`config/rbac/ichor-stack`, the chart's `rbac/ichor-stack.yaml`), the controller's rights on Kokumi `orders` and Argo CD `applications`, the `stack` CiliumNetworkPolicy, and the controller's egress to Kokumi's registry. `TestStackRBAC` became `TestControllerRBACHoldsNoEscalation`: the controller's ClusterRole still holds no escalation, CRD or workload-create rights.
- In `ichor init`: the wait for `StackReady`, the Kokumi admin password print, and `spec.stack` on the `IchorInstallation` it creates. The wait for `CNIHandover` stays (`internal/cli/init_handover.go`), as does the `IchorInstallation`.
- The Argo CD and Kokumi sections of `README.md`, `docs/design/architecture.md` and the ui-demo README.

Kept:

- Cilium as Ichor's Helm release: the inline bootstrap manifest, the `ichor-cni` Job in `kube-system` and its RBAC, `spec.cni.cilium.values`, upgrades through `cni` operations, the kube-proxy switch, the `CNIHandover` condition and `ichor init` waiting for it. Everything in ADR 0099 and its amendments about Cilium stands.
- `IchorInstallation.spec.stack` (`IchorStackSpec` and its component type) and `status.stack` (`IchorStackStatus`, `IchorStackComponentStatus`), per the append-only rule for CRD fields. They are marked deprecated and unused in the Go types and so in the CRD descriptions. Nothing reads or writes them. The `StackReady` condition is no longer set; an installed `IchorInstallation` that carries a stale one keeps it until the object is rewritten, and nothing depends on it.
- The detection of Ichor's own CRs and Deployment being tracked by a user's Argo CD or Flux, and the `argocd-cm` read behind it, which concern a user's GitOps tool, not an Ichor-installed one. (The GitOps, Git-tracking and PR-mode code is handled separately; see the GitOps section.)

## What remains of ADR 0099

ADR 0099 is superseded in part. These decisions stand: Cilium is the only CNI; it boots from the inline manifest and is then owned by Ichor as a Helm release run by an installer-style Job; the user's values layer lives on `TalosCluster.spec.cni.cilium.values`; `ichor-system` network policies (ADR 0100) are unchanged apart from losing the Kokumi egress rule and the `stack` policy. These do not: decisions about Kokumi, Argo CD, the in-cluster registry, Orders, bundles, `spec.stack`, `ichor-stack` Jobs and the stack's network policies.

## Consequences

- Existing clusters that already run the stack keep their Argo CD and Kokumi; Ichor just stops managing them. Removing them is the operator's job.
- A new `ichor init` finishes after the Cilium hand-over and installs nothing else.
- The additive-CRD rule holds: no field was removed from any CRD.

## GitOps

(To be written by the change that removes the GitOps, Git-tracking and PR-mode code.)
