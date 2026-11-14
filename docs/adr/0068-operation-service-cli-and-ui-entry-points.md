# 0068. The UI and CLI start, follow, pause and delete TalosOperations through one OperationService, and write the spec first

Status: Accepted · Date: 2026-10-26 · Design section: Upgrades, CLI and UI

Decided: an `OperationService` (list, get, plan, create, pause/resume, delete) is the one entry point for `TalosOperation`s, used by `ichor upgrade|cni|cluster shutdown|operation` and the UI; every call is made as the signed-in user, `CreateOperation` changes the spec first and creates the operation second after refusing everything it can beforehand (including a dry-run create as the caller), the kube-proxy confirmation annotation is set for that one write and removed again, destructive starts show the server's plan and ask for confirmation (a shutdown also needs the cluster name typed in the UI)

## Context

The executors for `talos` (0043), `kubernetes` (0057), `ichor` (0059), `cni` (0066) and `shutdown` (0067) existed,
but nothing could start one: no code in `internal/cli` touched `TalosOperation`, `proto/` had no RPC for it and the UI
had no screen. The design promises a progress view shared by the three upgrades and a CLI/UI that both write CRs
through the same Connect API (the CLI falling back to the same handlers in-process, ADR 0049).

## Decision 1: one service, the same handler on both paths

`proto/ichor/v1alpha1/operation.proto` adds `OperationService`: `ListOperations`, `GetOperation`, `PlanOperation`,
`CreateOperation`, `SetOperationPaused`, `DeleteOperation`. `uiapi.Server` implements it; the CLI's in-process
fallback (`localClients`) uses the same `uiapi.Server`, so every refusal below runs on both paths. Progress is
`TalosOperation.status` as it is (`phase`, `failureReason`, the machines with their phases); nothing is derived or
guessed server-side. There is no stream: `get --watch` and the UI poll (2 s in the CLI; 3 s running, 10 s paused,
stop on completed or failed in the UI). The status is a few fields and a poll is one Get.

## Decision 2: which comes first, the spec change or the operation

**The spec first, then the operation.** The executors take their target from the spec when a machine's turn comes
(`TalosCluster.spec.talosVersion` / `kubernetesVersion` / `cni.kubeProxy`, `IchorInstallation.spec.version`; ADR 0059
says outright that the `ichor` operation carries no version). An operation created first would run against the old
value and finish having done nothing, while the cluster lock was held; and the spec would then change under no
operation, which is the state the controller treats as drift.

The risk of the other order is that the second write fails after the first succeeded. What that leaves is
harmless: a spec that names a new version with no operation is only drift (the controller dry-runs it, it does not
apply a reboot-requiring change), and running the command again is idempotent because a spec that already names
the target is not written twice. To make that failure rare, `CreateOperation` refuses before the first write:

1. the plan's blockers: another operation on the cluster that has not `Completed` (the lock would refuse it
   anyway, but only after the spec had changed), and a target spec that Argo CD or Flux tracks;
2. a dry-run `Create` of the operation made as the caller (`client.DryRunAll`), which exercises RBAC and
   admission for creating operations before the spec is touched. A caller who may edit a cluster but may not
   create operations changes nothing.

Left: the spec update succeeding and the real `Create` failing (a race with another operation, an apiserver
error). The message says what was done; a retry finishes it. A failed first attempt thus leaves the spec already
naming the new version, and starting the command again creates a new operation without rewriting the spec.

A tracked spec (ADR 0051) is refused with a message saying to merge the change in the repository and then start the
operation with the version the spec now names, which is a start without a spec write. Opening a pull request for
the spec change and creating the operation later is not done here (below).

## Decision 3: the kube-proxy confirmation annotation

ADR 0013's `ValidatingAdmissionPolicy` denies a `spec.cni.kubeProxy` change without
`ichor.maesh.dev/confirm-kube-proxy-switch`. `CreateOperation` for `kind: cni` sets the annotation in the same
`Update` as the field (retried on a conflict with a fresh read), creates the operation, then removes it in a second `Update` (amended 2026-10-29, ADR 0066). The
confirmation is the user's, given in the CLI prompt or the UI dialog; leaving the annotation behind would turn it
into a standing consent for a later hand edit. If the removal fails the call reports it after the operation exists (naming the
annotation; remove it by hand). `--yes` / the dialog's Start is the confirmation; there is no way to set the
annotation without the switch.

## Decision 4: permissions

Every `Get/List/Create/Update/Delete` in `operation_service.go` is `s.X`, the shadowed calls that go through the
impersonated client when a caller identity exists (ADR 0058), so Kubernetes RBAC decides who may list, start,
pause and delete operations and edit the cluster spec, and the API server's audit log names the user. The only
`s.Client` use is the tracking check, which decides whether a write may happen and returns nothing to the caller
(ADR 0062's rule). `AuditRecord.actor` is the user, or `ichor-cli` on the fallback. The ServiceAccount's ClusterRole
gets `talosclusters update`, `talosoperations get/list/create/update/delete` and `ichorinstallations get/update`
(markers, `make manifests-ichor-ui`, the chart template); with OIDC on they are not used for these calls, with
OIDC off they are what "anyone who reaches the UI may do anything" (ADR 0058, point 4) means.  A write the caller's RBAC refuses returns `PermissionDenied` and
nothing is written.

## Decision 5: confirmation and the plan

`PlanOperation` returns, without writing: the effect (`REBOOT`, `RESTART_COMPONENTS`, `STOP`, `NONE`), the
approved machines in the order they will be handled and in steps (control planes one at a time, then workers in
groups of `maxUnavailable`, label order as the controller sorts them; for a shutdown the workers together, then the
control planes), the machines skipped by the hold annotation, the current and target values and a `blocked` reason.
The CLI (`planText`) and the UI build their confirmation from it, so both say how many machines, in which order and
whether they reboot, stop or only restart components. Destructive commands confirm unless `--yes` (`confirm`, as
`machine approve`); with `--yes` the plan is not asked for (the create does the same refusals). The UI's dialog is
two steps, "Review" (which calls the plan and writes nothing) and a separate "Start"; a shutdown also asks for the
cluster name to be typed.

Two shapes were weighed for the plan: computing it in the client from `ListMachines` (duplicates the controller's
ordering in TypeScript and Go) or one server function. The ordering is one function, `upgrade.SortByOrder` in
`pkg/upgrade`, called by the executor (rollouts and shutdown) and by the plan, so the order a person confirms is the
order that runs. A shutdown is sorted by the same label in the plan and
in the executor.

## Decision 6: commands

`ichor upgrade talos|kubernetes|ichor VERSION`, `ichor set kube-proxy replaced|enabled`, `ichor cluster shutdown
NAME`, `ichor operation list|get|pause|resume|delete` (alias `op`). The version may be given without the leading
`v`. `--cluster` (default: the only cluster in the namespace), `--max-unavailable`, `--follow` (poll until it ends;
a pause caused by a failed machine is an error exit), `get --watch`. The start, pause, resume and delete commands
are marked as writes for the version-skew check (ADR 0059, point 6).  `ichor upgrade ichor` also names a cluster: the operation backbone needs one for its
lock, though the target is `IchorInstallation.spec.version`.

## Decision 7: the `ca-rotation` kind (ADR 0063)

*Amended by ADR 0091: the stages gain `AwaitingCredentials`, `rotate-ca` performs the credential hand-off after the start, and the plan says the operator's credentials stop working.*

`OperationKind` gains `OPERATION_KIND_CA_ROTATION`, `OperationEffect` gains `OPERATION_EFFECT_CA_ROTATION`, `Operation` carries
`ca_rotation_stage` (`status.caRotation.stage`, empty before the new CAs are saved), and `PlanOperationResponse` carries `stages`
(`Accepting`, `Issuing`, `Dropping`) and `replaces_secrets_bundle`. The rest is the discipline of the other kinds: `CreateOperation`
writes as the caller, refuses while an operation on the cluster is unfinished, runs the dry-run create first, and takes no target
(a target is `InvalidArgument`) and writes no spec, so a tracked cluster is not refused (nothing in the spec changes).

The plan lists every approved machine, control planes first and then workers, one at a time, in the executor's order
(`upgrade.SortByOrder`). A machine with `upgrade-hold` is not skipped and `held` is empty: a machine left out would be locked out
when the old CA is dropped (ADR 0063). The confirmation says that each machine is reconfigured once per pass without a reboot, that
the kubelet restarts on each machine in the passes that change the Kubernetes CA, and that when the rotation completes the secrets
bundle in the cluster is replaced and the copy on the user's machine is stale: run `ichor cluster bundle-export NAME`. The CLI and
the UI build this from the same plan fields; the CLI also prints the bundle-export reminder after the start, and `--follow` and
`operation get --watch` print the stage.

`ichor cluster rotate-ca` now calls `runStartOperation` like `ichor cluster shutdown`; the earlier direct write
through the kubeconfig (and its own already-running check) is removed, so the same refusals run on the CLI, the UI and the
in-process fallback. The UI dialog is the two-step Review/Start of the other kinds and, like a shutdown, asks for the cluster name to
be typed before Start. The progress page shows "Pass n of 5: <stage>" from `caStageProgress`.

Not done: the operation page does not read the cluster's `BundleBackupStale` condition (a completed rotation says what to run);
the stage is shown, not a per-machine view inside a stage (`status.machines` already shows the stage under way).

## UI

`/operations` (list: kind, cluster, state, progress, pause/resume/delete) and `/operations/:namespace/:name` (the
state of each machine, the failure reason with the statement that nothing retries and nothing is rolled back). The
start buttons are on the cluster page. All wording is in `en`/`ja` (the type forces both). Pure logic is in
`ui/src/lib/operations.ts`.

## Not done

- Opening a pull request for the spec change of a tracked cluster (PR mode, ADR 0056) and creating the operation
  after the merge. Today the CLI/UI refuses and says to change Git first.
- Override for these writes (ADR 0064).
- A stream instead of polling; a per-machine log of what the executor is doing (the status has phase only).
- Clearing a failed machine so the operation resumes (ADR 0026 left that open); the screens say to delete and
  start again. `operation resume` on an operation paused after a failed machine is accepted but has no effect.

## Later changes

- `--follow` / `--watch` (one shared helper, `retryTransient`) retry transient API errors (connection refused/reset, Unavailable, EOF, timeouts) with backoff for up to 10 minutes, printing `API unreachable, retrying`, and re-open a dead ichor-ui tunnel. NotFound, PermissionDenied and the like still end the command.
- `PlanOperation` blocks a version target older than the current one (Talos, Kubernetes, Ichor), so `CreateOperation` never writes a downgrade into the spec before the executor refuses it.

## Amendment (2026-11-04): the service refuses early, the controller serializes

`blockedReason` / `unfinishedOperation` (Decision 2, point 1) only guard operations created through `OperationService`. The controller now keeps operations apart by itself: each takes the cluster lock under its own identity, a second one waits with `ClusterLockHeld` naming the holder, and a lease held by an operation that is gone, `Completed` or paused on a failed step is taken over at once (ADR 0026, amendment 2026-11-04). The service's refusal stays, since it stops the spec from being written for an operation that cannot start. The admission webhook deliberately does not reject a second operation, so operations created by `kubectl` or GitOps queue instead of failing.
