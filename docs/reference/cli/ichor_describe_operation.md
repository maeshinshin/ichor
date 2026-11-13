---
title: "ichor describe operation"
description: "Show one operation: phase, failure reason and the state of each node"
---

## ichor describe operation

Show one operation: phase, failure reason and the state of each node

### Synopsis

describe operation prints the operation's status as the controller wrote it. With --watch it
keeps polling, prints a progress line to stderr whenever the status changes, and
exits when the operation completes (or pauses itself because a node failed,
which is an error exit).

An operation is the record of one node-by-node action: a Talos, Kubernetes, Ichor or Cilium
upgrade, a kube-proxy switch, a configuration rollout, a rotation or a shutdown cluster. The operation commands
read and change the records; the commands that do the action (`ichor upgrade`, `ichor set kube-proxy`, `ichor shutdown cluster`) start them.

```sh
ichor describe operation NAME [flags]
```

### Examples

```sh
  ichor describe operation upgrade-talos-x7k2p
  ichor describe operation upgrade-talos-x7k2p --watch
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for operation
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
  -w, --watch                    keep polling and print progress until the operation finishes
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

