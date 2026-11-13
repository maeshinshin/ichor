---
title: "ichor snapshot"
description: "Download a snapshot from a node"
---

## ichor snapshot

Download a snapshot from a node

```sh
ichor snapshot [flags]
```

### Examples

```sh
  ichor snapshot etcd --node cp-1 --out etcd.snapshot
```

### Options

```text
  -h, --help   help for snapshot
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
* [ichor snapshot etcd](ichor_snapshot_etcd.md)	 - Download an etcd snapshot from a control plane node

