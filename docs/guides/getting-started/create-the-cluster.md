---
sidebar_order: 2
---

# 2. Create the cluster

`ichor init` makes the node the first control plane of a new cluster. Before
you run it, choose the endpoint: the one address that kubectl, the nodes and
Ichor's controller use to reach the Kubernetes API, and what stands behind
that address. The endpoint type is that choice, and it is the one setting that
is hard to change later. Each pattern below is one endpoint type.

| Pattern | Endpoint type | Use it when |
| --- | --- | --- |
| [Virtual IP](create-the-cluster/vip.md) | `vip` | the default. The control planes share one layer-2 segment with no router between them. |
| [Load balancer or DNS name](create-the-cluster/load-balancer.md) | `loadBalancer` | there is no shared layer-2 segment, or you are in a cloud, or you have a DNS name and no load balancer. You run what answers on that address. |
| [Node address](create-the-cluster/node.md) | `node` | a lab or a single control plane. The node's own address is the endpoint. |

Use the virtual IP unless your network rules it out. Only the VIP changes what Ichor renders. For a load balancer or a DNS name, Ichor records the address you give it and creates nothing behind it: running what answers on that address is yours. Each
pattern page shows the same three ways to create the cluster: at a prompt, from
an `init.yaml` file, and with flags. The settings that every pattern shares
(the topology preset, the subnets, the secrets bundle's passphrase, the
kubeconfig and sign-in) are on [Settings every pattern shares](create-the-cluster/settings-every-pattern.md).

Changing the endpoint type or address after init re-issues every certificate in
the cluster. That is why init prints a warning for any type other than `vip`.
