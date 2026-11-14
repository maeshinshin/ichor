---
sidebar_order: 2
---

# Load balancer

Use this pattern when the control planes have no shared layer-2 segment, for
example in a cloud or across routers, or when you have a DNS name and no load
balancer. The endpoint is an IP address or a hostname. A hostname is a valid
endpoint here: if a name you control is the only thing in front of the control
planes, this is your page.

Ichor records the address you give it and renders the API address as
`https://ADDRESS:6443`, and nothing more. It creates no load balancer, no DNS
record and no health check, and it does not tell you when what is in front of
the control planes is down. Those parts are yours to run.

## What sits in front

There are two common choices. Both are yours to run.

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

Either way, if the thing in front fails, `kubectl` and the kubeconfig Ichor hands
out lose the Kubernetes API. The components Talos points at KubePrism, the local
endpoint it enables by default on each node (the kubelet, the scheduler, the
controller manager and kube-proxy), do not depend on that endpoint, so a dead
endpoint does not take them down with it. What stops is access from outside the
cluster, meaning `kubectl` and the kubeconfig Ichor hands out, and anything joining
through the endpoint. Nothing here says anything about workloads.
Ichor's controller dials each machine's own address, so Talos API
traffic does not pass through the endpoint.

Init prints a warning for this type that a VIP is recommended. The reason is on
the [Virtual IP](vip.md) page: changing the endpoint later re-issues every
certificate in the cluster.

## Interactive

Run `ichor init` and pick `loadBalancer` from the endpoint type list. At the
endpoint address prompt, type the IP address or the hostname that answers on
port 6443. The node address and the install disk are asked as described on the
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
- `--endpoint-address` is the IP address or hostname that answers on port 6443.
  Use the forwarder's or the record's name, not a machine's address.
- `--node-endpoint` is still the first node's own Talos API (`host:port`).
- With this pattern Ichor configures no network for the first node unless you give its network flags (`--nic`, `--address`, `--gateway`, `--dns`, `--vlan`); then Talos's default applies: a DHCP client on every physical network interface.
- Approve the other control planes as on that page, with `--role controlplane`.
