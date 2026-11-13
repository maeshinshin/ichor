---
title: "ichor diff"
description: "Show how a change would alter what is in force"
---

## ichor diff

Show how a change would alter what is in force

### Synopsis

diff shows how the effective Cilium values would change if a file became your layer, without writing anything.

```sh
ichor diff [command]
```

### Examples

```sh
  ichor diff cni-values -f cilium-values.yaml
```

### Options

```text
  -h, --help   help for diff
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
* [ichor diff cni-values](ichor_diff_cni-values.md)	 - Show how the effective values would change if FILE became your layer

