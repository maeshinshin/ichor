# LoadBalancer addresses on the LAN (Cilium Gateway API and L2 announcements)

A `LoadBalancer` Service gets an address from a pool and answers on the LAN, so
`ichor-ui` can be reached without a port-forward. Two Cilium features do it:
the Gateway API (`--gateway-api`) and L2 announcements (`--l2-announcements`).
Both need Cilium's kube-proxy replacement, so `ichor init` takes
`--kube-proxy replaced` with them. Both are set by `ichor init` only in this
release; an existing cluster is not changed by either flag.

## Requirements

Cilium's Gateway API and L2 announcements both require kube-proxy replacement
(`kubeProxyReplacement=true`, Cilium 1.20.4 documentation). With kube-proxy
`enabled` (init's default), `ichor init` refuses `--gateway-api` and
`--l2-announcements` and names the requirement. The checks run before anything
is written, so a refused run leaves no cluster behind.

Talos enables KubePrism on port 7445 by default (Talos v1.14 KubePrism
documentation), which is the address Cilium reaches the API through when
kube-proxy is replaced. The Talos v1.14 Cilium guide
(`docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium`) describes the
same values.

## What init turns on

- `--gateway-api` sets Cilium's `gatewayAPI.enabled`. Cilium's chart does not
  ship the Gateway API CRDs, so init applies seven of them through the API server
  once it answers: GatewayClass, Gateway, HTTPRoute, GRPCRoute, BackendTLSPolicy,
  ReferenceGrant and TLSRoute (Gateway API v1.6.1, standard channel). They are
  applied under their own field manager, `ichor-init-gateway-api`, so `ichor
  upgrade` never owns or removes them. They are not in any Talos configuration.
- Three standard-channel CRDs are not applied: ListenerSet, TCPRoute and UDPRoute.
  Cilium 1.20 does not read them; if you need one, apply its CRD yourself from the
  same Gateway API release.
- `--l2-announcements` sets Cilium's `l2announcements.enabled` and takes the pool
  from `--l2-pool`. Init does not create the pool or the policy; you do (below).

## What you write

Cilium's L2 announcements need two cluster resources. Ichor has no typed kind for
them, so write them yourself, after the cluster is up and Cilium is Ready (the
Cilium operator installs their CRDs). Both are from the Cilium 1.20.4
documentation (`Documentation/network/l2-announcements.rst` and `lb-ipam.rst`).

```yaml
apiVersion: "cilium.io/v2"
kind: CiliumLoadBalancerIPPool
metadata:
  name: lan-pool
spec:
  blocks:
  - cidr: "192.168.0.240/28"       # the same CIDR as --l2-pool
---
apiVersion: "cilium.io/v2alpha1"
kind: CiliumL2AnnouncementPolicy
metadata:
  name: lan-l2
spec:
  loadBalancerIPs: true
  interfaces:
  - ^enp1s0$                       # the first node's primary link, so announcements stay on the LAN link
```

Apply them with `kubectl apply -f`, then give a Service `type: LoadBalancer`.
`kubectl get svc` shows the address from the pool; `ip neigh` on another machine
on the LAN shows which node answers for it.

## Choosing the pool

Init checks the pool and refuses it when it overlaps an address that is already
owned, naming which one:

- the endpoint address (`--endpoint-address`, the VIP when the endpoint is a VIP),
- the first node's address (`--node-endpoint`, and each `--address`),
- a discovery address or CIDR (`--discover-address`, `--discover-cidr`).

The pool must also be inside the first node's prefix, which init takes from
`--address`. With DHCP on the first node init cannot know the prefix, so give
`--address` with the prefix length to use `--l2-announcements`.

The maintainer's network is a flat 192.168.0.0/16 with control planes on .201,
.202 and .203 and the VIP on .210. A pool such as 192.168.0.240/28 keeps clear of
all four, and of anything else in the range you have reserved for nodes that
join later. A /16 pool would cover every one of them, which init refuses.

## Commands

```sh
ichor init -f init.yaml          # with the keys below; or the flags
```

The file keys, beside `cluster` and `firstNode`:

```yaml
cluster:
  network:
    cni:
      kubeProxy: replaced         # --kube-proxy: enabled (default) or replaced
      gatewayAPI: true            # --gateway-api
      l2Announcements: true       # --l2-announcements
      l2Pool: 192.168.0.240/28    # --l2-pool
firstNode:
  addresses:
    - 192.168.0.201/16            # --address: needed to check the pool
```

## Limits

- L2 announcements answer ARP from one node at a time per address; the leader
  moves when its lease lapses. This is not load balancing, and it works only on
  the LAN segment the pool belongs to.
- Existing clusters: not in this release. The flags exist only on `ichor init`, and an
  existing cluster keeps its CNI settings.
