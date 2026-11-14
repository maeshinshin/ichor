# 0047. TalosEtcdBackup: verified, bundle-keyed age snapshots in S3

Status: Accepted · Date: 2026-10-26 · Design section: Backup, recovery, certificates

Decided: `TalosEtcdBackup` snapshots one healthy non-leader control plane over Talos's `EtcdSnapshot` stream, checks the etcd sha256 trailer, encrypts with age using a key derived from the secrets bundle, and stores `<prefix>/etcd-<UTC>.snapshot.age` in S3 via minio-go; restore reads exactly that

## Context

Goal 4 of the design is "restore from the secrets bundle and a snapshot alone".
No type, controller or storage format existed. This ADR covers the backup half.
Restore (live and `--recover-cluster`) is separate, so the stored format below
is written for it and lives in `internal/etcdbackup`, which
both sides import so they cannot drift.

## Decisions

1. **How a snapshot is taken.** Talos's `MachineService.EtcdSnapshot` streams the
   bbolt database from the node's local etcd member
   (`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go`,
   `Server.EtcdSnapshot`, Talos 1.14.3: it opens `etcd.NewLocalClient` and calls
   `Snapshot`). It is control-plane only (`CheckControlplane`) and any one member
   suffices, since a snapshot is one consistent MVCC read. The controller asks
   every approved control plane for `EtcdStatus` and tries healthy followers
   first (no errors, not a learner, `member_id != leader`), then healthy leaders,
   stopping at the first that yields a verified snapshot. A single-control-plane
   cluster snapshots its leader. Machinery's `Client.EtcdSnapshot` and
   `Client.EtcdStatus` are used; `talosctl etcd snapshot` is the reference.
2. **Verification.** etcd's snapshot is the bbolt file followed by the sha256 of
   it. `talosctl` checks only `size % 512 == 32`; `etcdbackup.Verifier` also checks
   the hash while streaming. It does not open the database (that needs etcd's
   packages); it proves the stream was complete and intact. A snapshot that fails is
   never uploaded: the encrypted stream is staged to a spool file (an `emptyDir`;
   the root filesystem is read-only), uploaded only after verification, and the next
   control plane is tried.
3. **S3 client: `minio-go/v7`.** `go.mod` has no S3-capable dependency (no aws
   SDK, no minio, no gocloud), so any choice adds one.
   `aws-sdk-go-v2` pulls in dozens of modules; minio-go adds nine small ones,
   speaks SigV4 against AWS, MinIO, Ceph and others, and supports a custom CA.
4. **Encryption: age with a scrypt recipient, passphrase derived from the bundle.**
   Snapshots contain Secrets, so they are always encrypted; there is nothing to
   configure. The key is `hex(HKDF-SHA256(secret = bundle.Cluster.Secret,
   salt = bundle.Cluster.ID, info = "ichor.maesh.dev/etcd-backup/v1"))`. It is
   recomputable from the bundle and stored nowhere, so (a) the bucket alone
   reveals nothing, and (b) "bundle + snapshot" really is sufficient, with no
   second key to lose. A key stored with the backups would make encryption
   pointless; a key held separately would add a thing recovery depends on. The cost is
   that the bundle is the root of trust for backups, which it already is for the
   cluster. age gives streaming authenticated encryption without us writing
   crypto; the file also decrypts with the plain `age` CLI given the passphrase.
   Changing `hkdfInfo` or the derivation orphans every existing backup, so it is
   part of the format. `bundle.Cluster.Secret` is not rotated by Talos CA rotation.
5. **Credentials.** `spec.destination.s3.credentialsSecretRef` names a Secret with
   `accessKeyID`, `secretAccessKey`, optional `sessionToken` and `ca.crt`. It is read
   from the **controller's own namespace** (`POD_NAMESPACE`), never the backup's
   (ADR 0011), the same rule as `loadBundle` and `render.ResolveRegistryAuth`. Errors name the Secret
   and key, not values. Every error string is passed through a scrubber holding the
   credentials, the derived passphrase and the bundle's secret scalars before it
   reaches status or a log, because S3 and Talos error text is not ours.
6. **Git.** The spec holds only references, so it is safe to commit and the design
   lists `TalosEtcdBackup` as Git-tracked. Status holds object key, time, size and
   machine name only. **Follow-up:** ADR 0028's `ichor gitops export` allow-list
   (`internal/cli`) does not include this kind, so an exported repository misses
   it; adding it is not part of this ADR.
7. **No cluster lock.** A snapshot is a read of one member; it does not stop, drain
   or restart anything. The lock exists for disruptive operations
   (`acquireDisruptiveLock` is the only `Acquire` caller). A node mid-upgrade fails its snapshot and the next is tried. Restore,
   which does stop etcd, must take the lock.

## Stored format (what restore reads)

- Key: `<spec.destination.s3.prefix>/etcd-<YYYYMMDDTHHMMSSZ>.snapshot.age`, UTC.
  Sorting the names sorts by time. Objects under the prefix not matching the
  pattern are never listed, restored or pruned. Use one prefix per cluster.
- Content: an age file (scrypt recipient) whose plaintext is **exactly what
  `EtcdSnapshot` streamed**: the bbolt file plus the 32-byte sha256 trailer, i.e. the
  input of `talosctl bootstrap --recover-from` / `Client.EtcdRecover`. Restore
  decrypts, may re-run `etcdbackup.Verifier`, and feeds it to Talos unchanged.
- To restore: read the bundle, `etcdbackup.Passphrase(bundle)`, `Store.List`
  (newest first), `Store.Get`, `Decrypt`. Nothing else is needed.
- Retention: after each successful upload, keep the newest `retention.keepLast`
  (default 24) and delete the rest. Pruning errors are reported but do not fail the
  backup. A suspended backup prunes nothing.

## Schedule, status, metrics

`spec.schedule` is a five-field cron in UTC (robfig/cron/v3, no dependencies),
default hourly. Due-ness is computed from `status.lastAttemptTime`, so a restarted
controller resumes; after a failure it retries every 5 minutes rather than waiting for
the next tick. `Ready` is True after success, False after a failure or when the
newest success is older than 2.5 schedule intervals (`Stale`). Metrics:
`ichor_etcd_backup_last_success_timestamp_seconds` and
`ichor_etcd_backup_failures_total`, labelled by namespace and name; alert rules over
them belong with the chart's monitoring option.

## Deferred

- `shortcut:` only S3, no GCS/Azure/local; path-style vs virtual-host is minio-go's
  auto-detection with no override field. Add a destination type when a user needs another store.
- `shortcut:` no deep verification (opening the bbolt file); a periodic restore test in
  e2e is the real proof, per the design.
- Snapshots are staged on local disk (one snapshot of space in the `emptyDir`); a
  streaming multipart upload would avoid that but cannot un-upload a bad snapshot.
- The chart's `manager-role` carries these rules.

- Amendment 2026-10-27: snapshots are sealed with scrypt work factor 2^10 (1 MiB) instead of age's default 2^18 (~270 MiB of heap per derivation, which OOM-killed the controller); the key is a 256-bit HKDF output so the cost bought nothing. Decrypt caps at the same 2^10. There is no compatibility with pre-release backups sealed with 2^18, so the legacy decrypt path is dropped. The manager memory limit is 256Mi (request 128Mi): normal use is tens of MiB plus ~100 MiB of caches, and no legacy derivation needs covering.
- Amendment 2026-10-28: a backup can be taken on demand. `ichor backup etcd NAME`, the Backups page's "Back up now" button and the `OperationService.RequestEtcdBackup` RPC set the annotation `ichor.maesh.dev/backup-now=<RFC 3339 time>` on the `TalosEtcdBackup` as the caller (same RBAC as the other operation writes: `update` on `talosetcdbackups`), so neither UI nor CLI touches Talos. The controller, when that time is newer than the new `status.lastManualBackup`, records it there and takes one snapshot in the same reconcile, outside the schedule, through the same path as a scheduled one (same key format, verification, encryption, pruning); the schedule then counts from that snapshot. A request is acted on once, whether the snapshot succeeded or not: a failure shows in `Ready` and is retried on the 5-minute failure interval like any failed attempt. A missing, unparsable or not-newer annotation is ignored, and a suspended backup ignores it. The pattern is ADR 0095's retry annotation. The RPC returns `FailedPrecondition` for a suspended backup and for a request still waiting, and writes a time never earlier than `lastManualBackup` plus one second. `EtcdBackup` in the API now also carries the destination (endpoint, region, bucket, prefix; never the credentials Secret), `keepLast`, the last success time, the last error (the Ready condition's message while it is Failed), `suspended`, `lastManualBackup` and whether a request is pending, and the Backups page shows them. The controller now also reconciles on annotation changes, not only on spec generation changes.
