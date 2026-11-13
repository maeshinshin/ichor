---
title: "ichor set settings"
description: "Change settings (see --help for the keys)"
---

## ichor set settings

Change settings (see --help for the keys)

### Synopsis

Change one or more settings; only the keys you name are written.

  users.tokenTTL=15m           token lifetime, 1m to 1h
  users.refreshLifetime=168h  refresh lifetime, 1h to 720h
  defaults.driftPolicy=warnOnly|autoCorrect   default for a new TalosCluster
  updateCheck.enabled=true|false    look for newer releases
  autoPatch.enabled=true|false    apply patch releases of the running minor by itself
  autoPatch.components=talos,cilium,kubernetes   which components (empty: all)
  autoPatch.window=02:00-05:00   daily UTC window an automatic upgrade may start in (empty: any time)
  autoPatch.acknowledgeHalt=NAMESPACE/NAME   resume after an automatic operation halted auto-patching

reclaim.enabled is removed: who may reset and add a node is decided by RBAC alone.

```sh
ichor set settings KEY=VALUE... [flags]
```

### Examples

```sh
  ichor set settings updateCheck.enabled=false
  ichor set settings autoPatch.enabled=true autoPatch.window=02:00-05:00
```

### Options

```text
  -h, --help   help for settings
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

* [ichor set](ichor_set.md)	 - Change one setting: kube-proxy mode or Ichor's runtime settings

