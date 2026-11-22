---
title: "Certificate names"
description: "Which names and addresses the Talos API and Kubernetes API certificates carry by default, which client can use which address, and why changing the extra names later reboots every node."
sidebar_group: "Trust and access"
sidebar_order: 45
---

# Certificate names

`talosctl` and `kubectl` check different certificates, and the two carry
different names. The defaults need no configuration.

![Two certificates. The Talos API certificate on every node lists the node's own addresses, 127.0.0.1, the hostname and machine.certSANs. The Kubernetes API certificate on each control plane lists the endpoint, the control plane's addresses, the service IP and kubernetes.default names. Names from --cert-san go into both.](../images/certificate-names.light.drawio.svg)

*What each certificate holds, and where your own names go.*

## What each certificate holds

| | Talos API | Kubernetes API |
|---|---|---|
| Client | `talosctl` | `kubectl` |
| Served by | `apid`, port 50000, every node | `kube-apiserver`, port 6443, control planes |
| Addresses | the node's own, except loopback, link-local and multicast | the control plane's own, except pod and service ranges; reissued when they change |
| Endpoint | no | yes (the host of `--endpoint-address`) |
| Fixed names | `127.0.0.1`, hostname, FQDN | `localhost`, `127.0.0.1`, hostname, FQDN, the service IP (`10.96.0.1` by default), `kubernetes`, `kubernetes.default`, `kubernetes.default.svc`, `kubernetes.default.svc.cluster.local` |
| Your names | `machine.certSANs`, which also holds the address `ichor init` wrote for the first node | `--cert-san` names |

## Can I use this address?

| Client and address | Answer | Why |
|---|---|---|
| `kubectl` to the endpoint (VIP, load balancer, DNS name) | yes | the endpoint is in every control plane's certificate |
| `kubectl` to a control plane's own address | yes | use this during an outage |
| `talosctl` to a node's own address | yes | every node lists its own addresses |
| `talosctl` to the VIP or the endpoint | not reliably | the endpoint is not in the Talos API certificate, and a VIP is listed only on the node that holds it. Use node addresses |
| any other name | only if you add it | `--cert-san` |

## Adding names

Pass `--cert-san` to `ichor init` once per name or IP address, or set
`cluster.certSANs` in the `-f` file. Up to 64 names, 253 characters each. Each
name goes into both certificates. The value is stored as `spec.certSANs` on the
`TalosCluster`.

```text
ichor init --cluster home --endpoint-type vip --endpoint-address 192.168.0.210 \
  --cert-san ichor.lan --cert-san 192.168.0.250 ...
```

> [!WARNING]
> `spec.certSANs` is part of every node's machine configuration. Adding or
> removing a name reissues the certificates, reboots every node and restarts
> each kube-apiserver. Set the names at `init`.
