# 0054. Approving control planes is capped at count+1, and schedulable false needs a worker

Status: Accepted · Date: 2026-10-26 · Design section: Validation, Nodes
Decided: The TalosMachine webhook refuses an approval (update to `approved`, or create already `approved`) that would leave more than `spec.controlPlane.count`+1 approved control planes, counted by `deleteguard.CountApprovedControlPlanes`; the TalosCluster webhook refuses `schedulable` turning `"false"` with no approved worker, and the TalosMachine webhook refuses an approved worker leaving (delete, un-approve, re-role, move) when that would leave none; both webhooks now match CREATE/UPDATE as needed, not DELETE only; the race admission cannot close is left to a controller-side check, reported as a follow-up

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
TalosCluster, and `config/webhook/manifests.yaml`, the chart template and the KubeVirt harness follow. The chart
test asserts these exact operation lists instead of "DELETE only". This widens the fail-closed blast radius of
ADR 0006: with the webhook down, spec and metadata writes to those CRs are refused, not only deletes. Status
writes use the `/status` subresource and do not match. That is the same direction of failure ADR 0006 chose.

## The race admission cannot close

Admission is not serialized with the commit it gates. Two concurrent approvals at the live count `count`
both see `count` and both pass, leaving `count`+2; two concurrent last-worker removals both see two workers. This
is the same TOCTOU as ADR 0010's delete guard, and a lock does not close it for the same reason: the lock would
have to span the apiserver commit. The webhook stays as the synchronous, early refusal that is right in the
single-writer case (a person at a terminal, one UI click) and costs one List.

The real fix is ordering at the actor that changes the world. For the cap that is the controller: before it renders
or applies config for an approved control plane it should recount under the cluster lock and, if the approved total
exceeds `count`+1, hold the surplus (deterministically, newest approval first) with a Ready=False reason instead of
joining it to etcd. Not done here: it is a change to `internal/controller`, outside this unit's scope, and is
reported as a follow-up. The worker rule needs no controller twin: a transient "no workers" is a scheduling
problem that the next approval resolves, not a safety loss like etcd membership.

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
