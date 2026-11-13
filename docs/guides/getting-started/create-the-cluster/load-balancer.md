---
sidebar_order: 2
---

# Load balancer

Use this pattern when the control planes have no shared layer-2 segment, for
example in a cloud or across routers, and you run a TCP load balancer in front of
them. The endpoint is the load balancer's name or address.

Ichor treats this type the same as a VIP or a DNS name: it renders the API
address as `https://ADDRESS:6443`, and nothing more. It does not create
a load balancer, add a health check to one, or tell you when it is down. Those
parts are yours to run.

The load balancer is a TCP forwarder on port 6443. It needs a frontend listening
on TCP 6443 and forwarding to the address of every control plane. A forwarder
on a router, a small machine running HAProxy, or a cloud load balancer all do the
job; the [Talos production notes](https://docs.siderolabs.com/talos/v1.14/getting-started/prodnotes)
describe the setup.

Two things follow from that, and both are easy to miss:

- If the forwarder dies, `kubectl` and the kubeconfig Ichor hands out lose the
  Kubernetes API, even when every control plane is healthy. Watch the forwarder
  as you would any other single point of failure.
- Ichor's controller dials each machine's own address, so the Talos API traffic
  does not pass through the load balancer.

Init prints a warning for this type that a VIP is recommended. The reason is on
the [Virtual IP](vip.md) page: changing the endpoint later re-issues every
certificate in the cluster.

## Interactive

Run `ichor init` and pick `loadBalancer` from the endpoint type list. At the
endpoint address prompt, type the IP address or the hostname the load balancer
answers on. The node address and the install disk are asked as
described on the [Virtual IP](vip.md#interactive) page.

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
- `--endpoint-address` is the IP address or hostname the load balancer answers
  on. Use the forwarder's address, not a machine's address.
- `--node-endpoint` is still the first node's own Talos API (`host:port`).
- If the first node needs a static address or a named link, the network flags
  (`--nic`, `--address`, `--gateway`, `--dns`, `--vlan`) are the same as on the
  [Virtual IP](vip.md#flags) page.
- Approve the other control planes as on that page, with `--role controlplane`.
