---
sidebar_order: 1
---

# Virtual IP

A virtual IP (VIP) is one address on the nodes' network that belongs to the
cluster, not to any node: the kubeconfig, the nodes and Ichor's controller keep
one address whichever control plane answers. It is the ordinary choice.

> [!IMPORTANT]
> The control planes must share a layer-2 segment: the address moves by
> gratuitous ARP, and a router between them stops that (the cluster can look
> healthy while the VIP does not answer). See the Talos
> [virtual IP page](https://docs.siderolabs.com/talos/v1.14/networking/advanced/vip).

Pick a free address on that segment. It must lie inside a subnet of a control
plane's static addresses and not equal one of them: otherwise Ichor refuses it
when rendering and when the endpoint is set on a `TalosCluster`, naming
`spec.endpoint.address` (an address elsewhere is a load balancer's: use `node`).
The first node keeps its own address for `--node-endpoint`.

Do not use the VIP as a Talos API endpoint: `talosctl` and `--node-endpoint`
name a machine's own address, since the VIP depends on etcd and the API server
being healthy.

![Three control planes, each with its own address. The virtual IP 192.168.1.40 is on one of them at a time, on that machine's link enp1s0: on control plane 1 while it is up, and on control plane 2 once control plane 1 is gone. kubectl and the kubeconfig use the VIP; talosctl uses each machine's own address.](../../../images/create-the-cluster-vip.light.drawio.svg)

*One address, held by one control plane at a time. When that one is gone, another takes it over.*

## Machines that already run Talos

Init needs a machine in maintenance mode; skip this for one already there. A
machine that runs Talos must be reset first.

> [!WARNING]
> This erases the machine's configuration, certificates (`STATE`) and data
> (`EPHEMERAL`). Run it only on a machine whose old cluster you are giving up.

Reset it with the old cluster's talosconfig as the current context
(`ichor reclaim node` does this with an inspection and a typed UUID:
[Reclaim a machine](../../reclaim-a-machine.md)):

```sh
talosctl reset --nodes 192.168.1.50 \
  --system-labels-to-wipe STATE \
  --system-labels-to-wipe EPHEMERAL \
  --reboot=true --graceful=false
```

Exit status 0 only says Talos accepted the request; a failed wipe goes to the
node's log. Ask the node for its volumes (`--insecure`, as it is in maintenance mode):

```sh
talosctl get discoveredvolumes --insecure --nodes 192.168.1.50
```

It worked when only `META` is listed. If `STATE` or `EPHEMERAL` still appears,
do not run `ichor init`: check the node with the old talosconfig and reset again
once you know why it failed. `META` is kept and may hold static network settings,
so the node may return on its old address.

## Interactive

Run `ichor init --preset single`. On a terminal init asks, in this order, for each
required setting not given:

- **Cluster name**
- **Endpoint type:** pick `vip`.
- **Endpoint address:** the free IP address.
- **First node:** a network such as `192.168.1.0/24` to scan on port 50000, or the
  node's address, which skips the scan.
- **Node address:** with prefix length, such as `192.168.1.50/24`; asked unless
  `--address` is given. Enter on the prefilled address without a prefix is
  refused; clear the field to keep DHCP.
- **Install disk** and **Primary link** (listed as `enp1s0 <84:47:09:5c:ed:49>`).

Esc or Ctrl-C cancels and writes nothing. Without a terminal pass flags or the file.

## `init.yaml`

Save as `init.yaml` and run `ichor init -f init.yaml`.

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
  required. Prefer `serial:SERIAL`, `wwid:ID` or a `/dev/disk/by-id/` path to a
  device name, which is not stable across reboots.
- `--nic` names the link by MAC or name (`enp1s0`); Ichor stores its permanent MAC.
  The machine configuration binds the link as the alias `net0`
  (`LinkAliasConfig`) by that MAC, and the VIP goes on `net0`.
- `--address` (repeatable, CIDR) makes the address static; `--nic` alone means
  DHCP. `--gateway` needs a static `--address`. `--dns` is repeatable; see also `--vlan`.

`--preset single` makes one node. The default `standard` has two more control
planes: leave the flag out, find each with `ichor discovery add`, and approve it as
a control plane with its own link and address:

```sh
ichor discovery add 192.168.1.51 --cluster demo
ichor approve node NAME --role controlplane --install-disk /dev/sda \
  --nic enp1s0 --address 192.168.1.51/24 --gateway 192.168.1.1 --dns 192.168.1.1
```

`NAME` is the pending machine from `ichor get nodes`; repeat for the third.

### Give every control plane a static address

Ichor puts the VIP on the link a node's `spec.network` names, so every control
plane needs one. A control plane without it never holds the VIP, and `ichor approve
node` refuses one once another control plane has a link. Setting the endpoint to
`vip` on an existing cluster adds no link: the webhook warns, naming each control
plane without a `spec.network`.

Use a static `--address`, not DHCP: with one, Ichor makes etcd advertise from that
subnet (`cluster.etcd.advertisedSubnets`). On DHCP Talos advertises from the first
routable address, possibly a stale lease beside yours, and etcd then lists that
lease as its peer URL. Approval warns about a DHCP control plane. A running etcd
keeps its peer URL until it restarts with the new configuration.
