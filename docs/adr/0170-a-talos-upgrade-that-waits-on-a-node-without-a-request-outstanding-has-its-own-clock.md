# 0170. A Talos upgrade that waits on a node with no request outstanding has its own clock

Status: Accepted · Date: 2026-11-21 · Design section: Upgrades

Decided: when `finish` has to wait and `TalosUpgrade` is not True, a second machine condition, `TalosUpgradeWaiting`, records when the wait began; past `talosUpgradeVerifyTimeout` the step fails with `TalosUpgrade` False and reason `VerifyTimedOut`, and the operation pauses.

## Context

`TalosUpgrade` is the clock of a Talos upgrade step: True from the acknowledged Upgrade call, with `LastTransitionTime` as the start. `afterReboot` fails the step when that clock passes `talosUpgradeVerifyTimeout`, and `stepRenewUntil` stops renewing the cluster lock at the same moment.

A failed step leaves the condition False with a reason (`FellBack`, `VerifyTimedOut`), so a resumed operation issues a new request. If the node is already on the target when that operation resumes, `Advance` reaches `finish` with the condition False. There `finish` has three waits that return `false, nil` with no deadline: the node's name is unreadable, the node is not Ready, and the control plane is not yet an etcd member. `stepRenewUntil` gives a non-True condition `operationRenewCeiling` from now on every pass, so the lock is renewed for ever and the operation never pauses. The comment on `operationRenewCeiling` says each waiting step is bounded by its own clock; on this path none exists.

The False condition cannot be the clock. `meta.SetStatusCondition` moves `LastTransitionTime` only when `Status` changes (apimachinery v0.36.6, `pkg/api/meta/conditions.go`), so a False set at the failure holds the failure's time, and a resume days later would expire at once. `TalosOperation.CreationTimestamp` fails an old operation at once for the same reason. `status.lastRetry` is set only by the retry annotation, not when `spec.paused` is lifted, and the Paused condition is removed on resume.

Making the wait True again, as `kubernetesUpgradeAdvancer.waiting` does, would work, but a failure must stay False with a reason until the node is verified: `finish` rewrites a non-converged False to `Converged` only once it is, and the CLI reads the reason as failed.

## Decision

1. `finish` wraps its three waits in one helper. While `TalosUpgrade` is True the wait is unchanged: that clock is already checked by `afterReboot`. Otherwise the helper sets a TalosMachine condition `TalosUpgradeWaiting` (True, reason `Verifying`, message the TalosOperation's UID) if it is absent or carries another operation's UID. Its `LastTransitionTime` is the start of the wait.
2. Once that clock is older than `talosUpgradeVerifyTimeout`, the helper ends any evacuation, sets `TalosUpgrade` to False with `VerifyTimedOut` and a message naming the wait, removes `TalosUpgradeWaiting`, and returns an error. The operation pauses with `StepFailed`.
3. Whatever pauses a Talos operation (a failed step, `spec.paused`), `setPhase` removes `TalosUpgradeWaiting` from the machines the operation has selected, so a resume starts a fresh clock. It is done where the pause is entered because nothing records the moment of a resume: `spec.paused` being lifted leaves no timestamp, and the Paused condition is simply removed.
4. `finish` removes `TalosUpgradeWaiting` when it verifies the node.
5. `stepRenewUntil` bounds the lock renewal by `TalosUpgradeWaiting` plus the same timeout while `TalosUpgrade` is not True, and only when the condition carries this operation's UID.
6. The CLI lists `TalosUpgradeWaiting` among the progress conditions, so a True one reads as pending; `VerifyTimedOut` is already in its failure set.
7. The Kubernetes side is unchanged. Every wait in `kubernetesUpgradeAdvancer.verify` goes through `waiting`, which turns a non-True condition True with a fresh clock and fails past `kubernetesVerifyTimeout`; its other waits are the evacuation, which has its own timeout. It has none of the three unbounded waits.

## Consequences

- The waits for a Ready node and for the control plane to rejoin etcd are bounded whenever `TalosUpgrade` is not True, including when it is absent or Converged. This is deliberate: a node that stays not Ready after an operation selected it is a failed step.
- No CRD field is added: a condition is an element of `status.conditions`, which exists. The Helm release record does not grow.
- A lock loss that does not pause the operation does not reset the clock.
- A different operation's UID restarts the clock, so a wait abandoned by a deleted operation cannot fail the next one at once.
- A `VerifyTimedOut` raised here says the node did not verify within the window, which is accurate: it is on the target but not Ready or not in etcd.

## Alternatives considered

- Set `TalosUpgrade` True while waiting. Rejected: it hides the failure reason, and the existing behaviour that a node not yet verified keeps its reason is pinned.
- A new field in `TalosOperationStatus`. Rejected: a condition on the machine carries a start time without a CRD change, and the machine is where the other verify clocks live.
- Start from `CreationTimestamp` or `status.lastRetry`. Rejected: the first fails an old operation's resume at once; the second is not written when a pause by `spec.paused` is lifted.
