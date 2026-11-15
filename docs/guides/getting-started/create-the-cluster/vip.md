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
address, which the node endpoint (`--node-endpoint`) uses. The examples on this
page pass `--preset single`, one node; a VIP works for one node too, and it is
still recommended.

The VIP is the endpoint for the set of control planes, which is what kubectl and
the kubeconfig use. Do not use the VIP as a Talos API endpoint: `talosctl` and
`--node-endpoint` name a machine's own address. The VIP depends on etcd and the
API server being healthy, so when either is down the VIP cannot be reached, and
the Talos API is then the way to recover, which needs the machine's own address.

![Three control planes, each with its own address. The virtual IP 192.168.1.40 is on one of them at a time, on that machine's link enp1s0: on control plane 1 while it is up, and on control plane 2 once control plane 1 is gone. kubectl and the kubeconfig use the VIP; talosctl uses each machine's own address.](../../../images/create-the-cluster-vip.light.drawio.svg)

*One address, held by one control plane at a time. When that one is gone, another takes it over.*

## Machines that already run Talos

Ichor creates a cluster on machines in maintenance mode. A machine that already
runs Talos, such as one left over from another cluster, has to go back to
maintenance mode first. Skip this section for a machine that is already in
maintenance mode.

> [!WARNING]
> This reset erases the machine's configuration and cluster certificates (the
> `STATE` partition) and its data (`EPHEMERAL`). Run it only on a machine whose
> old cluster you are giving up, after you have kept any data you need.

Reset the machine with the old cluster's talosconfig as the current context:

```sh
talosctl reset --nodes 192.168.1.50 \
  --system-labels-to-wipe STATE \
  --system-labels-to-wipe EPHEMERAL \
  --reboot=true --graceful=false
```

Do not stop at the exit status. Talos accepts the request and does the wipe
afterwards, and a failed wipe is written to the node's log rather than returned
to the command. The default `--wait` also passes when the node only reboots,
because its post-check accepts a reboot. An exit status of 0 therefore says the
request was accepted, not that the disk was wiped. Ask the node for its volumes
instead:

```sh
talosctl get discoveredvolumes --insecure --nodes 192.168.1.50
```

The wipe worked when `STATE` and `EPHEMERAL` are no longer listed and only
`META` is. This command reads the node in maintenance mode, which is why it
takes `--insecure`. If `STATE` or `EPHEMERAL` is still listed, the node still
holds its old configuration: do not run `ichor init` against it. Check the
node again with the old talosconfig, without `--insecure`, and reset it again
only once you know why the first reset did not wipe it.

`META` is not in the list of partitions to wipe, so the reset keeps it. `META`
can hold the machine's static network settings, and the node may come back on
the address it had.

## Interactive

Run `ichor init --preset single` with the settings you know. On a terminal, init asks for each
required setting you did not give, in this order:

- **Cluster name**
- **Endpoint type:** pick `vip` from the list.
- **Endpoint address:** the free address, an IP address.
- **First node:** a network such as `192.168.1.0/24` (your own network is the
  default) to scan on port 50000 for nodes in maintenance mode, which are listed
  to pick from; or the node's address, which skips the scan.
- **Node address:** the first node's address with its prefix length, such as
  `192.168.1.50/24`. Asked whenever `--address` is not given, even when every
  other setting came from flags or a file. It starts filled in with the node's
  address without a prefix length, and Enter on that is refused (the answer
  must be an IPv4 address with its prefix length). Clear the field to keep DHCP
  on the link.
- **Install disk:** from the disks the node reports.
- **Primary link:** the first node's network link. The picker lists each link
  as `enp1s0 <84:47:09:5c:ed:49>`, the name followed by the MAC address.

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
  preset: single
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
  --gateway 192.168.1.1 --dns 192.168.1.1 \
  --preset single
```

- `--cluster`, `--node-endpoint`, `--endpoint-address` and `--install-disk` are
  required.
- `--install-disk` takes a device such as `/dev/sda`, or `serial:SERIAL` (or
  `wwid:ID`, or a `/dev/disk/by-id/` path). Prefer the serial or the id: device
  names are not stable across reboots.
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

`--preset single` makes one node. The default preset, `standard`, has two more
control planes: leave out `--preset single` (or set `preset: standard` in the
file), find each further one with `ichor discovery add`, then approve it as a
control plane with the same network flags for its own link and address:

```sh
ichor discovery add 192.168.1.51 --cluster demo
ichor approve node NAME --role controlplane --install-disk /dev/sda \
  --nic enp1s0 --address 192.168.1.51/24 --gateway 192.168.1.1 --dns 192.168.1.1
```

`NAME` is the pending machine from `ichor get nodes`. Repeat the approval for
the third control plane with its own address.
