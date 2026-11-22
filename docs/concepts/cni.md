---
title: "CNI and Cilium"
description: "Ichor owns Cilium as the only CNI, as a Helm release: how it boots, who may change its values, which keys Ichor pins, and how it upgrades."
sidebar_group: "What Ichor manages"
sidebar_order: 30
---

# CNI and Cilium

Cilium is the only CNI Ichor supports, and the only thing it installs on your
cluster besides itself, as a Helm release. No storage, monitoring or GitOps.
Design: [architecture](../design/architecture.md), "Cilium".

## How Cilium gets there

A new cluster boots with Cilium rendered from the chart embedded in the `ichor`
binary (`ichor version` prints its version) and ends with it as a Helm release.
`ichor init` waits for the hand-over. The tunnel protocol is chosen at install
with `--cni-tunnel vxlan|geneve`.

![The hand-over of Cilium on a new cluster, in three steps. First Cilium is an inline manifest in the Talos machine config. When the Cilium DaemonSet is ready, the ichor-cni Job adopts its objects as the Helm release cilium. Then the controller removes the inline entry, because Talos re-applies bootstrap manifests on a Kubernetes upgrade and a stale entry would roll Cilium back.](../images/cni-handover.light.drawio.svg)

*Cilium starts as an inline manifest and is handed over to a Helm release.*

The Job runs Helm because it needs rights close to cluster-admin, which the
controller is not given.

`ichor adopt` can take over a Cilium someone else installed, after checks on its
minor version, IPAM mode (`kubernetes` only) and health. Migrating from another
CNI is not supported.

## Values

Three layers, a later one wins. Yours is `spec.cni.cilium.values` on the
[`TalosCluster`](../reference/crds/taloscluster.md).

![The three layers of Cilium values. Ichor's defaults come first. Your layer, spec.cni.cilium.values, which only ichor-admin can write, replaces the defaults. The keys Ichor owns are applied last and always win: the settings Talos requires, the KubePrism address (set only when kube-proxy is replaced), kubeProxyReplacement, tunnelProtocol and the five secretsNamespace name keys. Setting one of them is refused. The result is the values of the Helm release cilium.](../images/cni-values.light.drawio.svg)

*Ichor's defaults, then your layer, then the keys Ichor owns.*

Edit your layer on the UI's Cilium page or with
`ichor get|apply|edit|diff cni-values`. One function validates it in the
TalosCluster webhook, the UI backend, the CLI and the controller (before every
Cilium Job). A refusal starts no Job and shows as `CNIValuesApplied=False`. It
checks the chart schema, unknown keys, Ichor-owned keys and a size cap, and
refuses values that cannot work (for example `gatewayAPI.enabled` without
kube-proxy replacement).

| Rule | Why |
|---|---|
| Writing values is `update` on `talosclusters/cilium-values`, `ichor-admin` only. The webhook checks it, so the UI, CLI and `kubectl` all obey | A chart value can describe a privileged host-path pod, and the Job that deploys it is nearly cluster-admin. An operator can still edit the rest, start a Cilium upgrade and see the layer read-only |
| `ingressController`, `gatewayAPI`, `envoyConfig`, `bgpControlPlane` and `tls` `.secretsNamespace.name` are always `cilium-secrets`. Setting one, even to that value, is refused | A value naming `ichor-system` would render a Role letting Cilium's service accounts read Ichor's Secrets |
| `k8sServiceHost` and `k8sServicePort` are Ichor-owned (`localhost`, `7445`, the KubePrism address) only when kube-proxy is replaced. Otherwise you may set both; switching to `replaced` is refused until you remove them | Only then does Ichor set them |

## Upgrades and kube-proxy

- Cilium moves only to the chart embedded in the running Ichor, and only when
  you start a `cni` [operation](operations.md) (`ichor upgrade cni`, or the UI).
  One pinned combination per release; a recorded patch newer than the embedded
  chart is never stepped back. Helm rolls back a failed upgrade.
- `ichor set kube-proxy` switches between Talos's kube-proxy and Cilium's
  replacement, as an operation.
- Once Cilium is ready, Ichor's own namespace is locked down with
  CiliumNetworkPolicies.

Related: [`ichor upgrade cni`](../reference/cli/ichor_upgrade_cni.md),
[`ichor get cni-values`](../reference/cli/ichor_get_cni-values.md).
