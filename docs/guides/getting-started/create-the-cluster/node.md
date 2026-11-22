---
sidebar_order: 2
---

# Single node endpoint

The `node` endpoint is an address that answers on port 6443: the first node's
own, for a lab or a single machine, or a load balancer's or DNS name's in front
of the control planes (the [last section](#load-balancer-endpoint)). One control
plane's API server then serves every client, so an upgrade that replaces it
makes the Kubernetes API unreachable for everyone (the upgrade warns). With more
than one control plane prefer the [Virtual IP](vip.md).

![The endpoint address is what kubectl and the kubeconfig use. With the node type it is the control plane's own IP, with no failover; with a load balancer it is a TCP forwarder on port 6443 that does not terminate TLS; with a DNS name it is one A record per control plane, with no health check.](../../../images/create-the-cluster-node.light.drawio.svg)

*What can stand behind a `node` endpoint address. Ichor creates none of it.*

## Interactive

Run `ichor init --preset single`. On a terminal init asks, in this order, for
each setting not given:

- **Cluster name**
- **Endpoint type:** pick `node`.
- **Endpoint address:** the node's own IP address (or hostname).
- **First node:** a network such as `192.168.1.0/24` to scan on port 50000 for
  nodes in maintenance mode, or the node's address, which skips the scan.
- **Node address:** asked even when everything else was given, unless
  `--address` or `firstNode.addresses` is set. It starts as the node's address
  without a prefix length, and Enter on that is refused: give `192.168.1.50/24`
  for a static address, or clear the field to keep DHCP.
- **Install disk:** from the disks the node reports.

Text fields check as you type; Esc or Ctrl-C cancels and writes nothing. With no
terminal there is no prompt: a missing setting fails with the "required flag"
error, so use flags.

## `init.yaml`

Save as `init.yaml` and run `ichor init -f init.yaml` (read as in
[Settings every pattern shares](settings-every-pattern.md#the-inityaml-file);
without `firstNode.addresses` a terminal still asks for **Node address**):

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo
  preset: single
  endpoint:
    type: node
    address: 192.168.1.50
firstNode:
  endpoint: 192.168.1.50:50000
  installDisk: /dev/sda
```

## Flags

```sh
ichor init \
  --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address 192.168.1.50 \
  --install-disk /dev/sda \
  --preset single
```

- `--cluster`, `--node-endpoint` (the node's Talos API, `host:port`),
  `--endpoint-address` and `--install-disk` are required. The disk can also be
  `wwid:ID`, `serial:SERIAL` or a `/dev/disk/by-id/` path.
- With no terminal, the first node gets no network configuration unless you pass
  `--nic`, `--address`, `--gateway`, `--dns` or `--vlan`; Talos's default (DHCP on
  every physical link) applies.
- `--preset single` is one schedulable control plane: nothing to approve, so
  skip that step.
- Init prints the first admin's password once ([Sign-in](settings-every-pattern.md#sign-in));
  add `--cni-tunnel geneve` on networks that pass geneve but not vxlan.

## Load balancer endpoint

Use `node` with a load balancer's name or address when the control planes share
no layer-2 segment (a cloud, routers) or you have a DNS name and no load
balancer. Same procedure, with that address as `--endpoint-address` and the first
node's own address as `--node-endpoint`. Init warns that a VIP is recommended
(changing the endpoint later re-issues every certificate).

Ichor renders `https://ADDRESS:6443` and nothing more; failover is yours, and it
does not tell you when what is in front is down. The kubeconfig names that address.

- **A TCP load balancer** on 6443 to every control plane. It must forward TCP
  and not terminate TLS (the API server handles mutual TLS itself), so an HTTP
  load balancer will not work. See the
  [Talos production notes](https://docs.siderolabs.com/talos/v1.14/getting-started/prodnotes).
- **One DNS record per control plane**: an `A` (or `AAAA`) record each, all with
  one name used as the endpoint. DNS does no health checks, so a dead control
  plane keeps being handed out.

If it fails, `kubectl` and the kubeconfig lose the API. The kubelet, scheduler,
controller manager and kube-proxy use KubePrism, Talos's local endpoint, and are
unaffected; Ichor's controller dials each machine's own address, so Talos API
traffic never passes through the endpoint.

A cluster still on the deprecated `loadBalancer` type must set
`spec.endpoint.type` to `node`; address and kubeconfig stay.

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo
  preset: standard
  endpoint:
    type: node
    address: kube.example.lan
firstNode:
  endpoint: 192.168.1.50:50000
  installDisk: /dev/sda
```

```sh
ichor init --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address kube.example.lan \
  --install-disk /dev/sda
```

Approve the other control planes as on the [Virtual IP](vip.md#flags) page, with
`--role controlplane`.
