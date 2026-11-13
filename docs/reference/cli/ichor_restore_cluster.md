---
title: "ichor restore cluster"
description: "Recover a cluster whose control planes and etcd data are gone, from a backup in S3"
---

## ichor restore cluster

Recover a cluster whose control planes and etcd data are gone, from a backup in S3

### Synopsis

restore cluster recovers a cluster whose control planes and
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

```sh
ichor restore cluster [flags]
```

### Examples

```sh
  ichor restore cluster --cluster prod --node-endpoint 192.168.10.11:50000 \
    --s3-endpoint https://s3.example.com --s3-bucket ichor-backups --s3-prefix prod
```

### Options

```text
      --cluster string          name of the cluster (locates ~/.ichor/clusters/<name>/secrets.yaml, required)
  -h, --help                    help for cluster
      --node-endpoint string    Talos API address (host:port) of the new control plane to recover onto (required)
      --s3-bucket string        S3 bucket (required)
      --s3-ca-file string       PEM file with extra root CAs for the S3 endpoint
      --s3-endpoint string      S3 endpoint URL, https://host[:port] (required)
      --s3-prefix string        key prefix the backups were written under (required)
      --s3-region string        S3 region, if the service needs one
      --secrets-bundle string   path to the secrets bundle; default ~/.ichor/clusters/<cluster>/secrets.yaml
      --snapshot string         object name to restore (etcd-<time>.snapshot.age); default the newest one
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

