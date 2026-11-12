# 0054. Approving control planes is capped at count+1, and schedulable false needs a worker

Status: Accepted · Date: 2026-10-26 · Design section: Validation, Nodes
Decided: The TalosMachine webhook refuses an approval (update to `approved`, or create already `approved`) that would leave more than `spec.controlPlane.count`+1 approved control planes, counted by `deleteguard.CountApprovedControlPlanes`; the TalosCluster webhook refuses `schedulable` turning `"false"` with no approved worker, and the TalosMachine webhook refuses an approved worker leaving (delete, un-approve, re-role, move) when that would leave none; both webhooks now match CREATE/UPDATE as needed, not DELETE only; the race admission cannot close is closed by a controller-side hold of the newest surplus approvals (`ControlPlaneOverCap`)

## Context

The design's Validation section lists "no approving more control planes than configured" and "no
`schedulable: false` with zero workers". Neither was enforced: `ValidateCreate`/`ValidateUpdate` returned nil, and
the webhook configurations (generated from the `+kubebuilder:webhook` markers, copied into the chart) matched
`DELETE` only, so those methods were never even called. Only the lower bound (ADR 0010, 0032) existed.

## Decision

**Upper bound.** A control plane is replaced 3 -> 4 -> 3, adding before removing (ADR 0032). A plain
`approved <= count` would make that impossible. The rule is: approving is refused when the control planes
*already* approved exceed `spec.controlPlane.count`; so the approved total may reach `count`+1, never more.
`count` is odd (1, 3, 5), so `count`+1 is exactly the one even, `Transitioning` state the design allows
(`transitioningControlPlanes`), and it pairs with `CheckControlPlaneCount`: that check refuses removal below
`count`, this one refuses approval above `count`+1. Together the live total stays in `[count, count+1]` outside a
deliberate change of `spec.controlPlane.count`. Lowering `count` below the live total is not blocked (that is how
a shrink starts); raising it widens the cap. Cases: 1 -> 2 ok, 2 -> 3 refused on count 1; 3 -> 4 ok, 4 -> 5
refused on count 3.

The check runs on the transition into "approved control plane of cluster X" only (update, or a create that is
already approved, since a GitOps apply can approve by creating), so ordinary edits cost nothing. A machine whose
TalosCluster does not exist or is deleting is let through: there is nothing to compare to, and the controller
does not act without the cluster.

**Schedulable.** Two operations can reach "unschedulable, no workers", and each is checked where it happens:
`TalosCluster` update, only on the transition into `"false"` (a cluster is created `false` before any worker is
discovered, per the `standard` and `large` presets, and an unrelated edit to a cluster that has since lost its
workers must not be blocked); and removing an approved worker from a cluster whose `schedulable` is `"false"`,
which is `ValidateDelete` (after `CheckDelete`, as for control planes) and the `ValidateUpdate` cases that make an
approved worker stop being one. A cluster being deleted skips the check so teardown can cascade. Only approved
workers count; a pending one has not joined.

**Counting.** All counting lives in `pkg/deleteguard` (`CountApprovedControlPlanes`, new `CountApprovedWorkers`,
one shared `countApproved`), read through `mgr.GetAPIReader()`. No new `TalosMachineList` appears in the webhook
package. The pure rules are `CheckControlPlaneApproval` and `CheckLastWorker`.

**Webhook scope.** The markers now say `create;update;delete` for TalosMachine and `update;delete` for
TalosCluster, and `config/webhook/manifests.yaml`, the chart template and the e2e harness follow. This widens the fail-closed blast radius of
ADR 0006: with the webhook down, spec and metadata writes to those CRs are refused, not only deletes. Status
writes use the `/status` subresource and do not match. That is the same direction of failure ADR 0006 chose.

## The race admission cannot close

Admission is not serialized with the commit it gates. Two concurrent approvals at the live count `count`
both see `count` and both pass, leaving `count`+2; two concurrent last-worker removals both see two workers. This
is the same TOCTOU as ADR 0010's delete guard, and a lock does not close it for the same reason: the lock would
have to span the apiserver commit. The webhook stays as the synchronous, early refusal that is right in the
single-writer case (a person at a terminal, one UI click) and costs one List.

The real fix is ordering at the actor that changes the world, which for the cap is the controller. It is built
(see the next section); the worker rule needs no controller twin: a transient "no workers" is a scheduling problem
that the next approval resolves, not a safety loss like etcd membership.

## The controller-side hold

`TalosMachineReconciler.Reconcile` recounts approved control planes before it loads the bundle, renders or applies
anything. If the total exceeds `spec.controlPlane.count`+1, the machines beyond the first count+1 are held:
`Ready=False`, reason `ControlPlaneOverCap`, nothing rendered, no dry run, no apply, and a 15 s requeue so the hold
lifts by itself once a sibling is deleted, un-approved, or `count` is raised.

- **Which is newer.** Approved control planes are sorted by `spec.approvalDecision.at`, ties broken by name
  (ascending), and the tail past count+1 is held. A machine with no recorded decision time (a GitOps apply that
  created it already approved) falls back to its `creationTimestamp`. The sort and the cut live in
  `deleteguard.ControlPlaneOverCap`, which shares one listing (`approvedMachines`) with
  `CountApprovedControlPlanes`; so the controller gained no `TalosMachineList` declaration, the guard allow-list is
  unchanged, and the decision is made in the one package that already owns the count. Read through `APIReader`.
- **Already an etcd member.** The hold only stops the controller from touching the node. It never removes the
  member, resets the node, or changes `spec`: the controller does nothing destructive without a user action. A held
  machine that had already joined stays a member, its config stays as last applied, and `Ready` says why. The
  operator resolves it by deleting a surplus control plane (the ordinary guarded removal path, which the hold does
  not block) or raising `count`. A held machine that is the only one the user wants removed can be removed the same
  way; no special case.
- **No lock.** The recount does not take the cluster lock. `acquireDisruptiveLock` refuses while the cluster is
  `Transitioning`, and count+1 *is* the Transitioning state, so taking it here would block exactly the legitimate
  replacement; and a hold is not a disruptive operation (it changes nothing on any node). The lock would also not
  close the race: two approvals commit independently of any controller lock, and the recount sees both once they
  are committed. Idempotence is what makes it safe: every reconcile recomputes the same ordered cut from live
  state, so two controllers or two passes agree on the same held set.
- **Residual window.** A node approved just before the surplus approval is visible can already have been
  configured; that is the case above (it is older, so it is not the one held). The hold only prevents the newest
  surplus from joining, which is the point.

## Consequences

- `ValidateUpdate`/`ValidateCreate` on TalosMachine and `ValidateUpdate` on TalosCluster are now live. A new
  `APIReader` field on the cluster validator.
- Approving a control plane at exactly `count` always succeeds, even when it was not meant as a replacement; the
  cluster then reads `Transitioning` until a member is removed or `count` is raised.
- The webhook reads TalosClusters; the controller's existing RBAC already allows it.

## Alternatives considered

**`approved <= count`.** Makes 3 -> 4 -> 3 impossible; rejected.

**Count only while `Transitioning` is true on the cluster status.** Status can be a reconcile stale and the
condition is derived from the very count being checked; the live count is the source.

**A ValidatingAdmissionPolicy.** CEL cannot list sibling objects.

## Addendum: one filter for "the machines that count"

The control-plane count, the approval cap and several controllers each narrowed a `TalosMachineList` by hand
(cluster, approval, role), and they disagreed about a machine that is being deleted: the count included it,
`findEtcdSurvivor` skipped it. This is a policy matter, not an etcd one: members leave etcd one at a time, so a
removal in progress cannot break quorum. What it broke was the declared count. With 4 control planes of which one
is stuck mid-removal (finalizer still held), counting it let a second removal pass `4 -> 3` although only 3 were
staying, so the effective count fell to 2.

`deleteguard.ApprovedMachines(ctx, reader, ns, cluster, MachineFilter{Role, IncludeDeleting, KeepDeleting})` is now
the only function that lists TalosMachines and narrows them to approved ones of a cluster. By default a machine
with a `DeletionTimestamp` is not counted. `KeepDeleting` names the machine whose own removal is being decided,
which the quorum and count rules count "including the one being removed". `IncludeDeleting` is for a caller that
must reach every node (the whole-cluster shutdown). `CountApprovedControlPlanes`, `CountApprovedWorkers`,
`ControlPlaneOverCap`, `findEtcdSurvivor`, `snapshotCandidates`, `candidateMachines`, `othersDone`,
`shutdownPlan` and the TalosStorage readiness check use it; `awaitPowerOn` reads each recorded machine by name.
Only `machinesOfCluster` (it must enqueue unapproved and deleting machines too) still lists by hand, and is the one
entry left in `countplanesAllowedFuncs` (ADR 0010's guard).

Side effects of the default: a deleting machine is no longer picked as a snapshot source, a rolling-operation
candidate, or waited for by the kube-proxy switch, and does not make a TalosStorage look unconverged.
