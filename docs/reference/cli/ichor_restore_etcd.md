---
title: "ichor restore etcd"
description: "Rewind a running cluster to a stored etcd snapshot"
---

## ichor restore etcd

Rewind a running cluster to a stored etcd snapshot

### Synopsis

restore etcd rewinds a RUNNING cluster to a stored snapshot (disaster recovery after
data corruption or a bad change). It creates an etcd-restore operation; the controller wipes
etcd on every control plane, recovers the snapshot on one, lets the others rejoin and reconciles
every node again. The Kubernetes API is unavailable meanwhile and EVERYTHING WRITTEN SINCE THE
SNAPSHOT IS LOST. It needs --override (and the override verb on the TalosCluster) and the
cluster's name typed (or --confirm NAME); --yes does not replace the name. Without --snapshot
the newest snapshot of the cluster's backup is used; --backup picks the backup when there are
several. Follow it with --follow.

The snapshot is read and decrypted by the controller; no secrets bundle or S3 credentials are
needed here.

```sh
ichor restore etcd [flags]
```

### Examples

```sh
  ichor restore etcd --cluster prod --override
  ichor restore etcd --cluster prod --backup nightly --snapshot etcd-20261112T020000Z.snapshot.age --override --follow
```

### Options

```text
      --backup string     the TalosEtcdBackup to restore from (default the cluster's only one)
      --cluster string    name of the cluster (default the only one)
      --confirm string    the cluster's name, to go on without being asked to type it
      --follow            follow the operation until it finishes
  -h, --help              help for etcd
      --snapshot string   object name to restore (etcd-<time>.snapshot.age); default the newest one
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --ichor-namespace string          namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
      --local                           do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string                namespace of the Ichor resources the command names (default "ichor-system")
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --override                        incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --tls-server-name string          name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor restore](ichor_restore.md)	 - Restore etcd from a backup: into the running cluster (restore etcd) or after losing it (restore cluster)

