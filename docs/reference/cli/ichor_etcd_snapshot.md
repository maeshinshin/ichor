---
title: "ichor etcd snapshot"
description: "Download an etcd snapshot from a control plane node"
---

## ichor etcd snapshot

Download an etcd snapshot from a control plane node

### Synopsis

snapshot downloads an etcd snapshot taken from one control plane node. THE SNAPSHOT CONTAINS
EVERY KUBERNETES SECRET in the cluster; keep it as you would the secrets themselves. For scheduled,
encrypted backups use a TalosEtcdBackup instead. The file is plaintext (not encrypted). Needs the "download" verb on
talosmachines/etcd-snapshot. The file is written with mode 0600; -o - writes to standard output.

```sh
ichor etcd snapshot --node CONTROL_PLANE [flags]
```

### Options

```text
  -h, --help            help for snapshot
      --node string     control plane TalosMachine to take the snapshot from
  -o, --output string   file to write (mode 0600); default the name the server suggests; - for standard output
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --ichor-namespace string          namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
      --local                           do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string                namespace of the Ichor resources the command names (default "ichor-system")
      --override                        incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --tls-server-name string          name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor etcd](ichor_etcd.md)	 - Download an etcd snapshot from, or run an etcd maintenance action on, a control plane node

