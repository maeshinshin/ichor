# 0010. The quorum and last-control-plane delete guards are stronger than `allow-delete`

Status: Accepted · Date: 2026-10-25 · Design section: Nodes (removal), Roadmap phase 3

## Context

Phase 1 of the deletion guard (`pkg/deleteguard.CheckDelete`) only checks the
`ichor.maesh.dev/allow-delete` annotation. The design's removal sequence
(architecture.md, "Nodes") says the etcd-member-removal step is "refused if
quorum would break, if the count would drop below `spec.controlPlane.count`,
or if it's the last one," and calls this out as Ichor's own responsibility:
"Talos's member-removal call requires only that a leader exists right now; it
does not ask whether the remaining members can still reach quorum." The
roadmap makes the acceptance condition explicit: "an explicit test has to show
that the annotation alone does **not** let the last control plane go." This
ADR covers two of those three refusals — quorum and last-control-plane; the
third (`spec.controlPlane.count`) belongs to the replace/topology-change flow
that doesn't exist yet, and is left for whoever builds that.

Querying Talos for the live etcd member list is out of scope here: the only
state available is the `TalosMachine` resources in the namespace, so the
check has to be computed from those.

### How to count

`TalosMachineSpec` already carries `clusterRef.name` and `role` (enum
`controlplane`/`worker`), and `approved` (bool). A control plane only ever
becomes an etcd member once approved — the design is explicit that approval
is what lets the controller render and apply configuration and start the
machine joining the cluster; an unapproved `controlplane`-role `TalosMachine`
has never been a member. So "control planes right now" is: `TalosMachine`s in
the same namespace, with the same `clusterRef.name`, `role: controlplane`,
`approved: true` — including the one being deleted, since the webhook runs
before it is actually removed. No new CRD field was needed.

### Quorum math, and n=1

etcd quorum for `n` members is `(n/2)+1` (integer division). Deleting one
member leaves `n-1`. Working the formula for every `n` that can occur (control
planes are 1, 3 or 5, with even counts only transient during a change):

| n (before) | quorum(n) | n-1 (after) | holds? |
|---|---|---|---|
| 1 | 1 | 0 | no |
| 2 | 2 | 1 | no |
| 3 | 2 | 2 | yes |
| 4 | 3 | 3 | yes |
| 5 | 3 | 4 | yes |

Single-member removal only ever fails quorum at `n` = 1 or 2; from `n` = 3
up, `n-1` always meets `(n/2)+1`. That's not a coincidence worth hand-waving
away: for `n >= 3`, `(n/2)+1 <= n-1` always holds, so removing exactly one
member can never be the thing that breaks quorum once there are three or
more.

`n=1` was considered for a carve-out — refusing to delete a single-node
cluster's only control plane could be seen as overreach, since there's
nothing left to protect once it's gone anyway. Weighed against that: the
design's removal-sequence refusals are written as unconditional ("or if it's
the last one"), with no such carve-out, and that sequence exists specifically
to walk a *running* node through drain and etcd-member removal before reset
— work that is pointless, and actively harmful (draining a node whose
workloads have nowhere else to go), for a cluster that's being torn down
entirely rather than shrunk. Tearing down a whole cluster already has its own
path: delete the `TalosCluster`. Per-`TalosMachine` deletion is "remove this
member from a cluster that keeps running," and a cluster cannot keep running
on zero control planes. So `n=1` gets no special case: `CheckControlPlaneQuorum`
treats "would leave zero" as the same refusal at every `n`, which is also
simpler than carrying a second rule that only fires once.

### Why this overrides the annotation

`allow-delete` exists to stop a GitOps prune from cascading into node resets
(architecture.md, "Validation") — it's a "did a human mean this" gate, not a
safety check on the cluster's ability to keep running. A human setting it
cannot make etcd quorum math come out differently, so the roadmap requires
this guard to refuse regardless: `CheckControlPlaneQuorum` is called
independently of, and in addition to, `CheckDelete`. There is no way to
override it through the webhook; the only path to removing the last control
plane is deleting the `TalosCluster`, which answers a different question
("tear down the whole cluster," not "shrink it by one").

## Decision

- `pkg/deleteguard.CheckControlPlaneQuorum(currentControlPlanes int) error`
  is added alongside `CheckDelete`, staying a pure function so it can be
  called from the webhook today and a UI backend later without either owning
  the counting logic (design principle 10). It refuses when removing one
  member would leave zero control planes ("last control plane") or would
  drop below `(currentControlPlanes/2)+1` ("quorum"), and both error
  messages say the before and after counts so `kubectl delete`'s error
  explains itself.
- `TalosMachineCustomValidator.ValidateDelete` (the only place that can
  trigger this for a control plane, since the webhook is DELETE-only) runs
  `CheckDelete` first, then, only for an approved `controlplane`-role
  machine, lists sibling `TalosMachine`s in the same namespace and cluster to
  count current control planes and calls `CheckControlPlaneQuorum`. Neither
  check can be skipped by satisfying only the other.
- `TalosCluster` deletion is unchanged (`CheckDelete` only): deleting the
  whole cluster is not "the last control plane leaving a running cluster,"
  it's the cluster going away, which `allow-delete` alone continues to gate.
- No new CRD field. `ClusterRef`, `Role` and `Approved` already said
  everything the check needed.

## Consequences

- `pkg/deleteguard` stays at 100% test coverage; `CheckControlPlaneQuorum` is
  tested at every boundary `n` in {1, 2, 3, 4, 5}, and a dedicated test
  (`TestAllowDeleteDoesNotOverrideQuorum`) shows the annotation does not
  change its answer, which is the roadmap's named acceptance condition.
  `internal/webhook/v1alpha1`'s envtest suite adds the same cases end to end,
  including the sequential case the task called out by name: three approved
  control planes, delete one (3 -> 2, allowed), then delete another
  (2 -> 1, refused for quorum).
- `TalosMachineCustomValidator` now needs a `client.Client` to list siblings;
  `SetupTalosMachineWebhookWithManager` supplies `mgr.GetClient()`. No RBAC
  change was needed -- the manager's role already has `list`/`watch` on
  `talosmachines`.
- Pending (unapproved) control-plane `TalosMachine`s are not counted and can
  always be deleted through this path once `allow-delete` is set: they were
  never etcd members, so nothing protects them. This is also why a lone
  unapproved control plane does not hit the "last one" refusal.
- The third removal-sequence refusal -- dropping below
  `spec.controlPlane.count` -- is not implemented. It's a topology/replace
  concern (phase 3's "replace add-then-remove" flow), not a bare quorum or
  last-member check, and doesn't exist to retrofit onto yet.
- Real etcd member health is still never queried; this guard is optimistic
  about every counted `TalosMachine` actually being a healthy voter. If that
  turns out to be wrong in practice (a control plane counted as live but
  actually down), the fix is a live etcd member query before removal, which
  is explicitly out of scope here and would need its own design pass.
- Counting objects at admission time cannot enforce an invariant across
  concurrent requests, and no amount of locking around the count changes
  that: two concurrent `DELETE`s for different control planes in the same
  cluster can each `List` before either delete commits, so both see the
  pre-deletion count and both get admitted, even though applied together
  they break quorum (marked `shortcut:` at the list call in
  `talosmachine_webhook.go`). This is not a missing lock -- an admission
  webhook is never serialized with the commit it is gatekeeping, so
  `pkg/clusterlock` around this check would still let both webhook calls run
  (and both pass) before either deletion lands; it closes nothing. The real
  fix is the removal operation described in design's "Nodes" section: once
  control-plane removal only happens as a step inside that serialized,
  ordered sequence, the webhook's job changes from "count and decide" to
  "refuse a delete that isn't part of the operation," and the operation
  itself -- which *is* serialized -- does the counting. That's phase 3's
  removal work, not yet built. Acceptable for now because today's deletions
  are an operator acting one at a time, not several removals fired at once.

### What this guard protects, and what it doesn't

It protects against a single, unaccompanied delete accidentally breaking
quorum or taking the last control plane -- the case the roadmap's
acceptance test asks for, and the only case a webhook that only sees one
request at a time can actually decide. It does not protect against several
such deletes landing concurrently and jointly breaking quorum, each one
individually legal by this check's own math; closing that gap needs the
ordered removal operation above, and stays open until that lands.

## The ordered removal operation closes the gap above

The previous section named the real fix and left it open: "once
control-plane removal only happens as a step inside that serialized,
ordered sequence, the webhook's job changes from 'count and decide' to
'refuse a delete that isn't part of it,' and the operation itself -- which
*is* serialized -- does the counting." `internal/controller/removal.go`
is that operation.

### What it does

An approved control-plane `TalosMachine` gets a finalizer
(`ichor.maesh.dev/machine-removal`) on its first reconcile -- nothing else
does, since a worker or an unapproved machine was never an etcd member and
has nothing here to protect. Deleting one no longer removes it
immediately: `TalosMachineReconciler.Reconcile` sees the
`deletionTimestamp`, finds the finalizer, and hands off to
`reconcileRemoval`, which:

1. Acquires the cluster's `pkg/clusterlock` Lease with the controller's
   own stable identity (the same `controllerIdentity` and the same Lease
   `refuseReboot` already takes for reboot-causing applies -- one lock per
   cluster, shared across every disruptive operation, principle 6). Held
   by someone else: report it and stop, finalizer untouched, exactly the
   way `refuseReboot` already reports `ClusterLockHeld` without a
   `RequeueAfter` -- the next trigger is whatever next touches this object,
   not a timer.
2. Recounts approved control-plane siblings *under the lock*. A second,
   concurrent removal of a different control plane in the same cluster has
   to take the same lock to reach this point, so it either already
   finished its own count-and-decide (and this reconcile sees the result)
   or is still waiting its turn -- never racing the same `List` the webhook
   above cannot serialize.
3. Runs every refusal docs/design/architecture.md's "Nodes" names for the
   etcd-member-removal step: `deleteguard.CheckControlPlaneQuorum`
   (last-control-plane and quorum, unchanged from the first section of
   this ADR) and the new `deleteguard.CheckControlPlaneCount`, which closes
   the gap the first section of this ADR left open on purpose ("the third
   [refusal]... belongs to the replace/topology-change flow that doesn't
   exist yet"). That flow still doesn't exist as a first-class thing, but
   the check it needs does now: `CheckControlPlaneCount` refuses removing a
   control plane when doing so would leave fewer control planes than
   `spec.controlPlane.count` declares, which is exactly what forces
   "3->4->3, never 3->2" (architecture.md) -- getting back to the declared
   count after a removal requires approving a replacement *first*.
4. If every check passes, drains the corresponding Kubernetes Node
   (`k8s.io/kubectl`'s own helper, cordon then evict -- architecture.md:
   "Draining uses `k8s.io/kubectl`'s own drain helper rather than a
   hand-written eviction loop"), renewing the lock every minute while that
   runs (see "Renew gets its first caller" below).

### The webhook's check is kept, not replaced

`TalosMachineCustomValidator.ValidateDelete`'s admission-time count (first
section of this ADR) stays exactly as it was. Two things made "keep both"
the right call over "replace it":

- It is the only one of the two that can refuse **synchronously, at
  admission**, with a message `kubectl delete` shows immediately. The
  reconcile-level check can only report through a condition after the
  fact, which is strictly slower feedback for the common case (one
  operator, one delete at a time -- the case ADR 0010's own "Acceptable
  for now" already named).
- It is not *wrong* for that case, only incomplete for the concurrent one.
  Removing it would make the single-delete path strictly worse (slower
  refusal, no change in safety) to fix a gap it was never going to close
  anyway.

So the shape is: the webhook is a fast, synchronous, best-effort check
that happens to already be correct for the overwhelmingly common case; the
reconcile-level check inside the lock is the authoritative one, and the
only one a concurrent pair of deletes cannot slip past. Nothing about this
ADR's "no way to override [the quorum guard] through the webhook" changes
-- removal.go enforces the identical rules, just serialized.

### The finalizer never clears in this phase, on purpose

Removing the etcd member and running the Talos `Reset` that returns a node
to maintenance mode both need `TalosClient` widened past the three methods
its own doc comment calls "the one interface rule 0 allows" -- a design
decision, referred back rather than made here (per the task that asked for
this: widening `TalosClient` is explicitly a refer-to-main trigger).
Without the reset, letting the `TalosMachine` disappear would be a lie:
the physical node would still be configured and still an etcd member.
So `reconcileRemoval` never clears the finalizer on the safe path either
-- it drains and reports `RemovalApproved`, and stops. Only two things can
end the `Terminating` state today: a human break-glass removal of the
finalizer (already how the design treats this general problem --
"Refusing finalizer removal except through break-glass belongs with the
other dangerous-operation checks" -- unchanged by this work), or a future
change that adds the etcd-removal and reset steps on top of this
foundation and then clears the finalizer once they succeed.

One consequence worth naming plainly: because nothing ever truly
disappears in this phase, a control plane that reached `RemovalApproved`
still counts toward every later removal's quorum/count check (it is
still, genuinely, an undisturbed etcd member). This is the conservative,
correct choice -- excluding it early would be pretending a drain-only step
already finished the job -- but it does mean several "approved" removals
in the same cluster can accumulate without any of them actually reducing
risk, since none of them do anything irreversible yet. That stops being
true the moment the deferred etcd-removal/reset step lands and starts
actually completing removals; whoever adds it should also decide whether
an object already mid-removal should be excluded from a *different*
machine's count, which this phase does not need to answer because nothing
here is irreversible yet.

### What the four failure modes mean for this slice

Architecture.md's "Nodes" names four things about the removal sequence
that are "easy to get fatally wrong," verified against Talos 1.14.3's
source. Two are about the `Reset` call specifically -- naming the
partitions to wipe, and the fire-and-forget double-reset hazard -- and do
not apply to this slice at all, because there is no `Reset` call in it;
that is the direct consequence of referring `TalosClient`'s widening back
rather than building it now. The other two are exactly what this slice
delivers and tests:

- "Ichor's refusals are Ichor's alone." `reconcileRemoval` never asks
  Talos anything -- there is no Talos API call anywhere in it. Every
  refusal comes from `pkg/deleteguard`, run twice (webhook, then
  reconcile) against nothing but counted `TalosMachine` objects.
- "A graceful reset already drains and already leaves etcd" -- the reason
  this phase's own drain and (deferred) etcd-removal are belts, not
  trousers, for a live node. Draining here anyway is still worth it on its
  own terms (it is the one step of the sequence that needs no `TalosClient`
  change and is independently safe and reversible -- cordon and evict,
  nothing deleted, pods reschedule), which is why it is implemented even
  though the step that would make it strictly necessary (`Reset`) is not.

### Resumability

A crash mid-removal is survivable without any new state machine, because
every step `reconcileRemoval` takes is already idempotent or safely
resumable on its own:

- `clusterlock.Lock.Acquire` resumes its own identity's lock rather than
  erroring (its own doc comment: "if identity already holds it, that's
  resumption") -- a restarted controller re-reconciling the same
  `Terminating` object (which it will: client-go's informer re-lists every
  object with a pending finalizer on startup, no special code needed)
  picks the lock back up rather than being told it's held.
- Recounting siblings and re-running the three checks is a pure
  read-then-decide with no side effect to double up on.
- Re-draining an already-drained (or partially drained) node is harmless
  (architecture.md says so directly: "re-draining a cordoned node is
  harmless"), and `k8s.io/kubectl`'s own cordon/evict helpers are written
  to be re-run.
- The `Removal` status condition (distinct from `Ready`, which this path
  never touches) records the last verdict reached, so the object's status
  reflects reality again the moment the resumed reconcile finishes --
  satisfying architecture.md's chaos-case requirement that progress
  resume from status, without needing a dedicated phase field to do it.

Tested directly: acquiring the same cluster's lock as a second,
independently-constructed `TalosMachineReconciler` (simulating a fresh
process with no memory of the first) succeeds rather than erroring, and
the removal verdict it reaches afterward is correct.

### Renew gets its first caller

`pkg/clusterlock.Lock.Renew` existed since the lock package was written,
documented ("the reconciler is expected to call Renew well inside this
window on every step") but never called from production code until this
file's `renewWhile`, which renews every minute (`removalRenewInterval`,
comfortably inside `DefaultDuration`'s five) for as long as the drain step
is running, stopping the moment it returns. Tested by giving a `Lock` a
deliberately short duration, running `renewWhile` against it, and showing
the lock survives several multiples of that duration -- which only holds
if `Renew` is really being called repeatedly, not just once at the start --
and that it expires normally once `renewWhile` is stopped.

### RBAC this added

Draining needs a `kubernetes.Interface` Clientset (`k8s.io/kubectl`'s drain
helper takes one, not controller-runtime's cached `client.Client`), wired
in `cmd/main.go` from the manager's own config. The new RBAC it needs --
`get`/`list`/`watch`/`patch` on `nodes`, `get`/`list`/`delete` on `pods`,
`create` on `pods/eviction`, `get`/`list` on `poddisruptionbudgets` -- is
additive only; nothing already granted was widened or removed.
`k8s.io/kubectl` is pinned to the same `v0.36.0` line as the rest of this
module's `k8s.io/*` dependencies (`go get k8s.io/kubectl/pkg/drain@v0.36.0`
specifically, not a bare `go get`, which resolves to kubectl's latest and
drags every `k8s.io/*` dependency in this module up with it -- caught
before it reached `go.sum`, not after).

## `Transitioning`: the live control-plane count, not the declared one

Architecture.md's "Nodes" section makes one more claim this ADR's work
touches directly: "Even counts are allowed only transiently during a
change and shown as 'transitioning', during which the cluster lock blocks
other disruptive work." `spec.controlPlane.count`'s `Enum=1;3;5` is the
*declared* state and never becomes even -- so "transitioning" has to be a
fact about something else: the live count of approved control-plane
`TalosMachine`s, which does pass through 2 or 4 during an add-then-remove
replacement (3->4->3).

### Where it lives, and why

`TalosCluster.status.conditions` carries a `Transitioning` condition,
not a new CRD. Two reasons:

- It is a derived fact about objects that already exist (`TalosMachine`s),
  recomputed fresh every time, not a record of an imperative action in
  progress -- nothing here corresponds to "a `TalosOperation` is running"
  (that CRD, per the API table in architecture.md, is for one-off
  imperative actions like reboot/reset/snapshot/rollback; a control-plane
  replacement today is two separate operator actions -- approve one
  machine, delete another -- with no single operation object spanning
  both of them to hold a condition on).
- `TalosCluster` is the object whose topology the condition describes, and
  it already has a `Status.Conditions` field (ADR 0010's own quorum work
  needed nothing new there either).

`countApprovedControlPlanes` -- already counting control planes for the
quorum/count checks above -- is reused to compute it, from two places: the
removal operation (after every `reconcileRemoval` recount) and
`TalosMachineReconciler.Reconcile`'s main path (after every approved
machine's reconcile, of any role, since the fact being reported is
cluster-wide, not specific to the machine that happened to trigger it).
Piggybacking on counts these call sites already pay for means the
condition stays reasonably current without a dedicated watch or poll of
its own.

### How it actually blocks other disruptive work

The lock itself is not continuously held for the whole, human-paced
window between "approve the replacement" and "remove the original" --
only briefly, around each individual step, same as every other disruptive
operation in this codebase. So "the cluster lock blocks other disruptive
work" during a transition is implemented as an explicit check, not solely
the lock's own mutual exclusion: `refuseReboot` recomputes the live count
fresh (not trusting the stored `Transitioning` condition, which could be
one reconcile stale) and refuses before ever attempting the lock if the
count is even. The only other disruptive-operation path that exists today
is `refuseReboot`; a future upgrade or CA-rotation operation gets the same
treatment by calling the same `transitioningControlPlanes` check.

### Tests

`internal/controller/removal_test.go`'s "TalosCluster transitioning
status" group covers: a stable (odd) count reports `Transitioning: False`
(`StableControlPlaneCount`); approving a fourth control plane flips it to
`True` (`EvenControlPlaneCount`); and a reboot-requiring change on *any*
machine (not just a control plane) in a transitioning cluster is refused
with `ClusterTransitioning`, before the cluster lock is ever taken.
