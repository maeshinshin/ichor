---
title: "ichor get"
description: "List objects, or show a cluster in a few lines"
---

## ichor get

List objects, or show a cluster in a few lines

### Synopsis

get lists clusters, nodes, TalosConfigPatches, operations, users, Ichor's settings and
Cilium's Helm values. Add -o yaml or -o json for scripts. Use describe to see one object in full.

### Examples

```sh
  ichor get clusters
  ichor get nodes --cluster prod
  ichor get operations -o json
```

### Options

```text
  -h, --help   help for get
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
* [ichor get cluster](ichor_get_cluster.md)	 - Show a cluster's versions, endpoint and CNI in a few lines
* [ichor get clusters](ichor_get_clusters.md)	 - List clusters
* [ichor get cni-values](ichor_get_cni-values.md)	 - Print your layer of Cilium's values (--layer shows the others)
* [ichor get nodes](ichor_get_nodes.md)	 - List nodes, pending ones included
* [ichor get operations](ichor_get_operations.md)	 - List operations
* [ichor get patches](ichor_get_patches.md)	 - List patches
* [ichor get settings](ichor_get_settings.md)	 - Show each setting, its value in force and where the value comes from (spec or deployment)
* [ichor get users](ichor_get_users.md)	 - List users

