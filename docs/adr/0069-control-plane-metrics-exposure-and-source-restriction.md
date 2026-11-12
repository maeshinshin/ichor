# 0069. Control-plane metrics are exposed by `spec.controlPlaneMetrics` and fenced by one source-restricting firewall rule

Status: Accepted · Date: 2026-10-26 · Design section: Monitoring and registry mirrors
Decided: `TalosCluster.spec.controlPlaneMetrics {enabled, allowedCIDRs}` (off by default; enabled needs at least one CIDR, `/0` rejected) renders, on control-plane nodes only and in the system layer, `bind-address: 0.0.0.0` for kube-controller-manager and kube-scheduler, `listen-metrics-urls: http://0.0.0.0:2381` for etcd, and one `NetworkRuleConfig` for TCP 2381, 10257 and 10259 that drops every source outside `allowedCIDRs`, with Talos's default firewall action left at accept; the firewall kinds are no-reboot in `pkg/fieldmeta`, the etcd argument stays reboot-required; the chart gets no new monitoring resource.

## Context

architecture.md ("Monitoring and registry mirrors") says Ichor can expose the control-plane metrics Talos
binds locally and restrict scraping to a CIDR with Talos's ingress firewall. ADR 0040 left it as a
`pkg/render` system-layer unit and listed what it needs. Everything below is from the Talos
v1.14.3 source (`siderolabs/talos@v1.14.3` and `pkg/machinery@v1.14.3`).

## 1. What moves the bind

- **kube-controller-manager.** `controllers/k8s/control_plane_final.go` builds its arguments from a
  default set containing `"bind-address": 127.0.0.1` and merges the document's `extraArgs` over it.
  `bind-address` is not in that controller's `MergeDenied` list, so `extraArgs` can replace it. The 1.14
  document is `KubeControllerManagerConfig` (`config/types/k8s/controller_manager.go`), field
  `extraArgs`; below the 1.14 contract it is `cluster.controllerManager.extraArgs`.
- **kube-scheduler.** Same file, default `"bind-address": 127.0.0.1`, `MergeDenied` only for the
  kubeconfig and `config` arguments. Document `KubeSchedulerConfig`, field `extraArgs`;
  legacy `cluster.scheduler.extraArgs`.
- **The kubelet probes keep working.** Both static pods probe `Host: localhost`
  (`k8stemplates/controller-manager.go`, `scheduler.go`), which a `0.0.0.0` bind still answers.
- **etcd.** `services/etcd.go` has a deny list of arguments Talos owns (`listen-peer-urls`,
  `listen-client-urls`, `listen-client-http-urls`, certificates, ...). `listen-metrics-urls` is not in
  it, and Talos sets nothing for it, so `cluster.etcd.extraArgs` can carry it. etcd has no
  standalone 1.14 document (`v1alpha1_etcdconfig.go`); `cluster.etcd.extraArgs` is the only form,
  so the legacy `Config` document is rendered for etcd under every contract. `listen-metrics-urls`
  is an upstream etcd flag and Talos passes it through untouched.
- **Ports.** Controller-manager 10257 and scheduler 10259 (HTTPS, bearer token with RBAC for
  `/metrics`), etcd 2381 (plain HTTP). Talos 1.14 already serves etcd's `/metrics` over mTLS on
  2383 (`constants.EtcdClientHTTPPort`) on all addresses; that needs a client certificate, so it is
  not the port a scraper uses and is not touched here.

## 2. The firewall: one rule, default action untouched

Documents (`config/types/network`): `NetworkRuleConfig` (`name`, `portSelector {ports, protocol}`,
`ingress [{subnet, except}]`) and `NetworkDefaultActionConfig` (`ingress: accept|block`).

`controllers/network/nftables_chain_config.go` shows the evaluation order:

1. chain policy is accept (drop only under `block`);
2. input from `lo`, `kubespan` and `siderolink` is accepted;
3. conntrack established/related is accepted, invalid is dropped;
4. under `block` only, ICMP, pod/service CIDR and host-DNS accepts are added;
5. each `NetworkRuleConfig` becomes one rule: under default **accept** its verdict is **drop** and the
   source match is **inverted** (drop TCP to these ports when the source is not in the subnets);
   under `block` it would be an accept.

`DefaultAction()` returns accept when no `NetworkDefaultActionConfig` exists (`config/network.go`,
"zero value is accept"). A rule with default accept therefore changes nothing for any port the rule
does not name: the Kubernetes API (6443), etcd client and peer (2379, 2380), kubelet (10250) and the
Talos API (50000) are never matched. Because lo is accepted before the rule, a node scraping itself
still works. Ichor therefore never renders `NetworkDefaultActionConfig`; switching it to `block`
would drop all of the above and is exactly what this design must not do. The render contains
no `NetworkDefaultActionConfig` and the rule names no port besides the three.

Consequences to know: the rule also drops sources that are not the scraper, such as other nodes, so
`allowedCIDRs` must cover wherever Prometheus runs (pod CIDR or node CIDR, depending on CNI and
masquerading). Talos also builds a prerouting chain once any rule exists; with default accept it only
adds drops for NEW connections to the same ports, so it widens nothing. The rule is rendered only
for control-plane nodes (workers run none of these services).

## 3. The spec

```yaml
spec.controlPlaneMetrics:      # optional pointer, unset = today's behaviour
  enabled: false               # default false
  allowedCIDRs: [10.20.0.0/16] # max 8, items <= 43 chars
```

CEL (the `spec.discovery` style): `self.all(c, isCIDR(c) && cidr(c).prefixLength() > 0)` on the list
(constant cost, bounded by MaxItems) and `!self.enabled || (has(self.allowedCIDRs) &&
self.allowedCIDRs.size() > 0)` on the object. Enabled without a CIDR is rejected in the CRD, and again
in render (an error, never an open port): with default accept an empty subnet list would be no
restriction at all. A `/0` is rejected for the same reason; IPv4 and IPv6 are both accepted since the
Talos rule accepts either. Disabled keeps `allowedCIDRs` so toggling does not lose them.

## 4. Render layer

System layer, applied after the registries and storage and before every patch, taking the cluster
(`Render` already receives it), so `SystemInputs` does not grow: nothing here is read from the
cluster at run time. The machine controller's input hash already includes the cluster generation.

Patch override: the typed patch kinds are `Config`, `SysctlConfig`, `KernelModuleConfig` and the
volume kinds, so there is no way to write `NetworkRuleConfig`, `KubeSchedulerConfig` or
`KubeControllerManagerConfig` in a patch. Under a 1.14 contract a legacy `cluster.scheduler` patch
is also rejected by Talos next to the standalone document (already true before this ADR). What a patch
can override is etcd's argument (`cluster.etcd.extraArgs`). For the rest the answer is the
switch in the spec, as with ADR 0066. Disabled renders byte-identically to unset.

## 5. Reboot necessity (`pkg/fieldmeta`)

- `NetworkRuleConfig` and `NetworkDefaultActionConfig`: **no reboot**. `NfTablesChainConfigController`
  takes the active `MachineConfig` as an input and rewrites its chains on each change; the apply RPC's
  no-reboot mode only persists the config (`v1alpha1_server.go`). Rules added.
- `KubeControllerManagerConfig` / `KubeSchedulerConfig` `extraArgs`: already no-reboot (static pods).
- `cluster.etcd.*`: **reboot-required stays.** `services/etcd.go` reads the extra arguments when the
  service starts and no controller restarts it on change, so there is no safe way to apply it live.
  Consequence: toggling `enabled` on a running cluster needs a reboot of each control-plane
  node, one at a time, through the existing reboot path.

## Chart

No ServiceMonitor for the control plane was added. A Prometheus scrape of node IPs needs either a
selector-less Service plus Endpoints naming the node addresses, which the chart cannot know, or a
Service in `kube-system`, outside the release namespace the chart owns. The
scrape targets are `https://<node>:10257/metrics`, `https://<node>:10259/metrics` (bearer token) and
`http://<node>:2381/metrics`, for the operator's own Prometheus (Ichor installs none).
