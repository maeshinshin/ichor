---
sidebar_order: 1
---

# Ingress firewall

```yaml
spec:
  security:
    firewall:
      enabled: true
      controllerCIDRs: [192.168.10.0/24]   # where Ichor's controller connects from
      nodeCIDRs: [10.0.0.0/24]             # every machine of the cluster
      controlPlaneCIDRs: [10.0.0.10/32]    # optional: control planes only
      talosAPICIDRs: [172.16.0.0/12]       # optional: operators using talosctl
      apiServerCIDRs: [203.0.113.0/24]     # optional: users and load balancers
      kubeSpan: false
      allow:                               # optional: host-network services
        - {name: ingress, protocol: tcp, ports: ["80", "443"], cidrs: [0.0.0.0/0]}
```

Enabled sets `NetworkDefaultActionConfig` to `block` and renders one
`NetworkRuleConfig` per service, named `ichor-fw-<service>` (Talos v1.14,
"Ingress Firewall"). Talos always accepts `lo`, `siderolink` and `kubespan`, and
in block mode also rate-limited ICMP and traffic between the pod and service
subnets. What Ichor opens:

| Rule | Port | Sources | On |
|---|---|---|---|
| apid | TCP 50000 | `controllerCIDRs`, `talosAPICIDRs`, control planes | all nodes |
| kubelet | TCP 10250 | control planes, the pod subnets | all nodes |
| Cilium tunnel | UDP 8472 (vxlan, the default) or 6081 (`spec.cni.tunnel: geneve`) | `nodeCIDRs` | all nodes |
| Cilium health | TCP 4240 | `nodeCIDRs` | all nodes |
| trustd | TCP 50001 | `nodeCIDRs` | control planes |
| etcd | TCP 2379-2380 | control planes | control planes |
| Kubernetes API | TCP 6443 | `controllerCIDRs`, `apiServerCIDRs`, `nodeCIDRs`, the pod and service subnets | control planes |
| Cilium WireGuard | UDP 51871 | `nodeCIDRs` | all nodes, only if `wireGuard` is on |
| KubeSpan | UDP 51820 | anywhere | all nodes, only if `kubeSpan` is set |
| `allow` entries | yours | yours | all nodes |

Control planes means `controlPlaneCIDRs`, or `nodeCIDRs` when that is unset. The
pod and service subnets are `spec.network`, or Talos's defaults (10.244.0.0/16,
10.96.0.0/12). The control-plane metrics rule keeps working beside it.

Everything not listed is blocked, so a service you run with host networking
(an ingress controller on 80 and 443, a node exporter, a NodePort range, Hubble
relay peers on 4244) needs an `allow` entry.

## Not to lock Ichor out

> [!WARNING]
> `controllerCIDRs` is required, and the admission
> webhook refuses a firewall whose `controllerCIDRs` does not contain the address
> the controller connects to the cluster endpoint from (it asks the routing table
> of the webhook's own pod). If the controller runs in a pod, list the pod network
> and its node's address: with Cilium the controller reaches apid on its own node
> from its pod IP, but on any other node from its node's address (masqueraded). The webhook also refuses a firewall whose `controlPlaneCIDRs`
> miss the address of an approved control plane (the controller reaches the other
> nodes from a control plane's address, so Ichor itself would be cut off; a
> machine whose endpoint is a hostname is not checked). It only warns when
> `nodeCIDRs` miss a machine (that machine loses its Cilium and trustd rules, but
> Ichor's own path is intact) and when `talosAPICIDRs` is empty: list your own
> workstation there, it is the only way back if Ichor is cut off. If the endpoint
> does not resolve yet, the controller-address check is skipped; machines
> approved after the firewall was enabled are not re-checked, so keep the CIDRs
> wide enough for them.

## Recovery after a lock-out

A lock-out can come from a `controlPlaneCIDRs` that excluded the real control plane, on a cluster created before the check. The controller reports `NodeReachable=False NodeUnreachable` on the machines it lost, and
reverting the spec alone cannot help because it cannot reach them to deliver
it. From a workstation in `talosAPICIDRs`, open apid to the control plane with
a temporary rule, then fix the spec:

```sh
talosctl -e NODE -n NODE patch machineconfig --patch @rule.yaml
# rule.yaml:
#   apiVersion: v1alpha1
#   kind: NetworkRuleConfig
#   name: recover
#   portSelector: {ports: [50000], protocol: tcp}
#   ingress: [{subnet: CONTROL_PLANE_IP/32}]
```

The machine is Ready again within a few tens of seconds of the patch, and the
controller removes the temporary rule once the corrected spec applies. With no
`talosAPICIDRs` entry the way back is the node's console or a reset. A pod's traffic to
another node's host ports is also seen as its own node's address, so rules keyed
to node CIDRs cannot tell pods on the same node apart. Ichor does not yet apply the firewall in Talos's `try` mode with an
automatic revert (follow-up, needs hardware to verify). The firewall applies
without a reboot.
