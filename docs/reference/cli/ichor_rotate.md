---
title: "ichor rotate"
description: "Rotate a cluster's certificate authorities or its signing and encryption keys"
---

## ichor rotate

Rotate a cluster's certificate authorities or its signing and encryption keys

### Examples

```sh
  ichor rotate ca prod
  ichor rotate keys prod
```

### Options

```text
  -h, --help   help for rotate
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
* [ichor rotate ca](ichor_rotate_ca.md)	 - Replace the cluster's certificate authorities (Talos API, Kubernetes API, and optionally etcd and aggregator)
* [ichor rotate keys](ichor_rotate_keys.md)	 - Replace the cluster's service-account signing key and secretbox (encryption at rest) key

