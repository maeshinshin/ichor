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
| [Virtual IP](create-the-cluster/vip.md) | `vip` | the default. You have a free address on the nodes' network, and the control planes share it. |
| [Load balancer](create-the-cluster/load-balancer.md) | `loadBalancer` | a load balancer you run already sits in front of the control planes. |
| [DNS name](create-the-cluster/dns.md) | `dns` | a name you control already points at the control planes. |
| [Node address](create-the-cluster/node.md) | `node` | the cluster is one node, and that node's own address is the endpoint. |

Use the virtual IP unless one of the other rows fits your network better. Each
pattern page shows the same three ways to create the cluster: at a prompt, from
an `init.yaml` file, and with flags. The settings that every pattern shares
(the topology preset, the subnets, the secrets bundle's passphrase, the
kubeconfig and sign-in) are on [Settings every pattern shares](create-the-cluster/settings-every-pattern.md).

Changing the endpoint type or address after init re-issues every certificate in
the cluster. That is why init prints a warning for any type other than `vip`.
