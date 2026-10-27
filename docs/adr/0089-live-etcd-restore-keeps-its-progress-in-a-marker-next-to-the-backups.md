# 0089. A live etcd restore is a controller-run operation whose progress outlives the API in a marker next to the backups

Status: Accepted · Date: 2026-10-27 · Design section: Backup, recovery, certificates; Upgrades (status-held state machine), Trust

Decided: restoring a stored etcd snapshot into a running cluster is a new `TalosOperation` kind `etcd-restore` with `spec.etcdRestore {backupRef, snapshot, confirm, reason}` (`confirm` must equal the cluster name, `reason` is the break-glass reason; CEL and the break-glass webhook enforce both, and the verb is checked on the `TalosCluster`); the controller takes the cluster lock, decrypts and checksums the snapshot and keeps a pinned ciphertext copy of it in the bucket, writes a marker object next to the backups, wipes etcd on every control plane at once (Talos's own disaster-recovery procedure: non-graceful reset with reboot of EPHEMERAL), uploads the snapshot to one control plane and bootstraps it with `RecoverEtcd`, waits for the others to rejoin and the API to answer, asks every `TalosMachine` to reconcile again and waits until each is Ready; because the rollback deletes the operation itself, the progress that must survive lives in the marker, the wipe-and-recover runs as a job independent of the manager, and the operation is recreated from the marker; `ichor restore --live` and the cluster page's "Restore from backup…" create the CR

## Context

ADR 0053 built `ichor restore --recover-cluster` (full loss, from a bundle and a snapshot, no Kubernetes) and listed
the live-cluster restore as not built: "the lock, stopping etcd on every control plane, `EtcdRecover` plus
`Bootstrap{RecoverEtcd}` on one and the others re-joining, and a UI RPC; it should reuse `fetchVerified`". The
design says restoring into a live cluster stops etcd on all control planes and restores the snapshot, in the UI and
the CLI, and that the controller then re-reconciles every node and waits.

Three things make this different from every other operation (ADR 0026, 0068, 0085):

1. **The operation deletes itself.** Restoring etcd rewinds the Kubernetes API to the snapshot. A `TalosOperation`
   created after the snapshot is not in it. "Progress lives in status" (principle 7) cannot hold: after the rollback
   there is no status.
2. **The API goes away in the middle.** Etcd is wiped on every control plane, so for minutes the controller cannot
   read or write any object, including the cluster lock (a Lease) and its own status.
3. **The controller's own manager stops.** Leader election needs the API. Losing the lease stops the manager and,
   in `main`, the process; the Pod would restart into a manager that cannot start without an API. If the wipe is
   done and the recovery is not, nothing is left to do the recovery.

## Talos procedure (checked in Talos v1.14.3)

Source read: `internal/integration/api/etcd-recover.go` (`EtcdRecoverSuite.TestSnapshotRecover`),
`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go` (`Reset`, `Bootstrap`, `EtcdRecover`),
`internal/app/machined/pkg/system/services/etcd.go` (`argsForControlPlane`, `recoverFromSnapshot`,
`buildInitialCluster`), `pkg/machinery/config/config/volume.go` (`PromotableSystemVolumeNames`). The website
documentation itself is not in the module cache; the integration test is the executable form of the documented
procedure.

- The test takes a snapshot, then wipes every control plane with `Reset{Reboot: true, Graceful: false,
  SystemPartitionsToWipe: EPHEMERAL + every promotable system volume (ETCD, CRI, KUBELET, LOG) that is a dedicated
  partition}`, checks `/var/lib/etcd/member` is gone on each (a `List` that answers "no such file or directory"),
  then on one node `EtcdRecover` (retried on `FailedPrecondition`: the etcd service is not waiting yet) and
  `Bootstrap{RecoverEtcd: true}` (retried on `FailedPrecondition` and `DeadlineExceeded`), and asserts the cluster is
  healthy. STATE is not wiped, so each node keeps its configuration and identity.
- `Bootstrap` needs an empty `/var/lib/etcd` (`AlreadyExists` otherwise), a machine config with an etcd CA, time in
  sync and `cri` healthy. The etcd service then runs `recoverFromSnapshot`, which restores with
  `InitialCluster = {this node}` and removes the uploaded file: the recovered etcd is a **single-member cluster**,
  whatever the snapshot's membership was.
- A control plane that boots with an empty etcd data directory and no bootstrap request does not wait for one: it
  tries `addMember` against the existing etcd by itself (`buildInitialCluster`, retried up to
  `EtcdJoinTimeout`). So the other control planes rejoin automatically once the target is up, **and** a node wiped
  while the others still run rejoins the old cluster and has data again. All control planes must therefore be wiped
  together, which is also why the test wipes them all before recovering.
- `Reset` with `SystemPartitionsToWipe` rejects a directory-backed volume unless the volume it lives on is in the
  same request; wiping EPHEMERAL covers the volumes that are directories under it, and the ones that are dedicated
  partitions are added by looking at their `VolumeStatus` (`Type == partition`), exactly as the test does.

## Decision

**CRD.** `spec.kind=etcd-restore` requires `spec.etcdRestore` and nothing else allows it; it is immutable. `backupRef`
names a `TalosEtcdBackup` of the same cluster, `snapshot` is an object name (`etcd-<time>.snapshot.age`), `confirm`
must equal `spec.clusterRef.name` and `reason` is not empty (all CEL). `status.etcdRestore {step, target,
snapshotTime, requestedAt, reconcileRequest, message}` mirrors the marker; `step` is `Wiping`, `Recovering`,
`Rejoining` or `Reconciling`. `TalosEtcdBackup.status.snapshots` (up to 500, newest first, name, time and size) is
filled after each backup and its prune, so the UI and CLI can offer snapshots without reading the bucket.
`TalosMachine.status.observedReconcileRequest` echoes the `ichor.maesh.dev/reconcile-requested` annotation at the
end of every reconcile.

**Authorization.** The break-glass verb on the `TalosCluster` (ADR 0064, 0065), checked by `CreateOperation` as the
caller with a `SelfSubjectAccessReview` and, for a hand-written CR, by the break-glass admission webhook on CREATE of
`talosoperations` (the controller's own identity is exempt, because it recreates the operation after the rollback).
`CreateOperation` also needs the cluster name typed and a reason; the audit record (`create-break-glass`) and a
Warning Event on the cluster carry the reason; the reason is kept in the spec so the record survives the rollback.
Nothing else is new for the UI backend: it reads `talosetcdbackups` (`get;list`) and creates operations as before.

**Snapshot source.** One snapshot of a `TalosEtcdBackup`, read with that backup's S3 credentials and the key derived
from the cluster's secrets bundle (ADR 0047). `etcdbackup.FetchVerified` (moved from `internal/cli`, which now calls
it) is the recipe; `VerifyObject` runs it without keeping the plaintext, so a snapshot of any size is checked, and
`Reader` streams the plaintext into `EtcdRecover`. An uploaded snapshot has no path today and is not added.

**Sequence** (`internal/controller/etcd_restore.go`, `etcd_restore_tail.go`). Every step before the marker changes
nothing and a failure pauses the operation with "nothing was changed".

1. *begin*, under the cluster lock: the backup belongs to the cluster; no other restore's marker exists; the snapshot
   is listed in the bucket; no CA rotation was created after the snapshot (the restored secrets bundle would not match
   the nodes' certificates and Ichor would lose its access); every approved control plane answers and its etcd data
   can be read; the snapshot decrypts and passes the etcd checksum; a copy of its ciphertext is written under the
   same prefix as `etcd-restore.snapshot.age` (the listing skips it, so retention cannot prune it mid-restore); the
   marker `etcd-restore.marker.json` is written (`Wiping`); the status is written; the job starts.
2. *Wiping*: `Reset` with the EPHEMERAL wipe above, to every control plane that still has etcd data, all in the same
   pass (a node wiped alone would rejoin the old etcd). A node with no etcd data is wiped; one that cannot be reached
   is rebooting. The next piece is not started until none has data.
3. *Recovering*: the marker is moved to `Recovering` **before** the target is touched, then the pinned copy is
   checked once more, uploaded with `EtcdRecover` and bootstrapped with `Bootstrap{RecoverEtcd}`; "not ready" answers
   wait, anything else is final. A target that already has etcd data was bootstrapped; when its etcd answers, the
   marker moves to `Rejoining`. The data directory is the only thing that tells a recovered node from a wiped one,
   and it is read from the node, not remembered.
4. *Rejoining* (the API is expected back; this is the first step that takes the lock again): the operation is looked
   up live; if it is gone the rollback happened and it is recreated from the marker. The other control planes
   rejoin etcd by themselves; the target's member list must name every approved control plane and the API must answer.
   A control plane that has etcd data but is not a member holds the old cluster's data and would never join: it is
   wiped (rate limited), which is also how the controller's own node is handled (below).
5. *Reconciling*: every approved `TalosMachine` gets the `ichor.maesh.dev/reconcile-requested` annotation with the
   operation's UID; the operation waits until each machine's `status.observedReconcileRequest` equals it and its Ready
   condition is True, and names the machines it waits for. A machine whose configuration needs a reboot reports
   `RebootRequired`; the operation says so and pauses at its time limit instead of rebooting anything. Machines apply
   what they would apply anyway (the restored state is their desired state), so the plan says to check each.
6. *Done*: the pinned copy and the marker are deleted (the marker last; if that fails the end is repeated), the lock is
   released, the operation is `Completed`.

Time limits: wipe and recovery 60 minutes from the marker's start, the whole restore 150 minutes; past either, the
operation pauses with the reason. A final error pauses the operation (`failureReason`, also written to the marker)
and is never retried by itself, as everywhere (ADR 0026).

**Resume design.** Where each piece of state lives, and who reads it after each kind of interruption:

| State | Lives in | Survives |
|---|---|---|
| the snapshot to restore | the pinned ciphertext copy and, as the source, the bucket | prune, the rollback |
| how far the destructive part got | the marker (written before each irreversible step); the nodes' etcd data directories | the rollback, any restart of the controller, the API being away |
| the operation (spec, reason) | the marker has it; the CR is recreated from it | the rollback |
| progress after `Rejoining` | `status.etcdRestore` and what the nodes and machines report | restart of the controller (the API is up) |

- *Controller restarts with the API up, at `Wiping` or `Recovering`*: the reconcile reads the marker, sees no job in
  this process and starts one from it; a job is a function of the marker and the nodes, so a repeated step is harmless
  (a node still wiping is asked once more after ten minutes; a recovered target is recognised by its data and
  not uploaded to again).
- *The API goes away and the manager stops* (leader election lost): `main` does not exit while a restore job runs. It
  waits (up to 70 minutes) for the job, which needs nothing from Kubernetes (the Talos dialers, the object store and
  the key were read before it started), and serves the manager's health probe address meanwhile so that the liveness
  probe does not have the kubelet kill the process. The job ends at `Rejoining`; the process then exits and the
  restarted one starts a manager against the returned API.
- *The operation is gone because the API was rolled back*: `ResumeEtcdRestores` runs when the controller starts (and
  the reconcile calls it when an operation it saw starting disappears): for each `TalosEtcdBackup` it reads the
  marker; a marker with no operation means the rollback happened, so the marker moves to `Rejoining` and the
  operation is recreated at `Rejoining` from the marker, with the same name and spec. The operation's finalizer
  keeps it from being deleted while `Wiping` or `Recovering`; once past, or after a failure, deleting it removes the
  marker and the pinned copy first, so a deleted restore never comes back.
- *The controller's node is a control plane*: it is not wiped with the others, because that would stop the job before
  the recovery. The snapshot goes to another control plane, and the controller's node is wiped last by the
  `Rejoining` step, as a node with stale etcd data. Its wipe stops the controller; the next one continues from the
  marker and the operation. With a single control plane there is no other node: the wipe stops the controller before it
  can recover, the plan and the status say so, and `ichor restore --recover-cluster --node-endpoint …` finishes the
  restore (it needs only the bundle and the bucket); the restarted controller then recreates the operation from the
  marker.
- *The process dies while the API is away* (the node it runs on rebooted, the Pod was killed): nothing can start,
  because the manager needs the API and the API needs the recovery. This is the same state as a total loss, and the
  same command is the answer. It is the remaining hole of this design, and it is the reason the controller's own node
  is held back.

**Plan, CLI, UI.** `PlanOperation` and `CreateOperation` take `EtcdRestoreRequest {backup, snapshot, confirm,
break_glass}`; the plan has plain-English `steps`, the `snapshot_time`, a warning that says what is lost (everything
since the snapshot, Ichor's own resources and Secrets among it), that the API is unavailable and that machines
reconcile against the restored state, and `confirm_name` and `break_glass_required`. It is blocked when the backup is
of another cluster, the snapshot is not listed, a CA rotation is newer than the snapshot, there is no control plane,
or another operation is unfinished. `OperationService.ListEtcdBackups(cluster)` returns the backups' `status.snapshots`.
A started restore cannot be paused (`SetOperationPaused` refuses, the UI does not offer it). `ichor restore --live
[--cluster C] [--backup B] [--snapshot S] --break-glass REASON [--confirm C] [--follow]` shows the plan and asks for the
cluster name; `--yes` skips the y/N question but not the name, and without `--break-glass` it is refused. The UI adds
"Restore from backup…" to the cluster page's Danger zone: a preactcn `alert-dialog` with the snapshot list, a
review step that shows the plan, the typed cluster name and the reason; the operation page shows the step,
the snapshot and its time.

## Not decided here

- Restoring from an uploaded snapshot file, and from storage other than S3.
- Choosing the target control plane (it is the first by name, other than the controller's own node).
- A restore of a cluster whose control planes changed after the snapshot. The wipe covers the control planes of
  now; the rollback restores the `TalosMachine`s of then. A control plane added since rejoins etcd but is unknown
  to Ichor until it is approved again; one removed since is still a `TalosMachine`, and the operation waits for its
  etcd member until its time limit and says which one.

## Consequences

- `TalosOperation` gains `spec.etcdRestore` and `status.etcdRestore`; `TalosEtcdBackup` gains `status.snapshots`;
  `TalosMachine` gains `status.observedReconcileRequest`; `TalosClient` gains `EtcdDataPresent`, `ResetEtcdData`,
  `EtcdRecover` and `BootstrapRecover`; the manager role gains `create` on `talosoperations` (the recreation) and the
  UI role `get;list` on `talosetcdbackups`, in `config/rbac` and the chart together.
- The controller binary now keeps the process alive after the manager stops while a restore runs, and binds the probe
  address itself for that time.
- Principle 7 ("progress lives in status") has one exception, and it is this one: the marker is the status of an
  operation that cannot keep a status.
- The marker and the pinned copy are written next to the snapshots with the backup's own credentials. Neither
  holds a secret in the clear: the marker has names, times and the reason; the copy is the encrypted snapshot.
- Not verified on hardware: nothing here has run against a real Talos cluster. The Talos side is the vendored client
  and the procedure above, read from source; the fakes in the tests stand in for the nodes. Specifically not
  observed: the reset of a node that hosts the running controller, the time the nodes take to reboot against
  `EtcdJoinTimeout`, a rejoin of a node whose old member the recovered etcd no longer lists, the kubelet keeping the
  process alive through the probe server, the informers' behaviour when the API returns with older objects, and
  an e2e round trip (the design's regular proof of restore).
