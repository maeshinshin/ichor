---
title: "ichor describe node"
description: "Show one node: its record, and its hardware, usage and Kubernetes status when ichor-ui is reachable"
---

## ichor describe node

Show one node: its record, and its hardware, usage and Kubernetes status when ichor-ui is reachable

### Synopsis

describe node shows the node's record (the TalosMachine: role, approval, install disk, network, disks,
conditions) and, below it, what only a live read of the node gives: its hardware, CPU, memory and filesystem
use, disks and the Kubernetes Node. The live part is read through ichor-ui, which holds the read-only Talos
identity (the CLI never talks to a node itself).

When ichor-ui is not reachable, or the node cannot be read (it is still pending, or powered off), the record
alone is shown, with a line saying why the live part is missing. -o yaml and -o json print the record under
"machine" and, when it was read, the live part under "node".

```sh
ichor describe node NAME [flags]
```

### Examples

```sh
  ichor describe node cp-1
  ichor describe node worker-3 -o yaml
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for node
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor describe](ichor_describe.md)	 - Show one object in full

