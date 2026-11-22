# 0174. An operation whose step failed on a transient error is retried automatically, with backoff, up to a limit

Status: Accepted · Date: 2026-11-22 · Design section: Upgrades

Decided: a `TalosOperation` of kind `talos`, `kubernetes`, `config` or `node` that pauses on a step whose error is transient (a node or the API unreachable, `etcdserver: request timed out`) is retried by the controller after a backoff of 1, 2, 4, 8 and 16 minutes, five attempts per step, then stays Paused for a person; `status.autoRetry` holds the step, the attempts made and the time of the next one. Every other failure pauses at once and is never retried.

## Context

ADR 0026 made a failed step hold for a person: "a paused-by-failure operation never retries automatically". A person then runs `ichor resume operation`, which sets the `ichor.maesh.dev/retry` annotation. For a failure that was a network blip this waits for a human who has nothing to decide.

The code already separates the two kinds of failure, in the Advancers. A transient failure inside a window is already a wait that returns no error (`talos_upgrade.go` `settling`, `kubernetes_upgrade.go` `upgradeApplied`, `config_rollout.go` `unreachable`, `node_action.go` `wait`). What `Advance` returns as an error is therefore one of three things: the window expired, the failure came before anything was sent, or a permanent pre-check failed. This ADR adds no second retry layer inside the Advancers and no new window: no recorded observation says a window is too short, so none is lengthened.

There is one list of transient errors. `internal/cli/follow_retry.go` held it (connection refused or reset, EOF, deadline, `Unavailable`, discovery not yet serving, `etcdserver: request timed out`, a webhook that could not be called); the controller kept a shorter one for the Ichor upgrade (`transientFailure`). Both now call `internal/transient`, `Is`, which is the CLI's list. The Ichor upgrade, which retries without a limit through controller-runtime's backoff, also keeps treating an internal error and a conflict as transient; the bounded retry here does not, since the CLI's list leaves them out on purpose. Refused credentials (`Unauthenticated`, `PermissionDenied`), `NotFound` and `Invalid` answers are not in it.

The bounded retry follows the Cilium Helm Job: a count, a doubling backoff from one minute up to 30 minutes (`cniRetryBackoff`, reused as it is), the count kept in status so a restarted controller continues it.

## Decision

1. **Where.** Only the two generic failure paths retry: the machine loop of `Reconcile` (kinds `talos`, `kubernetes`, `config`) and `nodeFailed` (kind `node`). The failure paths that call `setPhase` themselves are untouched and pause at once: `fail` in `key_rotation.go` and `ca_rotation.go`, the etcd restore (`ErrDenied`, `ErrNotFound`, and its destructive part), the shutdown kind, the Ichor and Cilium upgrades (which already return a transient error to controller-runtime's own backoff).
2. **What is retried.** An error is retried when `transient.Is` holds for it and it is not marked as possibly sent. Excluded whatever the error: a Talos `Upgrade` call whose send status is unknown (the error is wrapped in `sendUnknownError` in `talos_upgrade.go`; a `DeadlineExceeded` on that call leaves it unknown whether the node began the upgrade), a node `reset`, and any node action whose request was already recorded (`status.node.step` is `Requested`), because a retry would send it again.
3. **State.** One new field, `status.autoRetry`: `step` (the machine's name, or the node action's machine), `attempts` (automatic retries made for that step), `nextAt` (when the next one runs; absent when none is scheduled). A failure of another step starts the count at zero. A retry a person requests, and completion, clear it. The count is per step, not per failure, so a step that keeps failing and recovering is still bounded.
4. **Flow.** On a retryable failure the operation pauses as it does today (`StepFailed`, `failureReason` set, cluster lock released), with `nextAt` set and the message saying so: `... Retrying automatically at <time> (attempt 2 of 5).` The reconcile requeues for `nextAt`. When it is due the controller does what a person's retry request does (clears `failureReason`, runs the step again under the cluster lock) and adds one to `attempts`. After five attempts, or on a failure that is not retryable, `nextAt` stays absent and the message says that automatic retry gave up; the operation waits for a person as before.
5. **A person's pause is never lifted.** `spec.paused` is checked before any retry, so a paused operation is not retried, and the retry is not resumed behind it. A retry request annotation takes precedence over the timer and resets the count.
6. **Auto-patch.** An operation waiting for an automatic retry is in phase Paused, so `AutoPatchHalted` (ADR 0116) fires as for any paused automatic operation, and clears when the retry succeeds. This is the conservative choice: a blip pauses patching a little longer rather than starting a second operation under one that has not finished its step.
7. **Visibility.** The Paused condition and `failureReason` carry the message of 4, so `ichor describe operation`, `ichor get operations` and the UI show the attempt and the time with no new surface and no new proto field; `status.autoRetry` holds the same as data.

## Consequences

- One CRD field is added, which is append-only. The Helm release record grows by that field; `make check-release-record` prints its size.
- A transient failure no longer needs a person within 31 minutes of waiting in total (1+2+4+8+16); beyond that it is a person's again.
- The wording in ADR 0026 ("never retries automatically", and `failureReason` as "hold, never retry") stays as it was written and is superseded by this ADR; ADR 0026 carries a dated amendment saying so.
- Not done: a setting to turn automatic retry off, and a retry of the shutdown, rotation, restore and Ichor kinds. Each is a decision of its own.
