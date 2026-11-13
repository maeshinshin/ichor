---
title: "ichor backup"
description: "Take a backup now, outside the schedule"
---

## ichor backup

Take a backup now, outside the schedule

```sh
ichor backup [command]
```

### Examples

```sh
  ichor backup etcd nightly
```

### Options

```text
  -h, --help   help for backup
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
* [ichor backup etcd](ichor_backup_etcd.md)	 - Take an etcd snapshot now, outside the schedule

