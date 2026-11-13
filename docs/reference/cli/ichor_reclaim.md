---
title: "ichor reclaim"
description: "Reset a configured Talos node of another cluster back to maintenance mode"
---

## ichor reclaim

Reset a configured Talos node of another cluster back to maintenance mode

### Examples

```sh
  ichor reclaim node 192.168.10.21 --talosconfig old-cluster/talosconfig --cluster prod
```

### Options

```text
  -h, --help   help for reclaim
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
* [ichor reclaim node](ichor_reclaim_node.md)	 - Reset a configured Talos node of another cluster back to maintenance mode

