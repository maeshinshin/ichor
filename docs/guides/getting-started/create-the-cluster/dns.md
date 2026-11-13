---
sidebar_order: 3
---

# DNS name

Use this pattern when a name you control can point at the control planes, and
you can create records for it. A home network with a local DNS server is the
usual case. The endpoint is the name, and it must be a name, not an IP address.

Ichor treats this type the same as a VIP or a load balancer: it renders the API
address as `https://NAME:6443`, and nothing more. It does not create the DNS
records, and DNS does no health checking, so a record that points at a dead
control plane stays in place until you change it.

You create the records. The usual setup is one `A` (or `AAAA`) record per control
plane, all with the same name, so clients spread across the control planes. A
record can also point at a VIP or a load balancer instead. The
[Talos production notes](https://docs.siderolabs.com/talos/v1.14/getting-started/prodnotes)
describe the setup.

Init prints a warning for this type that a VIP is recommended. The reason is on
the [Virtual IP](vip.md) page: changing the endpoint later re-issues every
certificate in the cluster.

## Interactive

Run `ichor init` and pick `dns` from the endpoint type list. The endpoint
address prompt says `a hostname is expected here`; type the name. The node
address and the install disk are asked as described on the
[Virtual IP](vip.md#interactive) page.

## `init.yaml`

Save this as `init.yaml` and run `ichor init -f init.yaml`:

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo
  preset: standard
  endpoint:
    type: dns
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
  --endpoint-type dns --endpoint-address kube.example.lan \
  --install-disk /dev/sda
```

- `--cluster`, `--node-endpoint`, `--endpoint-address` and `--install-disk` are
  required.
- `--endpoint-address` is the name, and the records for it must exist before
  the cluster is reached through it.
- `--node-endpoint` is still the first node's own Talos API (`host:port`).
- If the first node needs a static address or a named link, the network flags
  (`--nic`, `--address`, `--gateway`, `--dns`, `--vlan`) are the same as on the
  [Virtual IP](vip.md#flags) page.
- Approve the other control planes as on that page, with `--role controlplane`.
