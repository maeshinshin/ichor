# 0137. An operation parked for an even control-plane count does not block its own cure

Status: Accepted · Date: 2026-11-07 · Design section: Nodes · Amends: 0010 (the gate that moved to where the lock is acquired), 0085 (the plan's "another unfinished operation" refusal), 0135

Decided: an operation the controller parked with `Paused` reason `ClusterTransitioning` (the count of approved control planes is even) does not block "Remove node" of a control plane, and the plan says so. While the count is even, planning a new operation (except an etcd restore) is refused with the way out, instead of creating one that is parked at once. A parked operation resumes by itself when the count is odd.

## Context

A fourth control plane was approved (3 to 4, on the way to 5), so `Transitioning=True` (`EvenControlPlaneCount`). An Ichor upgrade was created and parked at once with `ClusterTransitioning`. The user then removed the fourth control plane to go back to 3, and the plan refused: "operation ichor-2jfbw (ichor) is not finished (phase Paused); resume it ...". Resuming could not help (still even) and the override does not bypass the check: the action that ends the even count was blocked by an operation parked because of the even count.

## Decisions

1. **A parked operation holds nothing.** The operation controller checks the count before it takes the cluster lock (`acquireDisruptiveLock`, ADR 0010), so an operation with `Paused` reason `ClusterTransitioning` and no `failureReason` has not acquired it. The removal of a control plane completes the transition and takes the lock itself (`completesTransition`), and the lock still arbitrates against an operation that does run. The plan's `unfinishedOperationFor(..., resolvesTransition)` therefore skips such an operation for a control-plane Remove. Any other unfinished operation (running, paused by the operator, failed) still refuses, with the old text.
2. **Other callers keep the refusal, with an honest text.** For them a parked operation is reported as "waits for the control-plane count to be odd again; it resumes by itself then. Remove or approve a control plane to end the replacement", not as something to resume by hand. Approving or rejecting a machine reads no operation and was never blocked by one.
3. **Auto-resume.** The controller already requeues a parked operation every `operationRequeueInterval` and re-runs the count; once it is odd the pause condition clears and the operation continues. Nothing else is bypassed: the other pause reasons (operator, lock held, shutdown, failure) are untouched. A test pins it.
4. **Refuse at the plan.** `PlanOperation` returns `Blocked` for every kind except an etcd restore (a recovery, which runs before the count check) while `Transitioning=True`: "the cluster has an even number of approved control planes (...): ... Remove a control plane (to go back down) or approve another one (to go up), then run this again". The UI and CLI already show `Blocked`. A reset of a control plane while the count is even is likewise refused with "Remove the node instead, which ends it", because a reset operation would be parked by the controller; Remove does not.

## Rejected

- Letting every operation through the lock check while even: it would run disruptive work on an etcd without a majority margin (ADR 0010).
- Deleting a parked operation when Remove starts: it hides that the upgrade was wanted.

## Risks

An operation that was already running when a control plane was added keeps the lock until it renews out; Remove then waits on the lock (HeldError, retried) rather than the plan refusing. The plan reads the cluster's `Transitioning` condition, which can be one reconcile stale; the controller recounts.
