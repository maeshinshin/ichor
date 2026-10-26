# 0060. A TalosMachine requeues a failure that time can fix, and only that

Status: Accepted · Date: 2026-10-26 · Design section: Nodes
Decided: `TalosMachineReconciler` returns `ctrl.Result{RequeueAfter: transientRecheck}` (30s, one constant beside `overCapRecheck`) with a nil error after writing Ready=False for a cause that time can fix; a cause that waits for an operator returns an empty Result; the status write is the report and the interval is fixed, so no error is returned for backoff

## Context

`Reconcile` ended fourteen branches with `return ctrl.Result{}, r.ready(...)`. `ready` returns nil once status is
written, so controller-runtime never visited the machine again: this controller watches only TalosMachine and
TalosStorage, and `cmd/main.go` sets no `SyncPeriod`, so the next visit was another event or the 10 hour default
resync. After a whole-cluster restart where the controller starts before the nodes, every machine stayed on
`CurrentConfigUnavailable` (or `TalosConnectFailed`) for up to 10 hours with the node long since up. Nothing
destructive happened: a machine that cannot read the node stops before the drift decision and before any apply.
That stays true.

## Decision

Every Ready=False branch is one of two kinds. TalosCluster, TalosConfigPatch and Secrets are not watched, so
"an operator will fix it" is only a reason not to poll when the fix is an edit to the TalosMachine itself, or when
the state needs a person (reboot, reinstall, a policy change) and polling would change nothing.

| Reason | Cause | Kind | Requeue |
|---|---|---|---|
| `ClusterNotFound` | TalosCluster created later; not watched | time | yes |
| `SecretsBundleUnavailable` | Secret created or synced later; not watched | time | yes |
| `RenderFailed` (render, hash, `versionContract`) | patch, cluster or registry Secret edited; none watched | time (see below) | yes |
| `TalosConnectFailed` | node down or booting | time | yes |
| `CurrentConfigUnavailable` | node API not up yet | time | yes |
| `ConfigDiffFailed` | node returned unreadable config, possibly mid-boot | time (see below) | yes |
| `DryRunFailed` | node API error; dry run has no side effect | time | yes |
| `ApplyFailed` | node busy or restarting | time | yes |
| `ClusterTransitioning` | clears when a sibling control plane finishes; nothing enqueues this machine | time | yes |
| `ClusterLockHeld` | released lease enqueues nothing | time | yes |
| `ControlPlaneOverCap` | already polls (`overCapRecheck`, ADR 0054) | time | yes (existing) |
| `TalosClientNotConfigured` | controller wiring, fixed by redeploy (restart re-lists everything) | operator | no |
| `UnsupportedTalosVersion` | needs a newer pinned contract | operator | no |
| `InstallTimeOnlyChange` | needs reset and reinstall | operator | no |
| `RebootRequired` | this phase does not reboot nodes | operator | no |
| `NodeDrifted` | `driftPolicy: warnOnly` asks to be left alone | operator | no |

Two were close calls. `RenderFailed` and `ConfigDiffFailed` are deterministic for fixed inputs, so a bad patch
fails the same way every 30 seconds. They requeue because their inputs are exactly the objects that are not
watched, so without polling a corrected patch or cluster would not be seen for 10 hours; the cost is one render
per 30 seconds per broken machine, with the status message unchanged (so no status churn) and no node call for
the render cases. Watching TalosCluster, TalosConfigPatch and the Secret would remove the need and is the better
long-term fix; it is a larger change (map functions, RBAC for Secret watch) left for later.

Success (`Converged`, `Applied`, `DriftCorrected`) does not requeue: detecting later drift on a converged node is
a separate decision and not part of this fix.

## Result or error

Returning the error would use the work queue's per-item exponential backoff (5ms doubling to 1000s,
controller-runtime v0.24 default), which is the wrong shape here: a restarted cluster would wait up to ~17 minutes
per retry, the log would carry a stack for a state the status already reports, and the error return would hide
that the status write succeeded. `RequeueAfter` with a nil error is the documented way to poll
(`reconcile.Result`), uses one constant, and matches `overCapRecheck`, `handoverRequeueInterval` and
`operationRequeueInterval`. If the status write itself fails the error is returned, so backoff applies then.

## Other controllers

Checked `talosstorage`, `taloscluster`, `talosetcdbackup`, `cnihandover` and `talosoperation`. None has the shape:
each already returns `RequeueAfter` for waiting states (storage 2m, discovery 5m, backup `backupRetryInterval`,
handover 30s, operation 30s for lock and transition) and returns a real error otherwise. The deliberate no-requeue
cases (`TalosOperation` paused or failed step, suspended or invalid-schedule backup, handover `disabled` or
`manual`) wait for a spec edit, which is an event.

Update (ADR 0061): reaching an already converged machine is the watch's job; the requeue here is for failures only.
