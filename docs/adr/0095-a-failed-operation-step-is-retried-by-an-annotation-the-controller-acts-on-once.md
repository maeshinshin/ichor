# 0095. A failed operation step is retried by an annotation the controller acts on once

Status: Accepted · Date: 2026-10-27 · Design section: Upgrades, UI backend · Extends: 0026, 0068

Decided: `ichor operation retry NAME` (and a Retry button and the `OperationService.RetryOperation` RPC, next to pause and resume) sets the annotation `ichor.maesh.dev/retry=<RFC 3339 time>` on a `TalosOperation` that is Paused with a `failureReason`; the controller, when that time is newer than the new `status.lastRetry`, clears `failureReason`, records `lastRetry` and runs the failed machine's step again; `resume` stays as it was (it lifts `spec.paused` only) and says so when the operation has failed; `ichor node` actions also accept the machine's hostname or Kubernetes Node name instead of the TalosMachine name

## Context

ADR 0026 pauses an operation when a step fails and never retries it by itself, and `failureReason` is what tells the controller, the CLI and the UI that. `ichor resume operation` writes `spec.paused=false`, which is not what is wrong, so it did nothing and the phase stayed Paused. The only way out was to delete the operation and create it again, and neither the help text nor the UI said so. This follows every transient failure (a drain blocked by a bare pod, a refused webhook, a timeout).

## Decision

**The request is an annotation, not spec.** The controller never mutates `spec`, and `spec.paused` is the wrong switch: it is the operator's, and a failed step must not run again just because somebody un-paused. `ichor.maesh.dev/retry` carries the time of the request. The RPC (and so CLI and UI) sets it as the caller, so the same Kubernetes RBAC as pause and resume applies (`update` on `talosoperations`).

**The controller acts once per request.** In the gate that used to return for a Paused operation with a `failureReason`, it parses the annotation; if the time is after `status.lastRetry` (a new field, nil at first) it sets `lastRetry` to that time, clears `failureReason`, sets the phase InProgress and carries on in the same reconcile. The failed machine is still `InProgress` in `status.machines` (the failure path leaves it so), so the normal batch selection picks it up again; machines already `Completed` are not touched. If the step fails again the operation pauses again with the new reason. A missing, unparsable or not-newer annotation is ignored, and so is any annotation on an operation that is not Paused with a `failureReason`. The same gate in the etcd-restore driver does the same. `spec.paused` still wins: an operation paused by an operator stays paused, and the retry is acted on after it is resumed.

**The RPC refuses what cannot be retried.** `RetryOperation` returns `FailedPrecondition` unless the operation is Paused with a `failureReason`. It writes a time that is never earlier than `lastRetry` plus one second, so a second retry within the same second of the first is still newer. Annotation times have second resolution (`metav1.Time`); two requests in one second collapse into one.

**`resume` says what it did not do.** When the operation it resumed is still Paused with a `failureReason`, the CLI prints "the step failed: REASON; run `ichor operation retry NAME` after fixing the cause". `--watch` and `follow` errors for a failed operation carry the same hint. The UI shows Retry for a failed operation (before, it showed no button for one) and Resume only for an operator pause.

**Node actions take a hostname.** `ichor reboot node NAME` took only the TalosMachine name, a UUID. The server resolves the name it is given: the TalosMachine name first; otherwise the machine whose `spec.hostname` is that name; otherwise the machine whose Kubernetes Node has that name (or the address match `findKubernetesNode` already does). More than one match is `InvalidArgument` listing the candidates; none keeps the original `NotFound`. This is in the server so the UI and CLI agree; the reset and remove confirmations accept the machine's own name or the name the caller used.

## Consequences

One new status field (`lastRetry`) and one annotation; the CRD is regenerated. A retry cannot be undone, the same as the step it repeats. Deleting and recreating still works but is no longer the advice.

## Amendment: one command

`ichor operation retry`, the `RetryOperation` RPC and the UI Retry button are gone: two commands for one intent. `resume` (`SetOperationPaused` with `paused=false`) on an operation that is Paused with a `failureReason` also writes the retry annotation, with a time later than `status.lastRetry`, in the same update; on any other operation it only lifts `spec.paused`. The CLI prints "re-running the step that failed: REASON". The UI shows Resume for a failed operation, a failed etcd restore included, behind a confirmation that says it re-runs the failed step. The controller side (annotation and `lastRetry`) is unchanged. Resuming is no longer refused for an etcd restore that has started (only pausing is), so a failed one can be resumed.

## Amendment 2026-11-05: the request is `true`

The annotation takes the value `true`, not an RFC 3339 time, as of v0.1.0: `kubectl annotate talosoperation NAME ichor.maesh.dev/retry=true` is a retry. `ichor resume operation` and the UI write `true` too. The time form is gone with no compatibility path, so a time is an ignored value like any other.

The controller acts only on an operation that is Paused with a `failureReason` and is not paused by `spec.paused`. It sets `status.lastRetry` to the time it acted, clears `failureReason`, runs the failed step again, and then removes the annotation with a metadata patch that carries the resource version, so a concurrent edit is not overwritten (the patch fails and the reconcile retries). Removing it is what makes the next `true` a new request, which replaces the "newer than `lastRetry`" comparison. If the removal fails, the operation is already InProgress, and a `true` on an operation that is not waiting on a failure is removed on the next reconcile without running anything, so the step is never run twice for one request. Any value other than `true` is ignored and left in place. `status.lastRetry` stays (CRD fields are append-only) and is still written: it is the time of the last retry for display and audit, and the Cilium upgrade uses it to name the Job of a retried attempt.
