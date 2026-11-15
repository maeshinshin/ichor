# Back up etcd to S3

A `TalosEtcdBackup` takes an etcd snapshot on a schedule, encrypts it with a key
derived from the cluster's secrets bundle, stores it in an S3 bucket and keeps
the newest few. It is the only snapshot `ichor restore` reads, and nothing is
backed up until you create one. This page sets one up. The design is in
[Backup, recovery, certificates](../design/architecture/backup-recovery-certificates.md).

Ichor does not create the bucket. You bring a bucket and credentials; Ichor
writes the two objects that use them.

## What you need

- **A bucket that already exists, outside the cluster.** A restore takes the
  cluster's Kubernetes API down, and a store inside the cluster (a Service such
  as `minio.minio.svc`, or an address in the pod or service range) goes with it,
  so the restore cannot finish. The `TalosEtcdBackup` carries a `StoreInCluster`
  warning condition while its endpoint looks like that, and `ichor create etcd-backup` prints
  the same warning.
- **An access key and secret for it**, allowed to list, read, write and delete
  objects under the prefix. Delete is how old snapshots are pruned.
- **The `update` verb on `talosetcdbackups/secret-references`.** The credentials
  Secret is read with Ichor's own rights, so naming it, or changing the endpoint,
  bucket or prefix, is gated like the other Secret references
  ([Security model](../concepts/security.md)).
- **An endpoint that is `https://`.** A plain `http://` endpoint sends the access
  key id in clear and is refused unless you also say `--s3-insecure`.

## Create it

Give the credentials in the environment, not as flags, so they do not show in
`ps` or the shell history. These are the variables `ichor restore cluster`
reads too:

```sh
export ICHOR_S3_ACCESS_KEY_ID=...
export ICHOR_S3_SECRET_ACCESS_KEY=...
ichor create etcd-backup nightly --cluster prod \
  --s3-endpoint https://s3.example.com --s3-bucket ichor-backups
```

This writes a Secret named `nightly-s3` in the namespace Ichor is installed in
(`--ichor-namespace`, `ichor-system` by default: the controller reads it from
there, not from the namespace of the backup) and a `TalosEtcdBackup` named
`nightly` in `--namespace`. It refuses an endpoint, bucket or prefix the API
would refuse before it writes anything, never overwrites a Secret or a backup that
exists, and removes the Secret again if the `TalosEtcdBackup` is refused.

Optional flags: `--s3-prefix` (default the cluster's name; two clusters in one bucket need two prefixes),
`--s3-region`, `--s3-ca-file` (a PEM bundle that signs the endpoint, kept in the Secret as `ca.crt`),
`--schedule` (a five-field cron expression in UTC, default `0 * * * *`), `--keep-last` (default 24)
and `--credentials-secret` (default `NAME-s3`). `ICHOR_S3_SESSION_TOKEN` adds a session token.

### By hand

The same two objects, for a GitOps repository or a cluster you reach with `kubectl` only. The
Secret goes in the controller's namespace; its keys are `accessKeyID` and `secretAccessKey`,
and optionally `sessionToken` and `ca.crt`.

```sh
kubectl -n ichor-system create secret generic nightly-s3 \
  --from-literal=accessKeyID=... --from-literal=secretAccessKey=...
```

```yaml
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosEtcdBackup
metadata:
  name: nightly
  namespace: default
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

## See that it works

The controller takes the first snapshot as soon as it sees the object, then on
the schedule. Look at the object:

```sh
kubectl -n default get talosetcdbackup nightly -o yaml
```

- `status.conditions`, type `Ready`: `True` with reason `Succeeded` while the
  newest snapshot is fresher than twice the schedule interval. `False` with
  `Failed` carries the reason in its message (a missing key in the Secret, a
  bucket that does not exist, an endpoint that cannot be reached); `Stale` means
  the last success is too old. A `StoreInCluster` condition is the warning above.
- `status.lastSnapshot`: the key, time, size and source machine of the newest
  snapshot. `status.snapshots` lists every snapshot kept, which is what
  `ichor restore etcd` offers.

The Backups page of the UI shows the same, and `ichor backup etcd nightly` asks for one more snapshot
outside the schedule.

If it is `Failed`, fix the Secret or the bucket; the controller retries every five minutes.
Changing the credentials is an edit of the Secret. Pointing the object at another
Secret, endpoint, bucket or prefix needs the `update` verb on `talosetcdbackups/secret-references` again.

## Keep these

Snapshots are encrypted with a key derived from the cluster's secrets bundle.
`ichor restore cluster` needs the bundle (`~/.ichor/clusters/NAME/secrets.yaml`,
and `retired-keys.yaml` after a [key rotation](rotate-keys.md)) and the bucket,
and nothing else that survives the loss of the cluster. A bucket without the bundle cannot be restored from.

## Before a rotation or a risky change

[CA rotation](../concepts/ca-and-key-rotation.md) and the other operations that
restart control planes ask you to hold a snapshot first. There are two routes:

| | `ichor create etcd-backup`, then `ichor backup etcd NAME` | `ichor snapshot etcd --node NODE` |
|---|---|---|
| Setup | a bucket outside the cluster, once | none |
| The file | encrypted, in your bucket | **plaintext**, on your disk: it holds every Kubernetes Secret |
| `ichor restore etcd` and `restore cluster` read it | yes | no |
| Gets you | a snapshot Ichor can restore, kept under the retention | a copy you hold yourself |

Use the first route. The second is a stopgap for a cluster that has no backup yet:
`ichor restore` does not take the file, so it gives you the data and not a restore
Ichor performs for you, and it must be kept as carefully as the secrets themselves.
