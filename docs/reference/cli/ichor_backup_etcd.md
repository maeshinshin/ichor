---
title: "ichor backup etcd"
description: "Take an etcd snapshot now, outside the schedule"
---

## ichor backup etcd

Take an etcd snapshot now, outside the schedule

### Synopsis

backup etcd asks the controller of the TalosEtcdBackup NAME for one snapshot outside its schedule. It only sets an annotation on the TalosEtcdBackup; the controller takes, verifies,
encrypts and stores the snapshot, prunes to the retention, and records the request in
status.lastManualBackup. A suspended backup, or one with a request the controller has not started
yet, is refused. Check the result with 'ichor restore etcd' (it lists the snapshots) or the
Backups page.

```sh
ichor backup etcd NAME [flags]
```

### Examples

```sh
  ichor backup etcd nightly
```

### Options

```text
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
  -h, --help                     help for etcd
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
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

* [ichor backup](ichor_backup.md)	 - Take a backup now, outside the schedule

