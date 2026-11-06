---
title: "ichor restore"
description: "Restore etcd from a backup snapshot: into the running cluster (--live) or after losing it (--recover-cluster)"
---

## ichor restore

Restore etcd from a backup snapshot: into the running cluster (--live) or after losing it (--recover-cluster)

### Synopsis

restore --live rewinds a RUNNING cluster to a stored snapshot (disaster recovery after
data corruption or a bad change). It creates an etcd-restore operation; the controller wipes
etcd on every control plane, recovers the snapshot on one, lets the others rejoin and reconciles
every machine again. The Kubernetes API is unavailable meanwhile and EVERYTHING WRITTEN SINCE THE
SNAPSHOT IS LOST. It needs --override (and the override verb on the TalosCluster) and the
cluster's name typed (or --confirm NAME); --yes does not replace the name. Without --snapshot
the newest snapshot of the cluster's backup is used; --backup picks the backup when there are
several. Follow it with --follow.

restore --recover-cluster recovers a cluster whose control planes and
etcd data are gone, using only what survives outside it: the secrets bundle
(~/.ichor/clusters/<cluster>/secrets.yaml, or --secrets-bundle) and the S3
bucket the TalosEtcdBackup wrote to. It does not need Ichor, Kubernetes or
a Secret to be running.

It lists the snapshots under --s3-prefix, decrypts the newest one with the
key derived from the bundle, checks its integrity, and only then uploads it
to ONE freshly-installed control plane (--node-endpoint) and bootstraps etcd
from it, the same as 'talosctl bootstrap --recover-from'. That node must
already run Talos with a machine configuration made from the same secrets
bundle, and its etcd data directory must be empty. THE CLUSTER'S STATE
REVERTS TO THE SNAPSHOT'S TIME. Other control planes then join on their own.

S3 credentials come from ICHOR_S3_ACCESS_KEY_ID, ICHOR_S3_SECRET_ACCESS_KEY
and optionally ICHOR_S3_SESSION_TOKEN (falling back to the AWS_* variables).
They are never accepted as flags, so they do not show in 'ps'.

Without --snapshot the newest usable snapshot is used; one that fails to
decrypt or whose checksum does not match is skipped and reported. With
--snapshot, that snapshot or nothing.

With --live the snapshot is read and decrypted by the controller; no secrets bundle or S3
credentials are needed here.

```sh
ichor restore (--live | --recover-cluster) [flags]
```

### Options

```text
      --backup string            with --live: the TalosEtcdBackup to restore from (default the cluster's only one)
      --ca-file string           PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cluster string           name of the cluster (--recover-cluster: locates ~/.ichor/clusters/<name>/secrets.yaml, required; --live: default the only one)
      --confirm string           with --live: the cluster's name, to go on without being asked to type it
      --follow                   with --live: follow the operation until it finishes
  -h, --help                     help for restore
      --ichor-namespace string   namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --live                     restore into the running cluster: the controller wipes etcd on every control plane and recovers a stored snapshot (ADR 0089)
      --local                    do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string         namespace of the Ichor resources the command names (default "ichor-system")
      --node-endpoint string     Talos API address (host:port) of the new control plane to recover onto (required)
      --override                 incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --recover-cluster          full-loss recovery: bootstrap one new control plane from a snapshot
      --s3-bucket string         S3 bucket (required)
      --s3-ca-file string        PEM file with extra root CAs for the S3 endpoint
      --s3-endpoint string       S3 endpoint URL, https://host[:port] (required)
      --s3-prefix string         key prefix the backups were written under (required)
      --s3-region string         S3 region, if the service needs one
      --secrets-bundle string    path to the secrets bundle; default ~/.ichor/clusters/<cluster>/secrets.yaml
      --snapshot string          object name to restore (etcd-<time>.snapshot.age); default the newest one
      --tls-server-name string   name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle (ADR 0127); any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs

