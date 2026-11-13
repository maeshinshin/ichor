---
sidebar_order: 1
---

# Virtual IP

A virtual IP (VIP) is one address on the nodes' network that belongs to the
cluster rather than to any one node. It is the ordinary way to create a
cluster. The control planes share the address, so the kubeconfig, the other
nodes and Ichor's controller keep using one address whichever control plane is
answering for it.

> [!IMPORTANT]
> A VIP needs the control planes on the same layer-2 segment. The address moves
> between control planes by gratuitous ARP, and a router between them stops that.
> On a network where this does not hold, the cluster can come up and look healthy
> while the VIP does not answer. Pick a flat home or office network, not pod
> networking or a routed subnet between the control planes. The Talos
> [virtual IP page](https://docs.siderolabs.com/talos/v1.14/networking/advanced/vip)
> states the same requirement: the switch, with no router in between.

Pick a free address on that segment for the VIP. The first node keeps its own
address, which the node endpoint (`--node-endpoint`) uses. With `--preset
single` a VIP works for one node too, and it is still recommended.

The VIP is the endpoint for the set of control planes, which is what kubectl and
the kubeconfig use. Do not use the VIP as a Talos API endpoint: `talosctl` and
`--node-endpoint` name a machine's own address. The VIP depends on etcd and the
API server being healthy, so when either is down the VIP cannot be reached, and
the Talos API is then the way to recover, which needs the machine's own address.

![Three control planes, each with its own address. The virtual IP 192.168.1.40 is on one of them at a time, on that machine's link enp1s0: on control plane 1 while it is up, and on control plane 2 once control plane 1 is gone. kubectl and the kubeconfig use the VIP; talosctl uses each machine's own address.](../../../images/create-the-cluster-vip.light.drawio.svg)

*One address, held by one control plane at a time. When that one is gone, another takes it over.*

## Interactive

Run `ichor init` with the settings you know. On a terminal, init asks for each
required setting you did not give:

- **Endpoint type:** pick `vip` from the list.
- **Endpoint address:** the free address, an IP address.
- **Node address:** the first node's Talos API, as `host:port`.
- **Link:** the first node's network link. The picker
  lists each link as `enp1s0 <84:47:09:5c:ed:49>`, the name followed by the
  MAC address.
- **Install disk:** asked last, from the disks the node reports.

Esc or Ctrl-C cancels and nothing is written. Without a terminal there is no
prompt, so pass the flags or the file.

## `init.yaml`

Save this as `init.yaml` and run `ichor init -f init.yaml`. The settings are the
same as the flags below.

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: InitConfig
cluster:
  name: demo
  preset: standard
  endpoint:
    type: vip
    address: 192.168.1.40
firstNode:
  endpoint: 192.168.1.50:50000
  installDisk: /dev/sda
  nic: enp1s0
  addresses:
    - 192.168.1.50/24
  gateway: 192.168.1.1
  dns:
    - 192.168.1.1
```

The file is read as described on [Settings every pattern shares](settings-every-pattern.md#the-inityaml-file).

## Flags

```sh
ichor init --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type vip --endpoint-address 192.168.1.40 \
  --install-disk /dev/sda \
  --nic enp1s0 --address 192.168.1.50/24 \
  --gateway 192.168.1.1 --dns 192.168.1.1
```

- `--cluster`, `--node-endpoint`, `--endpoint-address` and `--install-disk` are
  required.
- `--nic` is the first node's link. It takes either a MAC address or a link
  name such as `enp1s0`. Whichever you give, Ichor stores and renders the
  permanent MAC address of that link.
- `--address` (repeatable, CIDR) makes the address static. `--nic` alone, with no
  `--address`, means DHCP. `--gateway` needs a static `--address`; without one
  init refuses it. `--dns` is repeatable. `--vlan` is also available; `ichor init
  --help` lists it.

The network settings of the first node are not written as a name. Ichor's
machine configuration binds the link through a link alias, `net0`
(`LinkAliasConfig`), that selects the link by its permanent MAC address, and the
VIP is placed on `net0`. The alias is bound to the MAC rather than to the
kernel's name for the interface because kernel device names are not stable: they
can change between boots, and the MAC does not. So `net0` in a machine
configuration is the link you named with `--nic`, and the MAC address init
printed for it is the one it means.

The standard preset has two more control planes. Find each one with `ichor
discovery add`, then approve it as a control plane with the same network flags
for its own link and address:

```sh
ichor discovery add 192.168.1.51 --cluster demo
ichor approve node NAME --role controlplane --install-disk /dev/sda \
  --nic enp1s0 --address 192.168.1.51/24 --gateway 192.168.1.1 --dns 192.168.1.1
```

`NAME` is the pending machine from `ichor get nodes`. Repeat the approval for
the third control plane with its own address.
