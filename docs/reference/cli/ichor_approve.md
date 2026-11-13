---
title: "ichor approve"
description: "Approve a pending node so the controller starts configuring it"
---

## ichor approve

Approve a pending node so the controller starts configuring it

### Examples

```sh
  ichor approve node 3f1c2a --role worker
```

### Options

```text
  -h, --help   help for approve
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
* [ichor approve node](ichor_approve_node.md)	 - Approve a pending node so the controller starts configuring it

