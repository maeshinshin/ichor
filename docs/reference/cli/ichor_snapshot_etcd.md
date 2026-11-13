---
title: "ichor snapshot etcd"
description: "Download an etcd snapshot from a control plane node"
---

## ichor snapshot etcd

Download an etcd snapshot from a control plane node

### Synopsis

snapshot etcd downloads an etcd snapshot taken from one control plane node. THE SNAPSHOT CONTAINS
EVERY KUBERNETES SECRET in the cluster; keep it as you would the secrets themselves. For scheduled,
encrypted backups use a TalosEtcdBackup instead. The file is plaintext (not encrypted). Needs the "download" verb on
talosmachines/etcd-snapshot. The file is written with mode 0600; --out - writes to standard output.

```sh
ichor snapshot etcd --node CONTROL_PLANE [flags]
```

### Examples

```sh
  ichor snapshot etcd --node cp-1
  ichor snapshot etcd --node cp-1 --out etcd.snapshot
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for etcd
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --node string              control plane TalosMachine to take the snapshot from
      --out string               file to write (mode 0600); default the name the server suggests; - for standard output
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

* [ichor snapshot](ichor_snapshot.md)	 - Download a snapshot from a node

