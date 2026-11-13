---
title: "ichor edit"
description: "Edit an object in $EDITOR, or change a user's fields"
---

## ichor edit

Edit an object in $EDITOR, or change a user's fields

```sh
ichor edit [flags]
```

### Examples

```sh
  ichor edit cni-values
  ichor edit user alice --role admin
```

### Options

```text
  -h, --help   help for edit
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
* [ichor edit cni-values](ichor_edit_cni-values.md)	 - Edit your layer of Cilium's values in $EDITOR, validate it, show the diff and save
* [ichor edit user](ichor_edit_user.md)	 - Change a user's display name, e-mail, roles or disabled flag (only the flags you pass)

