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
| [Load balancer](create-the-cluster/load-balancer.md) | `loadBalancer` | there is no shared layer-2 segment, or you are in a cloud. You run a TCP load balancer in front of the control planes. |
| [DNS name](create-the-cluster/dns.md) | `dns` | you can create DNS records for the control planes, for example on a home network with a local DNS server. |
| [Node address](create-the-cluster/node.md) | `node` | a lab or a single control plane. The node's own address is the endpoint. |

Use the virtual IP unless your network rules it out. Ichor treats the three address-based types the same way and creates nothing behind them: with a load balancer or a DNS name, running what answers on that name is yours. Each
pattern page shows the same three ways to create the cluster: at a prompt, from
an `init.yaml` file, and with flags. The settings that every pattern shares
(the topology preset, the subnets, the secrets bundle's passphrase, the
kubeconfig and sign-in) are on [Settings every pattern shares](create-the-cluster/settings-every-pattern.md).

Changing the endpoint type or address after init re-issues every certificate in
the cluster. That is why init prints a warning for any type other than `vip`.
