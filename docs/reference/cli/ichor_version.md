---
title: "ichor version"
description: "Print the CLI version and the installed Ichor version"
---

## ichor version

Print the CLI version and the installed Ichor version

### Synopsis

Print this CLI's version and, when the cluster is reachable, the version of Ichor
installed there (IchorInstallation status.installedVersion, plus spec.version when
it differs, which means an upgrade is pending). When the cluster cannot be
reached, the CLI version is printed with a note on stderr and the exit code is 0.
--client skips the cluster.

```
ichor version [flags]
```

### Options

```
      --client   print only the CLI version; do not contact the cluster
  -h, --help     help for version
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

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs

