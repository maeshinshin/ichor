# LoadBalancer addresses on the LAN (Cilium Gateway API and L2 announcements)

A `LoadBalancer` Service gets an address from a pool and answers on the LAN, so
`ichor-ui` is reachable without a port-forward. Cilium's Gateway API
(`--gateway-api`) and L2 announcements (`--l2-announcements`) do it. Both need
Cilium's kube-proxy replacement, so `ichor init` takes `--kube-proxy replaced`
with them (Cilium 1.20.5 documentation). They are set by `ichor init` only; an
existing cluster is not changed by either flag.

![Five steps in a row: ichor init with kube-proxy replaced, Gateway API and L2 announcements; Cilium is set up and init applies seven Gateway API CRDs; you apply a Cilium IP pool and an L2 announcement policy (CiliumLoadBalancerIPPool, CiliumL2AnnouncementPolicy); a Service of type LoadBalancer gets an address from the pool; one node answers ARP for it on the LAN.](../images/lan-load-balancers-flow.light.drawio.svg)

*What init does, and the two objects you add.*

With kube-proxy `enabled` (init's default) `ichor init` refuses both flags and
names the requirement, before writing anything. Cilium reaches the API through
KubePrism on port 7445, which Talos enables by default (Talos v1.14 Cilium guide).

## What init turns on

- `--gateway-api` sets `gatewayAPI.enabled` and applies seven CRDs once the API
  answers: GatewayClass, Gateway, HTTPRoute, GRPCRoute, BackendTLSPolicy,
  ReferenceGrant, TLSRoute (Gateway API v1.6.1, standard channel), under the field
  manager `ichor-init-gateway-api`, so `ichor upgrade` never owns or removes them.
  A CRD another tool installed at a different release refuses the run. ListenerSet,
  TCPRoute and UDPRoute are not applied (Cilium 1.20 does not read them).
- `--l2-announcements` sets `l2announcements.enabled` and takes the pool from
  `--l2-pool`. Init does not create the pool or policy.

```sh
ichor init -f init.yaml          # with the keys below; or the flags
```

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

## Choose the pool

Init refuses a pool that overlaps, and names which: the endpoint address
(`--endpoint-address`), the first node (`--node-endpoint`, each `--address`), or a
discovery address or CIDR. The pool must be inside the first node's prefix, from
`--address`; on DHCP init cannot know it, so give `--address` with a prefix length.

Example: on a flat 192.168.0.0/16 with control planes on .201 to .203 and the VIP
on .210, `192.168.0.240/28` keeps clear of all of them. A /16 pool would not.

## Apply the pool and policy

Ichor has no typed kind for these. Apply them once Cilium is Ready (its operator
installs the CRDs), from the Cilium 1.20.5 `l2-announcements` and `lb-ipam` documentation:

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
  - ^enp1s0$                       # the first node's primary link
```

Then give a Service `type: LoadBalancer`. `kubectl get svc` shows the pool
address; `ip neigh` on another LAN machine shows which node answers.

## Gateway API on a cluster init did not set up

Ichor refuses `gatewayAPI.enabled` on a running cluster until all seven CRDs
exist (naming missing ones).

1. Apply them from Gateway API v1.6.3 (v1.6.1 is Cilium 1.20's floor):

   ```sh
   for crd in backendtlspolicies gatewayclasses gateways grpcroutes httproutes referencegrants tlsroutes; do
     kubectl apply --server-side -f "https://raw.githubusercontent.com/kubernetes-sigs/gateway-api/v1.6.3/config/crd/standard/gateway.networking.k8s.io_${crd}.yaml"
   done
   ```

2. `ichor set kube-proxy replaced`.
3. Set `gatewayAPI.enabled: true`. `ichor apply cni-values -f FILE` replaces your
   whole values layer, so start from `ichor get cni-values`.

Ichor refuses a set with any of the seven missing (naming them), and checks the
`gateways` CRD's release: v1.6.1 or newer, and it must record one. With `TLSRoute`
objects at `v1alpha2`, use the experimental `TLSRoute` CRD, or they become
unreadable. Ichor never deletes or upgrades CRDs it did not install.

## Limits

L2 announcements answer ARP from one node per address (the leader moves when its
lease lapses). That is failover, not load balancing, and works only on the pool's
LAN segment.
