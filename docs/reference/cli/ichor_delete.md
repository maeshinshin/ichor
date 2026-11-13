---
title: "ichor delete"
description: "Delete a node, patch, operation or user"
---

## ichor delete

Delete a node, patch, operation or user

```sh
ichor delete [flags]
```

### Examples

```sh
  ichor delete patch ingress-sysctls
  ichor delete node worker-3 --confirm worker-3
```

### Options

```text
  -h, --help   help for delete
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
* [ichor delete node](ichor_delete_node.md)	 - Delete a node from the cluster: drain it, wipe it (STATE and EPHEMERAL, or more with --wipe-mode), power it off (or reboot it with --reboot) and delete its Kubernetes Node; needs the override permission
* [ichor delete operation](ichor_delete_operation.md)	 - Delete an operation record
* [ichor delete patch](ichor_delete_patch.md)	 - Delete a patch
* [ichor delete user](ichor_delete_user.md)	 - Delete a user (the last enabled admin cannot be deleted)

