# 0162. A paused operation releases the cluster lock

Status: Proposed · Date: 2026-11-15 · Design section: Upgrades · Extends: 0026, 0095, 0120

Decided: an operation that is paused to wait for a person releases the cluster lock when it enters that pause, does not take it back while it is paused, and takes it again only when it resumes; a resume that cannot take the lock waits. A paused operation has no expiry: it stays until a person retries or deletes it.

## Context

The cluster lock serializes disruptive operations on one cluster (`pkg/clusterlock`, ADR 0026). An operation that fails a step pauses for a person (`failureReason` is set, the failed step is never retried by the controller, ADR 0095). Before this decision such a paused operation kept the lock: every reconcile of it called `acquireDisruptiveLock`, which renews the lease, so a machine trying to join stayed unjoined for as long as the pause lasted, waiting on an operation nobody was working on.

## Decision

- **What releases means.** The lock is released actively when the pause is entered, not left to lapse. `setPhase` releases it for a pause with a step-failure reason (`failureReason` set) and for `spec.paused`. A lapse would depend on reconciles stopping, and a paused operation is still reconciled on every event; an explicit release also makes the freed lock visible at once. The cost is that the Lease object is deleted, so its `leaseTransitions` count and the acquire time of the last holder are lost. Nothing reads them after a release.
- **Which pauses release.** Only a pause that waits for a person. A self-resuming pause (a rotation blocked on a step, a lock held by another operation, a cluster transitioning, a shutdown in progress) keeps the lock, because the operation resumes by itself and needs the lock to do so. A blocked rotation that stays blocked for its give-up window becomes a step failure and releases the lock then (ADR 0120).
- **Not taken back.** A paused operation is decided before the lock is acquired. A later reconcile of a paused operation neither acquires nor renews the lock, so the release is not undone by the next pass.
- **Resume.** A retry (`ichor.maesh.dev/retry`) or the lifting of `spec.paused` takes the lock again through the normal acquisition. If another holder has it, the operation pauses with `ClusterLockHeld` and retries on its requeue. Two paused operations resumed at once compete through the Lease's `resourceVersion`: one takes the lock, the other waits. Neither interleaves with the other.
- **No expiry.** A paused operation is the record of a failure. Expiring it, or deleting it automatically, would throw away what a person needs to diagnose the failure. Releasing the lock removes the harm the pause caused (blocking the cluster) without removing the record.

## Consequences

- **The gap this decision creates.** Once a paused multi-node operation releases the lock, another operation may act on the cluster while the paused one is midway through it. The kinds with a half-done set of nodes are config, Talos upgrade, Kubernetes upgrade, the kube-proxy switch, shutdown and node actions. The design does not define what the remainder of a paused operation does when the cluster has changed under it. Resumption assumes the cluster is as the operation left it, which is the same assumption a controller restart makes, but a restart happens under the lock and an interleaved operation does not. This is accepted as the cost of ending the join block. It is not solved here: a resume that finds a changed cluster is not detected, and a remainder that depends on unchanged state is a defect to be handled per kind.
- **Exits from a paused operation, by kind.** Only some kinds can be undone by Ichor:
  - Undone by Ichor: `ca-rotation` and `key-rotation` before their point of no return (deleting the operation removes the new material and releases the lock, ADR 0120); the `node` kind's pause cleanup (uncordon and storage evacuation) for a step that failed before it was sent.
  - Retry or delete the record only, which leaves the half-done state in place: `config`, `talos`, `kubernetes`, the kube-proxy switch of `cni`, and `shutdown`. For `shutdown`, deleting the record lifts the mark that refuses other work; the stopped machines stay stopped.
  - Automatic inside a Job, not by a pause: the installer snapshot of `ichor` (ADR 0059) and Helm's atomic upgrade of the Cilium chart.
  - Not undoable: a node reset, which wipes the disk, and the destructive part of an `etcd-restore`. The restore runs without the cluster lock once it has started, so releasing the lock on its pause changes nothing for it.
- **No general rollback.** A rollback of a rollout needs the state from before each change, which Ichor does not keep, and adding it is a per-kind decision with append-only CRD fields. This ADR does not add one.
- **Deleting a paused operation.** Deleting the record of a non-rotation operation does not undo anything. The operation's cluster lock is already released by the pause, so the delete no longer frees a lock.
- **Renewal of a long pass.** A pass of the shutdown kind's stop stage makes its Talos calls one after another, up to 15 seconds each, so it can outlast the lease. It is renewed while it runs and fails past a bound derived from its own calls (`shutdownPassBound`), so a stuck pass pauses instead of requeueing with the lock in hand. The Ichor upgrade's `advanceIchor` renews once before its executor, which pins the installer image under a two-minute timeout; the reconcile's own renewal at the top of the pass already covers that call, so it is not changed.
