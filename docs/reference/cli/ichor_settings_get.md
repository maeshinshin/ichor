---
title: "ichor settings get"
description: "Show each setting, its value in force and where the value comes from (spec or deployment)"
---

## ichor settings get

Show each setting, its value in force and where the value comes from (spec or deployment)

### Synopsis

Show each setting with the value in force and its source: "spec" when the IchorInstallation
sets it, "deployment" when the flag or chart value applies. The default is a table;
-o yaml or -o json print the full response.

```
ichor settings get [flags]
```

### Options

```
  -h, --help   help for get
```

### Options inherited from parent commands

```
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor settings](ichor_settings.md)	 - Show and change Ichor's runtime settings (sign-in lifetimes, default drift policy, update check, automatic patching)

