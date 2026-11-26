---
title: "ichor create etcd-backup"
description: "Create a scheduled, encrypted etcd backup to an S3 bucket: the TalosEtcdBackup and its credentials Secret"
---

## ichor create etcd-backup

Create a scheduled, encrypted etcd backup to an S3 bucket: the TalosEtcdBackup and its credentials Secret

### Synopsis

etcd-backup writes the two objects a scheduled etcd backup needs: a Secret with the S3 credentials, in
the controller's namespace (--ichor-namespace, where the controller reads it), and the TalosEtcdBackup
NAME in --namespace that names it. It does not create the bucket: the bucket must already exist, and
should be outside the cluster, because a restore takes the cluster's own Kubernetes API down and an
in-cluster store with it.

The S3 credentials are never accepted as flags, so they do not show in 'ps' or the shell history.
Set ICHOR_S3_ACCESS_KEY_ID and ICHOR_S3_SECRET_ACCESS_KEY, and optionally ICHOR_S3_SESSION_TOKEN
(falling back to the AWS_* variables), as 'ichor restore cluster' does. --s3-ca-file adds a CA bundle
that signs the endpoint to the Secret.

Snapshots are taken on --schedule (a five-field cron expression in UTC, default every hour), always
encrypted with a key derived from the cluster's secrets bundle, and the newest --keep-last (default 24)
are kept. The prefix defaults to the cluster's name; two clusters need two prefixes.

Setting the credentials Secret and the destination needs the "update" verb on
talosetcdbackups/secret-references. An existing Secret or TalosEtcdBackup of that name is never
overwritten. The controller takes the first snapshot as soon as it sees the object.

```sh
ichor create etcd-backup NAME --cluster CLUSTER --s3-endpoint URL --s3-bucket BUCKET [flags]
```

### Examples

```sh
  export ICHOR_S3_ACCESS_KEY_ID=... ICHOR_S3_SECRET_ACCESS_KEY=...
  ichor create etcd-backup nightly --cluster prod --s3-endpoint https://s3.example.com --s3-bucket ichor-backups
  ichor create etcd-backup nightly --cluster prod --s3-endpoint https://s3.example.com --s3-bucket ichor-backups \
    --s3-prefix prod/etcd --schedule '0 2 * * *' --keep-last 14
```

### Options

```text
      --ca-file string              PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --cluster string              name of the TalosCluster to back up (required)
      --credentials-secret string   name of the credentials Secret (default NAME-s3)
  -h, --help                        help for etcd-backup
      --ichor-namespace string      namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --keep-last int32             how many snapshots to keep (default 24)
      --local                       do not use ichor-ui; compute from the Kubernetes API directly
      --override                    incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --s3-bucket string            S3 bucket, which must already exist (required)
      --s3-ca-file string           PEM file with the CA that signs the S3 endpoint, kept in the Secret as ca.crt
      --s3-endpoint string          S3 endpoint URL, https://host[:port] (required)
      --s3-insecure                 allow a plain http:// endpoint: the access key id then crosses the network in clear
      --s3-prefix string            key prefix snapshots are written under (default the cluster's name)
      --s3-region string            S3 region, if the service needs one
      --schedule string             five-field cron expression in UTC (default every hour, 0 * * * *)
      --tls-server-name string      name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
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

* [ichor create](ichor_create.md)	 - Create a user or an etcd backup, or write credentials for talosctl and kubectl

