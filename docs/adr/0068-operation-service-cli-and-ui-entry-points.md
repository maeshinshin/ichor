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
error). The message says what was done; a retry finishes it.

A tracked spec (ADR 0051) is refused with a message saying to merge the change in the repository and then start the
operation with the version the spec now names, which is a start without a spec write. Opening a pull request for
the spec change and creating the operation later is not done here (below).

## Decision 3: the kube-proxy confirmation annotation

ADR 0013's `ValidatingAdmissionPolicy` denies a `spec.cni.kubeProxy` change without
`ichor.maesh.dev/confirm-kube-proxy-switch`. `CreateOperation` for `kind: cni` sets the annotation in the same
`Update` as the field, then removes it in a second `Update` at once, before creating the operation. The
confirmation is the user's, given in the CLI prompt or the UI dialog; leaving the annotation behind would turn it
into a standing consent for a later hand edit. If the removal fails the call stops with a message that names the
annotation (the mode is already changed, no operation exists; remove the annotation and start again with the
mode already in the spec). `--yes` / the dialog's Start is the confirmation; there is no way to set the
annotation without the switch.

## Decision 4: permissions

Every `Get/List/Create/Update/Delete` in `operation_service.go` is `s.X`, the shadowed calls that go through the
impersonated client when a caller identity exists (ADR 0058), so Kubernetes RBAC decides who may list, start,
pause and delete operations and edit the cluster spec, and the API server's audit log names the user. The only
`s.Client` use is the tracking check, which decides whether a write may happen and returns nothing to the caller
(ADR 0062's rule). `AuditRecord.actor` is the user, or `ichor-cli` on the fallback. The ServiceAccount's ClusterRole
gets `talosclusters update`, `talosoperations get/list/create/update/delete` and `ichorinstallations get/update`
(markers, `make manifests-ichor-ui`, the chart template); with OIDC on they are not used for these calls, with
OIDC off they are what "anyone who reaches the UI may do anything" (ADR 0058, point 4) means. The unit tests
inject a client that refuses `Create`/`Update`/`Delete` as the caller and assert `PermissionDenied` and that nothing
was written.

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
order that runs. `TestPlanOperationOrderMatchesTheExecutorsCandidateOrder` holds the two together. The plan had first
sorted a shutdown by name alone; the executor orders it by the same label, so the plan now does too.

## Decision 6: commands

`ichor upgrade talos|kubernetes|ichor VERSION`, `ichor cni kube-proxy replaced|enabled`, `ichor cluster shutdown
NAME`, `ichor operation list|get|pause|resume|delete` (alias `op`). The version may be given without the leading
`v`. `--cluster` (default: the only cluster in the namespace), `--max-unavailable`, `--follow` (poll until it ends;
a pause caused by a failed machine is an error exit), `get --watch`. The start, pause, resume and delete commands
are marked as writes for the version-skew check (ADR 0059, point 6). Scaffolded with `cobra-cli add` in a scratch
module and moved (ADR 0014). `ichor upgrade ichor` also names a cluster: the operation backbone needs one for its
lock, though the target is `IchorInstallation.spec.version`.

## UI

`/operations` (list: kind, cluster, state, progress, pause/resume/delete) and `/operations/:namespace/:name` (the
state of each machine, the failure reason with the statement that nothing retries and nothing is rolled back). The
start buttons are on the cluster page. All wording is in `en`/`ja` (the type forces both). Pure logic is in
`ui/src/lib/operations.ts` with tests.

## Not done

- Opening a pull request for the spec change of a tracked cluster (PR mode, ADR 0056) and creating the operation
  after the merge. Today the CLI/UI refuses and says to change Git first.
- Break-glass for these writes (ADR 0064).
- A stream instead of polling; a per-machine log of what the executor is doing (the status has phase only).
- Clearing a failed machine so the operation resumes (ADR 0026 left that open); the screens say to delete and
  start again.
- No end-to-end run against a cluster; the service is tested over a fake client and the CLI over both the
  in-process and the httptest backends.
