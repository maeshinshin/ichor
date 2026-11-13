---
title: "ichor bootstrap"
description: "Install Ichor's Cilium on a cluster with no CNI, before helm install ichor"
---

## ichor bootstrap

Install Ichor's Cilium on a cluster with no CNI, before helm install ichor

```sh
ichor bootstrap [command]
```

### Examples

```sh
  ichor bootstrap cni --kube-proxy replaced
```

### Options

```text
  -h, --help   help for bootstrap
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
* [ichor bootstrap cni](ichor_bootstrap_cni.md)	 - Install Ichor's Cilium on a cluster with no CNI, before helm install ichor

