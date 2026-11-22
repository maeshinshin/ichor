---
sidebar_group: "Cluster lifecycle"
sidebar_order: 14
sidebar_label: "Architecture: CNI"
---

# CNI

![Two presets: cilium, managed by Ichor, and unmanaged, for adoption only. Below, the safe order for switching kube-proxy, and the network policy gate.](../images/cni.light.drawio.svg)

*Cilium is the only CNI Ichor installs; `unmanaged` leaves an adopted cluster's CNI alone.*

## Preset `cilium`

- `spec.cni.preset` is `cilium` and immutable. `spec.cni.tunnel` (`ichor init --cni-tunnel`) is `vxlan` or `geneve`, immutable once set. Talos's own CNI is removed (the `KubeFlannelCNIConfig` document, or `cni.name: none` before v1.14). `spec.network.{podSubnets,serviceSubnets}` are fixed once set.
- A cluster stored with another preset is refused, not guessed at: the controller sets `CNIUnsupported` and changes nothing.
- A cluster adopted with no CNI becomes a managed `cilium` cluster: adopt installs the pinned Cilium with `spec.cni.tunnel` from `--tunnel` (or the default) and `spec.cni.kubeProxy` read from Talos (kube-proxy enabled gives `enabled`, disabled gives `replaced`); `--cilium-values FILE` sets the initial `spec.cni.cilium.values`. `ichor init` keeps its inline-manifest bootstrap.
- Install, ownership and editing are in [Cilium](cilium.md). Hubble certificates come from a CronJob, not Helm, so a re-render does not change them.

## Preset `unmanaged`

For adoption only: `ichor init` has no flag for it and the webhook refuses it on create without the adoption annotation. `ichor adopt` refuses a non-Cilium CNI unless `--cni-unmanaged` is given, and a cluster with two CNIs even then. The preset is immutable; moving between managed and unmanaged means a new cluster.

- `spec.cni.cilium` and `spec.cni.tunnel` are refused. `spec.cni.unmanaged.talosCNI` is `default` (render says nothing about the CNI) or `none` (render keeps `cluster.network.cni.name: none`). `spec.cni.kubeProxy` only records what the node has.
- The hand-over, the `ichor-cni` Job, the Cilium commands (`ichor get cni-values`, `ichor upgrade cni`, ...), `cni` operations and the kube-proxy switch are refused or skipped. A Kubernetes upgrade is never blocked on a hand-over; a CA rotation restarts no CNI workload.
- `status.cni.detected` (`cilium`, `flannel`, `calico`, `other`, `none`) is for display, refreshed on a slow requeue.

## Network policy gate

Ichor's `CiliumNetworkPolicy` objects are applied only when the detected CNI is Cilium and the CRD exists, a hands-off Cilium included. Otherwise `ichor-system` is not isolated by Ichor and `NetworkPoliciesApplied` says so.

## kube-proxy

`spec.cni.kubeProxy` is `enabled` or `replaced`. Replacing it splits the setting across Talos (`KubeProxyConfig`) and Cilium's `kubeProxyReplacement` (the Helm release); both are Ichor's, and the consistency check reads Cilium's live config and reports a mismatch as a health failure. `ichor set kube-proxy` is refused unless annotated (the server sets the annotation for that one write).

Turning the old side off is a deletion Ichor must perform. In Talos v1.14.3 `ManifestController` renders the kube-proxy objects only while proxying is enabled, but `ManifestApplyController` has no delete path, only a server-side `Apply`: flipping the setting leaves the DaemonSet, ServiceAccount, ClusterRoleBinding and ConfigMap running, so an operator would get two proxies. The same controller stamps every object with an inventory annotation and refuses to apply over a *foreign* one.

A `vip` endpoint address that no approved control plane with static addresses could hold (outside every subnet) is refused at admission and by the renderer.
