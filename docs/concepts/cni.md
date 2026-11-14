---
title: "CNI and Cilium"
description: "Ichor owns Cilium as the only CNI, as a Helm release: how it boots, who may change its values, which keys Ichor pins, and how it upgrades."
sidebar_group: "What Ichor manages"
sidebar_order: 30
---

# CNI and Cilium

Cilium is the only CNI Ichor supports, and it is the one thing Ichor installs
on your cluster besides itself. Ichor keeps it as a Helm release. It does not
install or operate storage, monitoring or GitOps tools.
The full design is in [the architecture document](../design/architecture.md),
section "Cilium".

## How Cilium gets there

A new cluster boots with Cilium rendered from the chart embedded in the `ichor`
binary (`ichor version` prints the Cilium chart version it embeds) and ends with
it as a Helm release. `ichor init` waits for the hand-over.

![The hand-over of Cilium on a new cluster, in three steps. First Cilium is an inline manifest in the Talos machine config. When the Cilium DaemonSet is ready, the ichor-cni Job adopts its objects as the Helm release cilium. Then the controller removes the inline entry, because Talos re-applies bootstrap manifests on a Kubernetes upgrade and a stale entry would roll Cilium back.](../images/cni-handover.light.drawio.svg)

*Cilium starts as an inline manifest and is handed over to a Helm release.*

The Job runs Helm because the controller does not: the Job needs rights close
to cluster-admin, and the controller is not given them. The tunnel protocol is
chosen at install with `--cni-tunnel vxlan|geneve`.

A Cilium that someone else installed can be taken over by `ichor adopt`, after
checks on its minor version, its IPAM mode (`kubernetes` only) and its health.
Ichor does not migrate a cluster from another CNI to Cilium.

## Values

The values that Cilium runs with are built in three layers, and a later layer
wins. Your layer is `spec.cni.cilium.values` on the
[`TalosCluster`](../reference/crds/taloscluster.md).

![The three layers of Cilium values. Ichor's defaults come first. Your layer, spec.cni.cilium.values, which only ichor-admin can write, replaces the defaults. The keys Ichor owns are applied last and always win: the settings Talos requires, the KubePrism address (set only when kube-proxy is replaced), kubeProxyReplacement, tunnelProtocol and the five secretsNamespace name keys. Setting one of them is refused. The result is the values of the Helm release cilium.](../images/cni-values.light.drawio.svg)

*Ichor's defaults, then your layer, then the keys Ichor owns.*

You edit your layer on the Cilium page of the UI or with
`ichor get cni-values|set|edit|diff`. One function validates it, and it runs in
the TalosCluster webhook, the UI backend, the CLI and the controller (before
every Cilium Job). A refusal starts no Job and shows as
`CNIValuesApplied=False`. It checks the chart's schema, unknown keys, Ichor-owned
keys and a size cap, and refuses values that cannot work (for example
`gatewayAPI.enabled` without kube-proxy replacement).

### The values are admin-only

A Cilium chart value can describe a privileged pod with a host path on every
node, and the Job that deploys it is nearly cluster-admin. Writing the values is
therefore a right of its own: `update` on `talosclusters/cilium-values`, held
by `ichor-admin` only. The webhook checks it for the requesting user, so the
UI, the CLI and `kubectl` all obey it. An operator can still edit the rest of a
cluster, start a Cilium upgrade, and see the layer read-only.

### Pinned keys

The five keys `ingressController.secretsNamespace.name`,
`gatewayAPI.secretsNamespace.name`, `envoyConfig.secretsNamespace.name`,
`bgpControlPlane.secretsNamespace.name` and `tls.secretsNamespace.name` are
Ichor-owned and always `cilium-secrets`. Setting any of them, even to that same
value, is refused. Without the pin, a value naming `ichor-system` would render
a Role that lets Cilium's service accounts read Ichor's Secrets.

## Upgrades and kube-proxy

Cilium moves only to the chart embedded in the running Ichor, and only when
you start a `cni` [operation](operations.md) (`ichor upgrade cni`, or the UI).
There is one pinned combination per release, and a recorded patch newer than the
embedded chart is never stepped back. A
daily CI run opens a pull request when Cilium publishes a new patch of the
pinned minor.
Helm rolls the release back if the upgrade fails.

`ichor set kube-proxy` switches between Talos's kube-proxy and Cilium's
replacement as an operation.

Ichor's own namespace is locked down with CiliumNetworkPolicies once Cilium is
ready.

Related: [`ichor upgrade cni`](../reference/cli/ichor_upgrade_cni.md),
[`ichor get cni-values`](../reference/cli/ichor_get_cni-values.md).
