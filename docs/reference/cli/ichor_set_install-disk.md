---
title: "ichor set install-disk"
description: "Set the install disk of an approved node that has no configuration applied yet"
---

## ichor set install-disk

Set the install disk of an approved node that has no configuration applied yet

### Synopsis

Set spec.install of a node that was approved without an install disk (Ready is False with reason
InstallDiskUnknown). It is what the install-disk dialog of the UI does, through the same API call and so the same checks:
the node must be approved and must not have a configuration applied yet, because an install target only applies
before the first install. The installer image defaults to the one of the node's cluster.

```sh
ichor set install-disk NODE --install-disk DISK [--install-image REF] [flags]
```

### Examples

```sh
  ichor set install-disk 3f1c2a --install-disk /dev/nvme0n1
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for install-disk
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --install-disk string      system disk Talos installs onto: "wwid:ID", "serial:SERIAL", a /dev/disk/by-id/ path, a bus path such as /pci0000:00/..., or a device path such as /dev/sda
      --install-image string     installer image (default: the cluster's)
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
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

* [ichor set](ichor_set.md)	 - Change one setting: kube-proxy mode, containerd device ownership or Ichor's runtime settings, or a node's install disk

