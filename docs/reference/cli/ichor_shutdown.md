---
title: "ichor shutdown"
description: "Shut a node down, or every node of a cluster"
---

## ichor shutdown

Shut a node down, or every node of a cluster

```sh
ichor shutdown [flags]
```

### Examples

```sh
  ichor shutdown node worker-3
  ichor shutdown cluster lab --follow
```

### Options

```text
  -h, --help   help for shutdown
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

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs
* [ichor shutdown cluster](ichor_shutdown_cluster.md)	 - Stop every node of a cluster in the order that keeps the API up longest
* [ichor shutdown node](ichor_shutdown_node.md)	 - Shut a node down; it stays off until you power it on

