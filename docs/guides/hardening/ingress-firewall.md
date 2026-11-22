---
sidebar_order: 1
---

# Ingress firewall

![How the firewall is applied and what happens when it is wrong. Setting spec.security.firewall makes the webhook check the controller and control plane addresses. A failed check is refused. If it passes, Talos blocks everything except the ichor-fw rules and your allow entries, without a reboot. On a cluster created before the check, a wrong control plane CIDR locks Ichor out; recovery is a temporary apid rule patched in with talosctl from a talosAPICIDRs host.](../../images/hardening-firewall.light.drawio.svg)

*The webhook refuses a firewall that would cut Ichor off; the way back is a temporary apid rule.*

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
`NetworkRuleConfig` per service, named `ichor-fw-<service>` (Talos v1.14).
Talos always accepts `lo`, `siderolink` and `kubespan`, and in block mode
rate-limited ICMP and pod-to-service traffic. Ichor opens:

| Rule | Port | Sources | On |
|---|---|---|---|
| apid | TCP 50000 | `controllerCIDRs`, `talosAPICIDRs`, control planes | all nodes |
| kubelet | TCP 10250 | control planes, the pod subnets | all nodes |
| Cilium tunnel | UDP 8472 (vxlan) or 6081 (`spec.cni.tunnel: geneve`) | `nodeCIDRs` | all nodes |
| Cilium health | TCP 4240 | `nodeCIDRs` | all nodes |
| trustd | TCP 50001 | `nodeCIDRs` | control planes |
| etcd | TCP 2379-2380 | control planes | control planes |
| Kubernetes API | TCP 6443 | `controllerCIDRs`, `apiServerCIDRs`, `nodeCIDRs`, pod and service subnets | control planes |
| Cilium WireGuard | UDP 51871 | `nodeCIDRs` | all nodes, if `wireGuard` is on |
| KubeSpan | UDP 51820 | anywhere | all nodes, if `kubeSpan` is set |
| `allow` entries | yours | yours | all nodes |

"Control planes" means `controlPlaneCIDRs`, or `nodeCIDRs` when unset. Pod and
service subnets come from `spec.network`, else Talos's defaults (10.244.0.0/16,
10.96.0.0/12). Everything else is blocked, so a host-network service (an
ingress controller on 80 and 443, a node exporter, a NodePort range, Hubble
relay peers on 4244) needs an `allow` entry.

## Not to lock Ichor out

> [!WARNING]
> `controllerCIDRs` is required. If the controller runs in a pod, list the pod
> network and its node's address: with Cilium it reaches apid on its own node
> from its pod IP, and on any other node from its node's address (masqueraded).

The admission webhook:

- **Refuses** a `controllerCIDRs` that lacks the address the controller connects to the endpoint from (read from the webhook pod's routing table; skipped while the endpoint does not resolve).
- **Refuses** `controlPlaneCIDRs` that miss an approved control plane (the controller reaches other nodes from a control plane's address). A machine whose endpoint is a hostname is not checked.
- **Warns** when `nodeCIDRs` miss a machine (it loses its Cilium and trustd rules; Ichor's path is intact) and when `talosAPICIDRs` is empty. List your workstation there: it is the only way back.
- Does not re-check machines approved after the firewall was enabled, so keep the CIDRs wide enough for them.

## Recovery after a lock-out

On a cluster created before the check, a `controlPlaneCIDRs` that excluded the
real control plane cuts Ichor off: the machines show `NodeReachable=False
NodeUnreachable`, and reverting the spec cannot reach them. From a workstation
in `talosAPICIDRs`, open apid with a temporary rule, then fix the spec:

```sh
talosctl -e NODE -n NODE patch machineconfig --patch @rule.yaml
# rule.yaml:
#   apiVersion: v1alpha1
#   kind: NetworkRuleConfig
#   name: recover
#   portSelector: {ports: [50000], protocol: tcp}
#   ingress: [{subnet: CONTROL_PLANE_IP/32}]
```

The machine is Ready again within tens of seconds, and the controller removes
the rule once the corrected spec applies. With no `talosAPICIDRs` entry the way
back is the node's console or a reset.

- A pod's traffic to another node's host ports arrives as its node's address, so node-CIDR rules cannot tell pods on one node apart.
- Ichor does not yet apply the firewall in Talos's `try` mode with an automatic revert.
