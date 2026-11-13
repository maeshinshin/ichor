---
title: "Certificate names"
description: "Which names and addresses the Talos API and Kubernetes API certificates carry by default, which client can use which address, and why changing the extra names later reboots every node."
sidebar_order: 45
---

# Certificate names

A cluster serves two certificates that clients check, and they do not carry the
same names. This page says what each one holds by default, so you can answer
"can I point this client at that address?" without reading any code. You do
not configure anything to get the defaults.

## Two certificates, two lists

| | Talos API | Kubernetes API |
|---|---|---|
| Used by | `talosctl` | `kubectl` |
| Served by | `apid` on every node, port 50000 | `kube-apiserver` on every control plane, port 6443 |
| Present on | control planes and workers | control planes only |

The two lists are different. A name that works for `kubectl` does not
necessarily work for `talosctl`, and the reverse.

## What the Talos API certificate holds

Each node's Talos API certificate holds:

- the node's own addresses: every address the node holds at link scope or wider,
  except loopback, link-local and multicast addresses;
- `127.0.0.1`;
- the node's hostname and its fully qualified name;
- `machine.certSANs` from the node's configuration: the names you add with
  `--cert-san` (see below), and the address `ichor init` wrote for the first
  node. That address is in every node's configuration, so it is in the list
  even on a node that does not hold it.

It does not hold the cluster endpoint, the service IP or the in-cluster names.

## What the Kubernetes API certificate holds

Each control plane's kube-apiserver certificate holds:

- the cluster endpoint's host: the address or DNS name in `--endpoint-address`,
  such as the VIP. This is why `kubectl` works through the endpoint;
- the control plane's own addresses, except those in the pod and service ranges;
- the service IP: the first address of the service range, `10.96.0.1` with the
  default `10.96.0.0/12`;
- `kubernetes`, `kubernetes.default`, `kubernetes.default.svc` and
  `kubernetes.default.svc.cluster.local` (`cluster.local` is the default DNS
  domain);
- `localhost` and `127.0.0.1`;
- the control plane's hostname and fully qualified name;
- the names you add with `--cert-san`.

Its certificate is reissued whenever its node's addresses change, so a control
plane that gets a new address picks it up without any action from you.

## Can I use this address?

| Client and address | Answer | Why |
|---|---|---|
| `kubectl` to the endpoint (VIP, load balancer or DNS name) | yes | the endpoint is in every control plane's Kubernetes API certificate |
| `kubectl` to one control plane's own address, bypassing the VIP | yes | that control plane's certificate lists its own addresses. This is what to use during an outage |
| `kubectl` to another control plane's address | yes | each control plane's certificate lists its own addresses |
| `talosctl` to a node's own address | yes | every node's Talos API certificate lists its own addresses |
| `talosctl` to the VIP or the endpoint | not reliably | the endpoint is not in the Talos API certificate. A VIP is listed only in the certificate of the node that holds it, and it moves on failover. Point `talosctl` at node addresses |
| any other name (a DNS name, a load balancer's name) | only if you add it | use `--cert-san` at `init`, or the names in the next section |

## Adding names

Pass `--cert-san` to `ichor init`, once for each name or IP address, or set
`cluster.certSANs` in the `-f` file. Up to 64 names, each up to 253 characters.

```text
ichor init --cluster home --endpoint-type vip --endpoint-address 192.168.0.210 \
  --cert-san ichor.lan --cert-san 192.168.0.250 ...
```

Each name goes into both certificates. A name you add is therefore accepted by
`talosctl` and by `kubectl`, and a name you do not add is accepted by neither
unless it is a node address, the endpoint or one of the names listed above.

The value is stored as `spec.certSANs` on the `TalosCluster`, the one place
these names are set.

## Changing the names later costs a reboot

`spec.certSANs` is part of every node's machine configuration. Changing it on
an existing cluster reissues the certificates on every node, and every node
reboots to apply it. The kube-apiserver on each control plane restarts as well.
Adding a name is the same change as removing one.

Set the names at `init` when you can. Editing `spec.certSANs` later works, but
plan a reboot of the whole cluster for it.
