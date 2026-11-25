# Back up etcd to S3

A `TalosEtcdBackup` snapshots etcd on a schedule, encrypts it with a key derived
from the cluster's secrets bundle, stores it in S3 and keeps the newest few. It
is the only snapshot `ichor restore` reads; nothing is backed up until you
create one. Design: [Backup, recovery, certificates](../design/architecture/backup-recovery-certificates.md).

![Five steps in a row: you create the credentials Secret and the TalosEtcdBackup, the controller snapshots etcd at once and then on schedule, the snapshot is encrypted with a key from the secrets bundle, it is stored in an S3 bucket outside the cluster with the newest keepLast kept, and ichor restore etcd and restore cluster read it.](../images/etcd-backup-flow.light.drawio.svg)

*From the two objects you create to a snapshot a restore can read.*

## What you need

- **A bucket outside the cluster**, which Ichor does not create. A restore takes
  the Kubernetes API down, and a store inside the cluster (`minio.minio.svc`, a
  pod or service address) goes with it. Such an endpoint sets the `StoreInCluster`
  warning condition, and `ichor create etcd-backup` prints it too.
- **An access key and secret** allowed to list, read, write and delete under the
  prefix (delete prunes old snapshots).
- **The `update` verb on `talosetcdbackups/secret-references`**, which gates
  naming the Secret or changing the endpoint, bucket or prefix
  ([Security model](../concepts/security.md)).
- **An `https://` endpoint.** `http://` needs `--s3-insecure`.

## Create it

Credentials go in the environment, not flags (the variables `ichor restore
cluster` reads too):

```sh
export ICHOR_S3_ACCESS_KEY_ID=...
export ICHOR_S3_SECRET_ACCESS_KEY=...
ichor create etcd-backup nightly --cluster prod \
  --s3-endpoint https://s3.example.com --s3-bucket ichor-backups
```

This writes the Secret `nightly-s3` in `--ichor-namespace` (`ichor-system`, where
the controller reads it) and the `TalosEtcdBackup` `nightly` in `--namespace`.
It validates first, never overwrites, and removes the Secret if the backup is refused.

| Flag | Default |
|---|---|
| `--s3-prefix` | the cluster's name (two clusters in one bucket need two) |
| `--schedule` (`spec.schedule`) | `0 * * * *` (five-field cron, UTC) |
| `--keep-last` (`spec.retention.keepLast`) | 24; older snapshots are deleted after a successful upload |
| `--s3-region`, `--s3-ca-file` (kept as `ca.crt`), `--credentials-secret` (`NAME-s3`) | |
| `ICHOR_S3_SESSION_TOKEN` | optional session token |

### By hand

For GitOps, or a cluster you reach with `kubectl` only. The Secret's keys are
`accessKeyID`, `secretAccessKey`, optionally `sessionToken` and `ca.crt`.

```sh
kubectl -n ichor-system create secret generic nightly-s3 \
  --from-literal=accessKeyID=... --from-literal=secretAccessKey=...
```

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosEtcdBackup
metadata:
  name: nightly
  namespace: ichor-system
spec:
  clusterRef:
    name: prod            # a TalosCluster in this namespace; cannot change later
  schedule: "0 * * * *"
  retention:
    keepLast: 24
  destination:
    s3:
      endpoint: https://s3.example.com
      bucket: ichor-backups
      prefix: prod        # no leading or trailing slash
      credentialsSecretRef:
        name: nightly-s3
```

## Check it

```sh
kubectl -n ichor-system get talosetcdbackup nightly -o yaml
```

`ichor create etcd-backup` writes into `--namespace` (default `ichor-system`). A
backup you write by hand lives in the namespace you gave it; use that one here.

- `Ready` is `True` (`Succeeded`) while the newest snapshot is fresher than
  twice the schedule interval; `False` is `Failed` (reason in the message: a
  missing key, no bucket, unreachable endpoint) or `Stale`.
- `status.lastSnapshot` is the newest; `status.snapshots` is every one kept,
  which `ichor restore etcd` offers.

`spec.suspend: true` stops new snapshots; existing ones are kept and not pruned.
The Backups page shows the same. `ichor backup etcd nightly` takes one more
snapshot outside the schedule. A failed backup is retried every five minutes:
fix the Secret or the bucket.

## Restoring

`ichor restore etcd` (the controller reads the snapshot through the same Secret)
and `ichor restore cluster` (needs the bundle and bucket) read these snapshots.
A restore fails at once, with the cause, when the store refuses the credentials
or the bucket or snapshot object is gone; fix that and start it again.

`ichor restore etcd` rewinds a running cluster and loses everything written
since the snapshot. It needs:

- `--override`, and the `override` verb on the TalosCluster;
- the cluster's name typed as confirmation (or `--confirm NAME`); `--yes` does not replace it.

```sh
ichor restore etcd --cluster prod --override
ichor restore etcd --cluster prod --backup nightly \
  --snapshot etcd-20261112T020000Z.snapshot.age --override --follow
```

Without `--snapshot` the newest one is used.

> [!IMPORTANT]
> Keep the bundle: `~/.ichor/clusters/NAME/secrets.yaml`, plus
> `retired-keys.yaml` after a [key rotation](rotate-keys.md). A bucket without
> the bundle cannot be restored from.

## Before a rotation or a risky change

[CA rotation](../concepts/ca-and-key-rotation.md) and other operations that
restart control planes ask for a snapshot first.

| | `ichor backup etcd NAME` (after `create etcd-backup`) | `ichor snapshot etcd --node NODE` |
|---|---|---|
| Setup | a bucket outside the cluster, once | none |
| File | encrypted, in your bucket | **plaintext** on your disk, holding every Secret |
| Read by `ichor restore` | yes | no |

Use the first. The second is a stopgap for a cluster with no backup: it gives
you data, not a restore, and must be kept as carefully as the secrets.
