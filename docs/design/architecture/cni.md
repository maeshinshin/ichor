---
sidebar_group: "Cluster lifecycle"
sidebar_order: 14
---

# CNI

Cilium is the only CNI; `spec.cni.preset` is `cilium` and
immutable. `spec.cni.tunnel` (`ichor init --cni-tunnel`) picks `vxlan` or
`geneve` and is immutable once set. Talos's own CNI is removed (the
`KubeFlannelCNIConfig` document, or `cni.name: none` before v1.14). Pod and
service subnets are `spec.network.{podSubnets,serviceSubnets}`, fixed once set. A cluster stored with another preset is refused, not guessed at:
the controller sets `CNIUnsupported` and changes nothing.

A cluster adopted with no CNI is a managed `cilium` cluster: adopt
installs the pinned Cilium with `spec.cni.tunnel` from `--tunnel` (or the
default) and `spec.cni.kubeProxy` read from Talos (kube-proxy enabled gives
`enabled`, disabled gives `replaced`); `--cilium-values FILE` sets the initial
`spec.cni.cilium.values`. `ichor init` keeps its inline-manifest bootstrap.

The one exception is `spec.cni.preset: unmanaged`, which exists for
adoption only. `ichor init` has no flag for it and the webhook refuses it on
create unless the object carries the adoption annotation. The adopted cluster
keeps whatever CNI it runs (flannel, Calico, another, or none); Ichor does not
install, upgrade or configure it, and never migrates one. `ichor adopt` refuses
a non-Cilium CNI unless `--cni-unmanaged` is given, and refuses a cluster with
two CNIs even then. In this mode:

- `spec.cni.cilium` and `spec.cni.tunnel` are refused. `spec.cni.unmanaged.talosCNI`
  is `default` (render says nothing about the CNI) or `none` (render keeps
  `cluster.network.cni.name: none`). `spec.cni.kubeProxy` only records what
  the node has.
- The hand-over, the `ichor-cni` Job, the Cilium commands (`ichor get cni-values`, `ichor upgrade cni` and the rest), `cni` operations
  and the kube-proxy switch are refused or skipped. A Kubernetes upgrade is
  never blocked on a hand-over. A CA rotation restarts no CNI workload.
- The preset is immutable. Moving between managed and unmanaged means a new
  cluster.
- `status.cni.detected` (`cilium`, `flannel`, `calico`, `other`, `none`) is
  read for display and refreshed on a slow requeue.

Network policy gate. Ichor's `CiliumNetworkPolicy` objects are
applied only when the detected CNI is Cilium and the CRD exists. Otherwise
`ichor-system` is not isolated by Ichor and the condition
`NetworkPoliciesApplied` says so. A hands-off Cilium also gets them.

How Cilium is installed, owned and edited is described in [Cilium](cilium.md). The Talos-recommended values are built in; Hubble certificates
come from a CronJob, not Helm, so a re-render does not change them.

`spec.cni.kubeProxy` is `enabled` or `replaced`. Replacing kube-proxy splits
the setting across two owners, Talos (`KubeProxyConfig`, Ichor) and Cilium's
`kubeProxyReplacement` (the Helm release, also Ichor), so the consistency check
reads Cilium's live config and reports mismatches as health failures.
Switching modes (`ichor set kube-proxy`) is refused unless annotated (the
server sets the annotation for that one write), and then runs in the only safe
order: upgrade the Cilium release through the `ichor-cni` Job, verify on all
nodes, then turn the Talos side off.

Turning the old side off is a deletion Ichor has to perform, not a
consequence of the config change. In Talos v1.14.3,
`ManifestController` renders the kube-proxy objects only while proxying is
enabled, but `ManifestApplyController` has no delete path, only a server-side
`Apply`. Flipping the setting stops Talos managing kube-proxy and leaves the
DaemonSet, ServiceAccount, ClusterRoleBinding and ConfigMap running, so an
operator would get two proxies. The same controller stamps every object with an
inventory annotation and refuses to apply over a *foreign* one.
