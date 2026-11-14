# 0026. `TalosOperation`: the backbone every upgrade kind shares, with none of their execution

Status: Accepted · Date: 2026-10-25 · Design section: Upgrades

Decided: `TalosOperation`: the backbone every upgrade kind shares (lock, status-held state machine, pause-on-failure, paused, maxUnavailable, hold annotation, ordering), with none of their execution

## Context

`docs/design/architecture.md`'s "Upgrades" section opens with what all three
upgrade kinds (Talos, Kubernetes, Ichor) share:

> All three upgrade kinds share the lock, the status-held state machine,
> pause-on-failure (no automatic cluster-wide rollback), `paused`,
> `maxUnavailable` for workers, a per-machine hold annotation, label-based
> ordering, and a progress view.

This ADR covers exactly that list, scaffolded with `kubebuilder create api
--group ichor --version v1alpha1 --kind TalosOperation --resource
--controller`, and nothing else: no Talos Upgrade API call, no Kubernetes
upgrade execution, no Ichor self-upgrade, no UI, no `ichor` CLI command.
Each of those is separate work.

## Decision: an injected `AdvanceMachineFunc`, not a per-kind branch

`spec.kind` (`talos`/`kubernetes`/`ichor`) is stored and reported, never
branched on. `TalosOperationReconciler.Advance` is a single function field
-- `func(ctx, cluster, machine, op) (done bool, err error)` -- that performs
one disruptive step for one machine and reports whether that step finished.
`nil` (the default, until a real implementation is wired into
`cmd/main.go`) means "pause with `NoExecutor`," not a panic.

Declared in `internal/controller`, the consumer, not in `pkg/upgrade`: ADR
0022 left exactly this kind of interface undeclared on purpose, because its
consumer didn't exist yet ("Go interfaces belong to their consumer, declared
where they're consumed"). This reconciler is that consumer. Each upgrade
kind's own implementation -- calling Talos's Upgrade API, driving a
Kubernetes upgrade, launching Ichor's installer Job -- is later, separate
work that plugs into this one seam; the state machine itself never needs to
know which kind it is running.

## Decision: the lock, reused exactly as built, never called directly

`TalosOperationReconciler.acquireDisruptiveLock` is a second function with
that exact name (`internal/controller/talosoperation_controller.go`),
alongside `TalosMachineReconciler`'s own
(`internal/controller/removal.go`). `TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite`
matches on the enclosing function's *name*, not its receiver type, so a
second function of the same name satisfies the guard exactly the way the
first one does.

It refuses the same way `refuseReboot`'s call does: recompute the live,
approved control-plane count (via
`deleteguard.CountApprovedControlPlanes`, read directly rather than through
the manager's cache, ADR 0010), and refuse with `*transitioningError` if it is even,
before ever calling `Acquire`. Unlike `reconcileRemoval`, `TalosOperation`
has no `completesTransition` exception: there is no scenario where an
upgrade operation is itself the step that resolves a control-plane
replacement, so this call always passes `false` (implemented by simply never
exposing the parameter).

`pkg/clusterlock.Lock.Renew` -- also called by removal.go's `renewWhile` -- is called here once
per reconcile batch, after selecting which machine(s) to advance and before
calling `Advance` on any of them, comfortably inside `DefaultDuration`'s five
minutes for the handful of calls one reconcile pass makes.

## Decision: status holds the whole state machine

`TalosOperationStatus` carries `phase` (`Pending`/`InProgress`/`Paused`/
`Completed`) and `machines` (`[]{name, phase: InProgress|Completed}`) --
nothing is kept in the reconciler's memory. A name's *absence* from
`machines` means "never selected" (held, not yet its turn, or the operation
finished before reaching it), not a third phase value forcing every reader
to special-case it.

This is what makes resumption automatic rather than a feature to build:
`Reconcile` always starts by reading `op.status` fresh and recomputing which
machines are still pending from it, so a killed and restarted controller
picks up exactly where the persisted status
says the operation was, with no separate recovery path -- the same shape
`removal.go`'s own resumability section already documents for
`clusterlock.Lock.Acquire`'s self-resumption.

## Decision: pause-on-failure stops advancing, never undoes

A machine's `Advance` call returning an error sets `phase: Paused` and
`failureReason`, and returns. It does not touch any other machine's status
-- there is no code path that could revert an already-`Completed` entry,
because nothing ever removes an entry from `machines` or demotes its phase.
Once `phase == Paused` *and* `failureReason != ""` (distinguishing a failure
from `spec.paused`), `Reconcile`'s very next check short-circuits before
calling `Advance` again on anything: a paused-by-failure operation never
retries automatically. The only way out is whatever
the operator does next (fix the underlying cause and clear the failure);
nothing here defines an automatic retry policy.

`spec.paused` is the separate, operator-driven halt: it sets `phase: Paused`
with a different reason (`OperatorPaused`) and, critically, *still holds the
cluster lock* -- the operation is not finished, only halted, so releasing
the lock would let an unrelated disruptive operation start on a cluster
that is mid-upgrade in every sense except "currently calling `Advance`."

## Decision: control planes first and alone, workers within `maxUnavailable`

Every reconcile computes `pendingMachines` for control planes first; as
long as any control plane has not reached `Completed`, exactly one of them
(not more) is the whole batch for that reconcile, and workers are never
touched. Only once every control plane shows `Completed` do workers become
eligible, up to `spec.maxUnavailable` (default 1) per reconcile. This is a
single `if/else` on "any control plane pending", not a counter or a phase
of its own -- the state already recorded per-machine is sufficient to derive
it fresh every time, consistent with principle 1.

## Decision: hold and ordering are machine-level, not operation-level

`ichorv1alpha1.TalosMachineUpgradeHoldAnnotation`
(`ichor.maesh.dev/upgrade-hold`) and
`ichorv1alpha1.TalosMachineUpgradeOrderLabel`
(`ichor.maesh.dev/upgrade-order`) are new constants in `api/v1alpha1`,
read off `TalosMachine` objects, not `TalosOperation` fields -- an operator
marks the machine, once, and every `TalosOperation` that ever touches it
honors the mark. A held machine is filtered out before it is ever a
candidate at all: it never appears in `status.machines`, and does not block
an operation from reaching `Completed` (completion only waits on machines
that were actually candidates). The order label sorts within a tier
(`sortByUpgradeOrder`, `slices.SortFunc`) by string value, a missing label
sorting last, with the object's own name as the final, deterministic
tiebreak -- which matters for tests more than production, since
`GenerateName`-assigned object names are not something a test should ever
rely on for ordering.

## Decision: no Conditions populated by the backbone

`TalosOperationStatus.Conditions` exists (the same `[]metav1.Condition`
shape every other reconciled CRD here carries) but the backbone never calls
`apimeta.SetStatusCondition` on it. `phase`, `machines` and `failureReason`
already say everything it needs to say, and nothing in the acceptance criteria
asks for a condition-shaped signal on top of them. The field stays in the
schema (removing it would be a disruptive schema change for whatever later
wants one) but is inert until something actually needs it -- deletion-by-
default, not speculative API surface. (A later amendment below adds a `Paused`
condition.)

## Decision: not tracked in Git, and why that is this type's own doc comment, not a mechanism

`docs/design/architecture.md`'s "GitOps and Argo CD" section lists
`TalosOperation`s under "What doesn't [go in Git]" alongside Secrets: it is
a record of an imperative action in progress, not a declarative desired
state. `TalosOperation`'s own Go doc comment says so explicitly and points
at `ichor gitops export` as the thing responsible for excluding it --
that command and its exclusion logic are separate. This type cannot enforce its own absence from a Git
repository; the most it can do is tell the next reader why, which is what
the doc comment does.

## Consequences

- `pkg/deleteguard.CountApprovedControlPlanes` (ADR 0010, which reads
  directly rather than through the cache) is reused
  directly by `TalosOperationReconciler.acquireDisruptiveLock`, via a new
  `APIReader client.Reader` field wired from `mgr.GetAPIReader()` in
  `cmd/main.go` -- the same pattern `TalosMachineReconciler` and the
  `TalosMachine` webhook now both use, so this new reconciler never
  reintroduces the trap that fix closed.
- `TalosOperationReconciler` is registered as its own controller in
  `cmd/main.go`, not folded into `TalosClusterReconciler` or
  `TalosMachineReconciler`, for the same work-queue-isolation reason ADR
  0024 gives for `CNIHandoverReconciler`.
- Not part of this decision: any call to Talos's Upgrade API, Kubernetes
  upgrade execution, Ichor's self-upgrade, the UI's progress view, and the
  `ichor` CLI's own upgrade commands.

- `IchorInstallation` and the `kind: ichor` executor landed in ADR 0059; the installer Job is a separate process, so the backbone is unchanged (the executor is idempotent per operation).

- The entry points landed in ADR 0068: `OperationService`, `ichor upgrade`, `ichor upgrade cni`, `ichor shutdown cluster`, `ichor get operations`, and the operations screens.

- Every pause now says why: `setPhase` writes a `Paused` condition (reason and message) for a transitioning cluster, a held lock, `spec.paused` and a failed step; `failureReason` stays reserved for failed steps because the controller, the CLI and the UI read it as "hold, never retry".

## Amendment (2026-11-04): each operation takes the cluster lock under its own identity

**The problem.** `OperationService` refuses a second operation while one is unfinished (ADR 0068, Decision 2), but the controller did not keep two operations apart. Every `TalosOperation` took the cluster lease as `ichor-controller`, and `clusterlock.Acquire` lets the same identity back in, so two operations created some other way (`kubectl`, GitOps, a script) ran their disruptive steps at the same time, and an operation also ran alongside a machine removal or join, which share that identity.

**Decision.**

- An operation acquires, renews and releases the lock as `ichor-controller/op/<namespace>/<name>` (`operationLockIdentity`). The name, not the UID, is used because an etcd restore rolls the API back and the restored operation has to find its own lease (ADR 0089). The same operation re-enters its own lock across reconciles and controller restarts; any other operation waits.
- The one that waits pauses with `ClusterLockHeld` and the message `the cluster lock is held by TalosOperation "<namespace>/<name>"; this operation starts when it finishes`, requeued every 30 seconds. A lock held by anything else keeps the old wording (`held by "<identity>"`), which is also what a machine removal or join sees when an operation holds the lock: they defer with `ClusterLockHeld` as before.
- A holder that will never renew is not waited for. If the holder is an operation that no longer exists, is `Completed`, or is `Paused` with a `failureReason` (only an explicit retry moves it, and the retry takes the lock again), `acquireDisruptiveLock` releases that lease and takes it. Before this, a failed or deleted operation's lease lingered up to five minutes and the next operation had to wait it out; with per-operation identities that wait would have appeared in the "delete it, start again" flow. An operator pause (`spec.paused`) is not stopped in this sense and keeps the lock, as above.
- A lease left by the old build under `ichor-controller` is indistinguishable from a machine removal or join, which still use that identity, so it is treated as held by someone else: operations wait. Nothing renews it once the old controller is gone, so it ages out within `clusterlock.DefaultDuration` (five minutes). That is the whole upgrade story: an Ichor self-upgrade that replaces the controller pod mid-operation resumes after at most five minutes. No migration code.
- Not changed: the machine removal and join paths (one shared identity among machines of a cluster), and the CNI Helm Job (`ichor-controller/cni-helm`, already separate).

**The webhook does not reject a second operation.** The service refuses early so a person sees why before the spec is written; the controller serializes. A webhook refusal would break GitOps, where a repository can hold several operations that are meant to run one after another. The cost is a `Paused` operation with a visible reason instead of an error at creation.

**Follow-up (2026-11-04): a control-plane join and a machine removal take the lock under the machine's own identity.** The amendment above left the machine paths on the shared `ichor-controller`, so two control-plane joins, two removals, or a join and a removal on one cluster could hold the lock together (`clusterlock.Acquire` lets the same identity back in), and the later one also released the earlier one's lease when it finished. Etcd membership changes must run one at a time: a join adds a member, a removal removes one.

- A machine acquires, renews and releases the lock as `ichor-controller/machine/<namespace>/<name>` (`machineLockIdentity`, from the name and not the UID, for the reason given above). The same machine re-enters its own lock across reconciles and restarts; any other machine or operation waits. The wait is `ClusterLockHeld` on the Ready condition (join) or the Removal condition (removal), worded `the cluster lock is held by TalosMachine "<namespace>/<name>"; control plane join deferred` (or `removal deferred`). A holder that is an operation reads `TalosOperation "<namespace>/<name>"`, and an operation that waits for a machine reads `... held by TalosMachine "<namespace>/<name>"; this operation starts when it finishes`. Any other holder, such as `ichor-controller/cni-helm`, keeps clusterlock's wording.
- A holder that will never renew is not waited for, whoever waits (`lockHolderStopped`, one function for operations and machines). A machine holder is stopped when the machine no longer exists, or when it is not being deleted and is done with its locked step. For a join that step is handing the configuration to the node in maintenance mode, which happens inside one reconcile and ends with `status.configStoredAt`, so a machine with `configStoredAt` set, or Ready, holds nothing. For a removal the step is the removal itself, which runs until the finalizer goes, so a machine with a deletion timestamp is not stopped; once the finalizer is gone it no longer exists and its lease is. There is no failed state to add for machines: a refused or failed join or removal releases the lock before it returns (the deferred `Release`), and the next reconcile takes it again. The lease is only ever left behind by a controller that died mid-step, which these rules turn from a five-minute wait into none when the machine is gone or finished.
- Upgrade: a lease the previous build left under `ichor-controller` is held by nobody this build can name, so machines wait for it as operations do, and it ages out within `clusterlock.DefaultDuration` (five minutes) because nothing renews it. No migration code.
- Worker joins take no lock and still do not: a worker is not an etcd member, and only a control-plane join reaches `acquireDisruptiveLock` (covered by `joins a worker while the cluster lock is held`). The removal of a worker that was reset (ADR 0085) still takes it, as before, to serialize its drain with everything else.
- The Cilium Helm Job (`ichor-controller/cni-helm`, ADR 0113) is unaffected: it was already a separate identity. A control-plane join still waits while it holds the lock, and the Job still waits for a machine or operation that holds it.
- Not changed, and worth knowing: the join holds the lock only while the configuration is handed over, not while the node installs, reboots and becomes an etcd member, and nothing in the lock keeps a second join or a removal from starting after the first reconcile ends. What the lock now guarantees is that two steps never overlap; the control-plane count (the even-count refusal and the approval cap) is what paces the membership changes across time.
