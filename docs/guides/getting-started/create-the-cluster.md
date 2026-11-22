---
sidebar_order: 2
---

# 2. Create the cluster

`ichor init` makes the node the first control plane of a new cluster. First
choose the endpoint: the one address kubectl, the nodes and Ichor's controller use
for the Kubernetes API. It is the one setting that is hard to change later.

| Pattern | Endpoint type | Use it when |
| --- | --- | --- |
| [Virtual IP](create-the-cluster/vip.md) | `vip` | the default. The control planes share one layer-2 segment with no router between them. |
| [Single node endpoint](create-the-cluster/node.md) | `node` | a lab or a single control plane. The node's own address is the endpoint. |
| [Load balancer endpoint](create-the-cluster/node.md#load-balancer-endpoint) | `node` | there is no shared layer-2 segment, or you are in a cloud, or you have a DNS name and no load balancer. You run what answers on that address. |

![Three endpoint types. The virtual IP is one address on the layer-2 segment that moves between control planes, held by one at a time. The single node endpoint is the node's own address, answered by that control plane. The load balancer endpoint is an address you give Ichor, with whatever answers there run by you.](../../images/create-the-cluster-endpoints.light.drawio.svg)

*What stands behind the one address that each endpoint type gives the cluster.*

Use the virtual IP unless your network rules it out. Only the VIP changes what
Ichor renders; for a load balancer or DNS name Ichor records the address and
creates nothing behind it. Each pattern's page shows the prompt, an `init.yaml`
and the flags (the load balancer is a section of the single node page). Shared
settings (preset, subnets, bundle passphrase, kubeconfig, sign-in) are on
[Settings every pattern shares](create-the-cluster/settings-every-pattern.md).

> [!WARNING]
> Changing the endpoint type or address after init re-issues every certificate
> in the cluster, so init warns about any type but `vip`.

Running `ichor init` again for a cluster whose secrets bundle Secret exists is
refused before any node is written. The message names both routes:
`ichor create credentials CLUSTER` to reach it, or
`ichor adopt --cluster CLUSTER --secrets-bundle FILE` to bring it under management.
