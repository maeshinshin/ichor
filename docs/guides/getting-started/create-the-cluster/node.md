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
install disk is asked from the disks the node reports, and a node with one disk
has it chosen and named. Esc or Ctrl-C cancels and nothing is written.

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

## Load balancer endpoint

Use `loadBalancer` when the control planes have no shared layer-2 segment, for
example in a cloud or across routers, or when you have a DNS name and no load
balancer. The endpoint is an IP address or a hostname that answers on port 6443.
The procedure is the same as above: run `ichor init` with `--endpoint-type
loadBalancer` and the address of the thing you run in front of the control
planes, and give the first node's own address as `--node-endpoint`.

> [!NOTE]
> **Why not `node` with the load balancer's address in `certSANs`?** That is
> half the answer, and it is the half that is easy to see. Ichor's
> `spec.certSANs` is written into the Talos API certificate of every node and
> into the kube-apiserver serving certificate, so listing the load balancer's
> address or name there makes the API server's certificate valid for it. Under
> `loadBalancer` the endpoint's own hostname is put on the kube-apiserver
> certificate by Talos, so you list nothing. What you do not get by listing it
> under `node` is the rest:
>
> - **The kubeconfig still names the node.** The kubeconfig's server is the
>   cluster endpoint, `https://ADDRESS:6443`, and under `node` that is the
>   node's own address. You would have to edit the server line by hand, and that
>   kubeconfig then stops working when the node is down. Under `loadBalancer` the
>   kubeconfig names the load balancer, and the failover is whatever you run in
>   front of the control planes.
> - **Failover is yours either way.** Ichor creates no load balancer, no DNS
>   record and no health check, and does not tell you when what is in front of
>   the control planes is down.
> - **A hostname is refused for `node`.** `CheckAddress` takes an IP address for
>   `vip` and `node`, and refuses a name for either, when `init` plans the
>   cluster and when the webhook admits a TalosCluster whose endpoint changes.
>   `loadBalancer` takes a name, such as `kube.example.lan`.
> - **The controller's egress allowance follows the endpoint.** The controller's
>   egress policy is built from `spec.endpoint.address`, and the API server port
>   it allows (the endpoint's `/readyz` check) names that address. Under `node`
>   that is the node, not the load balancer. A hostname goes in by name.
> - **`adopt` chooses it for you.** `ichor adopt` types the endpoint `node` only
>   when the address is one node's own. Any other address, including a load
>   balancer's, becomes `loadBalancer`, and `adopt` never chooses `vip`.

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

Interactively, pick `loadBalancer` from the endpoint type list. At the endpoint
address prompt, type the IP address or the hostname that answers on port 6443.

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

With flags:

```sh
ichor init --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type loadBalancer --endpoint-address kube.example.lan \
  --install-disk /dev/sda
```

- `--endpoint-address` is the IP address or hostname that answers on port 6443.
  Use the forwarder's or the record's name, not a machine's address.
- `--node-endpoint` is still the first node's own Talos API (`host:port`).
- Approve the other control planes as on the [Virtual IP](vip.md#flags) page, with `--role controlplane`.
- Everything else, including sign-in, is the same as above. The settings every
  pattern shares are on [Settings every pattern shares](settings-every-pattern.md).
