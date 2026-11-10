# 0144. Adopt keeps a cluster's missing control-plane node labels missing

Status: Accepted · Date: 2026-11-11 · Design section: Adopt

Decided: `TalosCluster.spec.controlPlane.omitDefaultNodeLabels` (bool, default false) makes the render leave out the labels Talos's generator adds to control planes. `ichor adopt` sets it when none of the control planes it reads carries `node.kubernetes.io/exclude-from-external-load-balancers`; `ichor init` never sets it, so a cluster Ichor creates keeps Talos's defaults.

## Context

Talos's generator adds `node.kubernetes.io/exclude-from-external-load-balancers` to a control plane in `machine.nodeLabels` below the v1.14 contract (`generate/init.go`), and that label plus `node-role.kubernetes.io/control-plane` in `KubeNodeConfig.labels` from v1.14 (`generate/kubernetes.go`). Ichor renders through that generator, so the baseline carried labels that a node configured without them (for example by an older Talos or a hand-written config) does not. The difference is a label absence, which a patch cannot express, so adopt blocked.

## Decisions

1. **Adopt represents the node, it does not change it.** Adding the exclude label removes the node from `Service type=LoadBalancer` membership. On a cluster of control planes only, that can take every node out of rotation. This is the rule of ADR 0138 to 0141: capture what the node has.
2. **A cluster flag, not a patch.** A strategic-merge patch can add a label but not remove one the generator emitted, and the generated documents are the lowest layer. The render edits the generator's output (`pkg/render/defaultlabels.go`): the exclude label from `machine.nodeLabels`, or the role and exclude labels from `KubeNodeConfig.labels`. Labels the user sets (`spec.nodeLabels`, patches) are applied afterwards and stay.
3. **All control planes or none.** The flag is set only when no read control plane has the exclude label. A cluster that mixes the two keeps blocking the nodes that lack it; per-machine omission is more than this needs.
4. **Append-only.** The field is new and never removed.

## Risks

A user who later wants the label has to unset the field and accept the LB change. A control plane that has the exclude label but lacks only the role label is not recognised.
