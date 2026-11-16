---
sidebar_order: 2
---

# Cilium: WireGuard and Helm values

## WireGuard encryption

```yaml
spec:
  security:
    wireGuard: {enabled: true, nodeEncryption: false}
```

Sets `encryption.enabled=true`, `encryption.type=wireguard` and
`encryption.nodeEncryption` in the managed Cilium Helm values (Cilium 1.18,
"Transparent Encryption with WireGuard"). The typed option wins over a
conflicting `encryption.*` in `spec.cni.cilium.values`. The Cilium Helm Job runs
again (about a minute) and, because the agents read encryption only when they
start, Ichor sets the chart's `rollOutCiliumPods` so the DaemonSet rolls (one
node at a time) when `cilium-config` changes. `CNIValuesApplied` turns True once
the Job is done and the DaemonSet has rolled out, i.e. the agents run with the
new values; you can confirm with `cilium-dbg encrypt status`. Nodes do not
reboot. Pod traffic
between nodes is encrypted once both ends have switched, with short resets of
connections as each agent flips. The tunnel MTU shrinks. With the firewall on,
UDP 51871 is opened between nodes. Strict mode (`encryption.strictMode`) is not
modelled; set it through `spec.cni.cilium.values` only after every agent reports
encryption.

## Cilium Helm values

`spec.cni.cilium.values` is the Helm values of the Cilium chart, and the Helm Job that applies them can create privileged, host-network pods with host paths on every node (the chart takes `extraContainers`, `extraHostPathMounts`, image overrides and the like).

> [!WARNING]
> A user who could write the values could therefore become root on the nodes.

So changing them, from the UI, `ichor apply cni-values|edit`, `ichor adopt --cilium-values` or `kubectl`, needs the verb `update` on `talosclusters/cilium-values`. Only the built-in `ichor-admin` role holds it; `ichor-operator` can still edit everything else on the cluster and start a Cilium upgrade, but its writes are refused when they change the values (an edit that leaves them as they are is not). The webhook checks the right as the requesting user, so a hand-written CR obeys it too.

The namespace of the Roles that let Cilium read Secrets is pinned as well: the five
`*.secretsNamespace.name` values (`ingressController`, `gatewayAPI`, `envoyConfig`,
`bgpControlPlane` and `tls`) are set by Ichor to `cilium-secrets` and refused as
"set by Ichor" if you set them. If you use BGP with authentication Secrets, put them in
`cilium-secrets` (the chart's own default for BGP is `kube-system`).
