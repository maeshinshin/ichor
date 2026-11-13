---
title: "ichor get settings"
description: "Show each setting, its value in force and where the value comes from (spec or deployment)"
---

## ichor get settings

Show each setting, its value in force and where the value comes from (spec or deployment)

### Synopsis

Show Ichor's runtime settings (sign-in lifetimes, default drift policy, update check, automatic patching): each setting with the value in force and its source: "spec" when the IchorInstallation
sets it, "deployment" when the flag or chart value applies. The default is a table;
-o yaml or -o json print the full response.

```sh
ichor get settings [flags]
```

### Examples

```sh
  ichor get settings
  ichor get settings -o yaml
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

* [ichor get](ichor_get.md)	 - List objects, or show a cluster in a few lines

