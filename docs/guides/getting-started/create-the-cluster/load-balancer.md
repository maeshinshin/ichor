---
sidebar_order: 2
---

# Load balancer

Use this pattern when a load balancer you already run sits in front of the
control planes and gives the cluster its address. The endpoint is that load
balancer's hostname. Ichor does not set up the load balancer or run it; that
part is yours.

Init prints a warning for this type that a VIP is recommended. The reason is on
the [Virtual IP](vip.md) page: changing the endpoint later re-issues every
certificate in the cluster.

## Interactive

Run `ichor init` and pick `loadBalancer` from the endpoint type list. The
endpoint address prompt says `a hostname is expected here`; type the load
balancer's name. The node address and the install disk are asked as described
on the [Virtual IP](vip.md#interactive) page.

## `init.yaml`

Save this as `init.yaml` and run `ichor init -f init.yaml`:

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo
  preset: standard
  endpoint:
    type: loadBalancer
    address: kube.example.lan
firstNode:
  endpoint: 192.168.1.50:50000
  installDisk: /dev/sda
```

The file is read as described on [Settings every pattern shares](settings-every-pattern.md#the-inityaml-file).

## Flags

```sh
ichor init --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type loadBalancer --endpoint-address kube.example.lan \
  --install-disk /dev/sda
```

- `--cluster`, `--node-endpoint`, `--endpoint-address` and `--install-disk` are
  required.
- `--endpoint-address` is a hostname for this type, not an IP address.
- `--node-endpoint` is still the first node's own Talos API (`host:port`); the
  load balancer is the endpoint the cluster is reached through, not the way init
  reaches the node.
- If the first node needs a static address or a named link, the network flags
  (`--nic`, `--address`, `--gateway`, `--dns`, `--vlan`) are the same as on the
  [Virtual IP](vip.md#flags) page.
- Approve the other control planes as on that page, with `--role controlplane`.
