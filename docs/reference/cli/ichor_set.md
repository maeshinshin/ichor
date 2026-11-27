---
title: "ichor set"
description: "Change one setting: kube-proxy mode, containerd device ownership or Ichor's runtime settings, or a node's install disk"
---

## ichor set

Change one setting: kube-proxy mode, containerd device ownership or Ichor's runtime settings, or a node's install disk

```sh
ichor set [command]
```

### Examples

```sh
  ichor set kube-proxy replaced --cluster prod
  ichor set settings updateCheck.enabled=false
```

### Options

```text
  -h, --help   help for set
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
* [ichor set containerd-device-ownership](ichor_set_containerd-device-ownership.md)	 - Let containerd give a pod's block devices to its user and group, so non-root pods can use volumeMode: Block volumes
* [ichor set install-disk](ichor_set_install-disk.md)	 - Set the install disk of an approved node that has no configuration applied yet
* [ichor set kube-proxy](ichor_set_kube-proxy.md)	 - Switch kube-proxy between enabled and replaced by the CNI
* [ichor set settings](ichor_set_settings.md)	 - Change settings (see --help for the keys)

