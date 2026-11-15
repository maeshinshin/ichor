---
sidebar_order: 2
---

# Single node endpoint

The `node` endpoint is the first node's own address. Use it for a cluster of one
node, such as a lab or a single machine: it is the simplest pattern and needs no
second address on the network. The [Load balancer endpoint](#load-balancer-endpoint)
section at the end of this page uses the same procedure with an address you run in
front of the control planes.

Nothing has to be run for the node endpoint, but one control plane's API server
serves every client. During an upgrade that replaces that node's API server, the
Kubernetes API is unreachable for everyone, and the upgrade warns about it. For
a cluster with more than one control plane, prefer the [Virtual IP](vip.md)
pattern.

## Interactive

Run `ichor init --preset single` and answer what it asks for. On a terminal the
required settings can be left out, and `ichor init` asks for each one that was
not given, in this order:

- **Cluster name**
- **Endpoint type:** pick `node` from the list.
- **Endpoint address:** for this pattern, the node's own IP address.
- **First node:** a network such as `192.168.1.0/24` (your own network is the
  default) to scan on port 50000 for nodes in maintenance mode, which are listed
  to pick from; or the node's address, which skips the scan.
- **Node address:** see below. Unlike the others, this one is asked even when
  every other setting came from flags or a file.
- **Install disk:** from the disks the node reports.

Settings given as flags or in the file are not asked for again; for **Node
address** that means `--address` or `firstNode.addresses`.

```console
$ ichor init --preset single
  Cluster name       demo
  Endpoint type      node
› Endpoint address   192.168.1.50▏
  an IP address or a hostname is expected here

  enter confirm · esc cancel
```

The endpoint type is a list whose entries read `vip (an IP address)` and `node
(an IP address or a hostname)`: typing narrows it, the arrow keys move, Enter
selects. Once answered, a line shows the value you chose (`node`), not the
list entry. Text fields check the answer as you type and say what is wrong. The
install disk is asked from the disks the node reports, and a node with one disk
has it chosen and named. Esc or Ctrl-C cancels and nothing is written.

**Node address** is asked whenever `--address` is not given, and it starts
filled in with the node's address, without a prefix length:

```console
› Node address   192.168.1.50▏
  the node's own address with its prefix length, such as 192.168.0.201/16; empty keeps DHCP on the link
```

Pressing Enter on that value as it stands is refused, because the answer must
be an IPv4 address with its prefix length, such as `192.168.1.50/24`. Add the
prefix length to give the node that static address. To leave the node on DHCP
instead, clear the field and press Enter.

With no terminal (in CI, or from a pod) there is no prompt: a missing setting
fails with the same "required flag" error as always, so scripts are
unaffected. Use the flags there.

## `init.yaml`

Save this as `init.yaml` and run `ichor init -f init.yaml`:

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

The file is read as described on [Settings every pattern shares](settings-every-pattern.md#the-inityaml-file).
This file has no `firstNode.addresses`, so on a terminal `ichor init -f
init.yaml` still asks for **Node address** (see [Interactive](#interactive)):
clear the field to keep DHCP, or add `addresses` to the file.

## Flags

```sh
ichor init \
  --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address 192.168.1.50 \
  --install-disk /dev/sda \
  --preset single
```

- `--cluster`, `--node-endpoint`, `--endpoint-address` and `--install-disk` are
  required. `--node-endpoint` is the node's Talos API (`host:port`); the install
  disk can also be `wwid:ID`, `serial:SERIAL` or a `/dev/disk/by-id/` path.
- `--endpoint-type node` takes the node's own IP address as `--endpoint-address`.
- With no terminal, Ichor configures no network for the first node unless you
  give its network flags (`--nic`, `--address`, `--gateway`, `--dns`,
  `--vlan`); without them Talos's default applies: a DHCP client on every
  physical network interface. On a terminal, init asks for **Node address**
  when `--address` is not given, even with every other flag set: clear the
  field to get that default (see [Interactive](#interactive)), or pass
  `--address` to answer it up front.
- `--preset single` makes the node one schedulable control plane. There is
  nothing to approve afterwards, so skip the approve step of the guide.
- Sign-in is on by default: init creates the first admin and prints its
  password once. See [Sign-in](settings-every-pattern.md#sign-in).
- On virtual networks that pass geneve but not vxlan, add `--cni-tunnel geneve`
  (see [Settings every pattern shares](settings-every-pattern.md#the-shape-of-the-cluster)).

## Load balancer endpoint

Use the `node` endpoint with a load balancer's name or address when the control
planes have no shared layer-2 segment, for example in a cloud or across routers,
or when you have a DNS name and no load balancer. The endpoint is the IP address
or the DNS name that answers on port 6443, and it goes in `--endpoint-address`.
The procedure is the same as above, with that address in place of the node's own,
and the first node's own address as `--node-endpoint`.

> [!NOTE]
> A load balancer's name or address goes in `node`, which takes a DNS name as
> well as an IP address. The kubeconfig names the endpoint, so it names the load
> balancer. Failover is yours: Ichor creates no load balancer, no DNS record and
> no health check, and does not tell you when what is in front of the control
> planes is down.
>
> A cluster that still has the `loadBalancer` type cannot change its endpoint
> while it keeps that type. Set its `spec.endpoint.type` to `node`: that one edit
> moves it, and the address and the kubeconfig stay as they are.

Init prints a warning for this type that a VIP is recommended. The reason is on
the [Virtual IP](vip.md) page: changing the endpoint later re-issues every
certificate in the cluster.

Ichor renders the API address as `https://ADDRESS:6443`, and nothing more. What
sits in front of the control planes is yours to run. Two common choices:

- **A TCP load balancer.** A forwarder on port 6443 sends traffic to every control
  plane. It must forward TCP and must not terminate TLS: the Kubernetes API server
  handles TLS and mutual TLS itself, so an HTTP load balancer will not work. The
  [Talos production notes](https://docs.siderolabs.com/talos/v1.14/getting-started/prodnotes)
  describe the setup. A forwarder on a router, a small machine running HAProxy, or
  a cloud load balancer all do the job.
- **One DNS record per control plane.** Create an `A` (or `AAAA`) record for each
  control plane, all with the same name, and use that name as the endpoint. It
  costs nothing to run, but DNS does no health checking: a control plane that is
  down keeps being handed out until you change its record.

If the thing in front fails, `kubectl` and the kubeconfig Ichor hands out lose
the Kubernetes API. The components Talos points at KubePrism, the local endpoint
it enables by default on each node (the kubelet, the scheduler, the controller
manager and kube-proxy), do not depend on that endpoint, so a dead endpoint does
not take them down with it. Ichor's controller dials each machine's own address,
so Talos API traffic does not pass through the endpoint.

Interactively, pick `node` from the endpoint type list. At the endpoint address
prompt, type the IP address or the DNS name that answers on port 6443.

Save this as `init.yaml` and run `ichor init -f init.yaml`:

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

With flags:

```sh
ichor init --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address kube.example.lan \
  --install-disk /dev/sda
```

- `--endpoint-address` is the IP address or DNS name that answers on port 6443.
  Use the forwarder's or the record's name, not a machine's address.
- `--node-endpoint` is still the first node's own Talos API (`host:port`).
- Approve the other control planes as on the [Virtual IP](vip.md#flags) page, with `--role controlplane`.
- Everything else, including sign-in, is the same as above. The settings every
  pattern shares are on [Settings every pattern shares](settings-every-pattern.md).
