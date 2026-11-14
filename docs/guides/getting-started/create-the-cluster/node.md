---
sidebar_order: 3
---

# Node address

The endpoint is the first node's own address. Use this pattern for a cluster of
one node, such as a lab or a single machine. It is the simplest pattern and
needs no second address on the network.

Nothing has to be run for this pattern, but one control plane's API server
serves every client. During an upgrade that replaces that node's API server, the
Kubernetes API is unreachable for everyone, and the upgrade warns about it. For
a cluster with more than one control plane, prefer the [Virtual IP](vip.md)
pattern.

## Interactive

Run `ichor init --preset single` and answer what it asks for. On a terminal the
required settings can be left out, and `ichor init` asks for each one that was
not given: the cluster name, the endpoint type, the endpoint address and the
node address. Settings given as flags are not asked for again.

```console
$ ichor init --preset single
  Cluster name       demo
  Endpoint type      node (an IP address)
› Endpoint address   192.168.1.50▏
  an IP address is expected here

  enter confirm · esc cancel
```

The endpoint type is a list: typing narrows it, the arrow keys move, Enter
selects. Text fields check the answer as you type and say what is wrong. The
install disk is asked last, from the disks the node reports; a node with one
disk has it chosen and named. Esc or Ctrl-C cancels and nothing is written.

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
- With this pattern Ichor configures no network for the first node unless you give its network flags (`--nic`, `--address`, `--gateway`, `--dns`, `--vlan`); then Talos's default applies: a DHCP client on every physical network interface.
- `--preset single` makes the node one schedulable control plane. There is
  nothing to approve afterwards, so skip the approve step of the guide.
- Sign-in is on by default: init creates the first admin and prints its
  password once. See [Sign-in](settings-every-pattern.md#sign-in).
- On virtual networks that pass geneve but not vxlan, add `--cni-tunnel geneve`
  (see [Settings every pattern shares](settings-every-pattern.md#the-shape-of-the-cluster)).
