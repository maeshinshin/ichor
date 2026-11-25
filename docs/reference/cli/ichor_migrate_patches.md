---
title: "ichor migrate patches"
description: "Move TalosConfigPatch spec.documents to spec.config"
---

## ichor migrate patches

Move TalosConfigPatch spec.documents to spec.config

### Synopsis

Release 0.3 retired TalosConfigPatch spec.documents in favour of spec.config. A stored patch that still sets
spec.documents keeps rendering as it did, so no node changes, and this command rewrites it: spec.config gets the same
settings and spec.documents is cleared, in one update. ichor upgrade ichor does the same as its last step, and helm
upgrade does it in a post-upgrade hook Job; run this to do it again by hand.

A patch with a setting spec.config has no place for is left as it is, listed with the setting, and gets the condition
Migrated=False. Write those settings in spec.config by hand and clear spec.documents. Running the command again
changes nothing that is already migrated.

```sh
ichor migrate patches [flags]
```

### Examples

```sh
  ichor migrate patches
  ichor migrate patches --namespace ichor-system
```

### Options

```text
  -h, --help               help for patches
  -n, --namespace string   only the patches of this namespace (default: all)
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

* [ichor migrate](ichor_migrate.md)	 - Move stored objects to the current schema

