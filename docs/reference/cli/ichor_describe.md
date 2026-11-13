---
title: "ichor describe"
description: "Show one object in full"
---

## ichor describe

Show one object in full

### Synopsis

describe shows one cluster, node, patch, operation, user or node configuration in full.
It reads the same resources the UI shows.

```sh
ichor describe [flags]
```

### Examples

```sh
  ichor describe cluster prod
  ichor describe node cp-1
```

### Options

```text
  -h, --help   help for describe
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
* [ichor describe cluster](ichor_describe_cluster.md)	 - Show one cluster: versions, conditions, rollout and hand-over state
* [ichor describe config](ichor_describe_config.md)	 - Show a node's rendered configuration, each value tagged with the layer that set it
* [ichor describe node](ichor_describe_node.md)	 - Show one node: its record, and its hardware, usage and Kubernetes status when ichor-ui is reachable
* [ichor describe operation](ichor_describe_operation.md)	 - Show one operation: phase, failure reason and the state of each node
* [ichor describe patch](ichor_describe_patch.md)	 - Show one patch
* [ichor describe user](ichor_describe_user.md)	 - Show one user

