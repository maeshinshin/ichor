---
title: "ichor export"
description: "Save a cluster's secrets bundle locally"
---

## ichor export

Save a cluster's secrets bundle locally

```sh
ichor export [flags]
```

### Examples

```sh
  ichor export bundle prod
```

### Options

```text
  -h, --help   help for export
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
* [ichor export bundle](ichor_export_bundle.md)	 - Save the cluster's current secrets bundle locally and acknowledge it after a CA rotation

