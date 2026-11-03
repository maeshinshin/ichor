# 0116. Patch upgrades can be applied automatically when a setting allows it

Status: Accepted · Date: 2026-11-04 · Design section: Upgrades · Touches: 0043, 0057, 0059, 0095, 0099, 0105, 0114, 0115

Decided: `IchorInstallation.spec.autoPatch` (and the chart's `autoPatch`, three controller flags) is a setting that is off by default; when it is on, a leader-only controller loop, once a minute, starts at most one upgrade `TalosOperation` per cluster for the newest patch of the running minor of Talos, then Cilium, then Kubernetes, taken only from the candidates the release check recorded in `status.upgradeTargets` and filtered by the compatibility table, only inside the configured daily UTC window, only while every approved machine is Ready and nothing else holds the cluster; the operation carries the annotation `ichor.maesh.dev/auto-patch: "true"` and `started-by: ichor-auto-patch` and runs through the normal executor; a failed or paused automatic operation halts the patcher (condition `AutoPatchHalted`, `status.autoPatch.haltedOperation`) until a person writes `spec.autoPatch.acknowledgedHalt`, or the operation completes or is deleted, or a later manual upgrade of that cluster completes; the patcher never retries. The one write to another CR's spec is a narrow, stated exception to "the controller never mutates `spec`": the version of a Talos or Kubernetes patch goes into the `TalosCluster` spec (the executors and the renderer read it there), and the admission webhook refuses any other write by the controller to a `TalosCluster`.

## Context

The user decided on 2026-11-04 that patch upgrades may be applied automatically when a setting allows it. ADR 0114 made the controller list releases, ADR 0115 made each component upgrade on its own against one compatibility table and put the candidate lists in `status.upgradeTargets` (Talos, Kubernetes, and the Cilium patches whose images verified). Today a person starts each of those operations in the UI or CLI. The architecture says the controller never mutates `spec` and that the UI and CLI change the world by writing CRs. This ADR keeps the rule in as many places as it can and names the one place it cannot.

## Decision

### 1. The setting

```yaml
spec:
  autoPatch:
    enabled: true                         # unset falls back to --auto-patch (chart autoPatch.enabled), default false
    components: [talos, cilium, kubernetes]  # unset or empty means all three
    window: "02:00-05:00"                 # daily UTC window, may wrap midnight (22:00-02:00); unset means any time
    acknowledgedHalt: ""                  # see 5
```

All fields are optional and append-only. A set `spec.autoPatch` wins field by field over the flags (`--auto-patch`, `--auto-patch-components`, `--auto-patch-window`, from the chart's `autoPatch.enabled`, `components`, `window`); `--settings-read-only` ignores the whole spec group exactly as for the other settings (ADR 0105, 0114). `status.effective` gains `autoPatch`, `autoPatchComponents` and `autoPatchWindow`. Changing the first three needs `update` on `ichorinstallations/settings`, as the other settings do, and leaves the usual `SettingsChanged` Event; `ichor settings set autoPatch.enabled=true autoPatch.components=talos,cilium autoPatch.window=02:00-05:00` and the Settings page (a Switch, one Checkbox per component, an Input for the window) write it through SettingsService. The webhook and the CRD both reject a malformed window or an unknown component.

### 2. What it may start

Only a patch: the same minor as what runs, newer, a final release. The candidate is the newest of `release.TalosChoices`, `release.KubernetesChoices` or `release.CiliumChoices` (the choices the UI offers, built from `status.upgradeTargets` and the table of ADR 0115) that has the running minor. So it is never a minor, never a version only `--override` would pass, never a version the release check did not list, and for Cilium only an image set whose signatures the check verified. Kubernetes needs the Cilium the cluster runs to be known (nothing is compatible with an unknown one), exactly as in the UI. The unmanaged CNI preset has no Cilium patch.

The order is Talos, Cilium, Kubernetes, one component per decision. Kubernetes comes last because its supported range is limited by the other two; a patch never changes a minor, so no component's patch makes another's candidate invalid. After one operation completes, the next minute's decision picks the next component.

### 3. When

Every guard is a field of the pure function `autopatch.Decide`, with a table test each:

1. the setting is on, and not halted;
2. no Ichor upgrade is in progress (`status.installedVersion` is `spec.version`);
3. now (UTC) is inside the window;
4. the release lists exist and are younger than 48 hours (the check runs every six hours, so older means it is failing or off);
5. no TalosOperation of the cluster is unfinished (running, paused or pending; a node shutdown resting in `ShutDown` does not count), and the cluster lock (the Lease of ADR 0043) is not held;
6. the cluster has approved machines and every one is Ready (a machine with a pending configuration change is not Ready, so a pending apply blocks it);
7. no operation was started by the patcher on this cluster in the last two minutes (the guard for reads that have not caught up; the patcher reads without the cache as well).

### 4. How it runs: its own loop

`controller.AutoPatcher` is a leader-only Runnable that ticks every minute with jitter. It does not reuse the release check's loop: that one runs every six hours and the window is a time of day, so reusing it would miss most windows or force the check to run every minute. It does not hang off the TalosCluster reconcile either: that reconcile is per cluster and event-driven, while the halt state is global and the setting is read once. The release check stays the only thing that reaches a registry; the patcher only reads what it wrote. While the setting is off and nothing is halted the tick returns after reading the settings.

A start, for Talos or Kubernetes: the operation's creation is dry-run first, then the version is written into the cluster's spec (compare-and-set: only if the field still holds what was decided on, so a person's edit in between is not overwritten), then the operation is created. This is the order the UI uses because the executors read the target from the spec when a machine's turn comes. For Cilium the operation carries the target itself (`spec.cni.chartVersion` and `imageVersion`, as StartCniUpgrade does). The operation gets `ichor.maesh.dev/from-version`, `ichor.maesh.dev/started-by: ichor-auto-patch` and `ichor.maesh.dev/auto-patch: "true"`; the UI's operation page shows "Started by: Automatic patching". There is no spec field for the starter: the annotations are what the audit trail and the UI read, as for `started-by` today. Each start emits a Normal Event `AutoPatchStarted` on the TalosCluster and records `status.autoPatch.lastOperation`. If the version was written but the operation could not be created in three tries, the patcher halts and says so; it does not leave the half-start unseen.

### 5. Halt and resume

An automatic operation (by the annotation) in phase Paused, which is how a failed step and a person's pause both show (ADR 0095), halts the patcher: `status.conditions[AutoPatchHalted]=True` with the operation and its failure reason, `status.autoPatch.haltedOperation: <namespace>/<name>`, a Warning Event `AutoPatchHalted`, and an item in the Overview's attention list. While halted nothing starts on any cluster, and nothing is ever retried by the patcher: running a failed step again stays `ichor operation resume` (ADR 0095).

The halt ends when:

- a person writes `spec.autoPatch.acknowledgedHalt` equal to `haltedOperation` (the Settings page's "Clear the halt" button, `ichor settings set autoPatch.acknowledgeHalt=<namespace>/<name>`). The controller never edits spec, so it only compares; the value is not removed, and a later halt names another operation, so an old acknowledgement does not clear it. This is allowed under `--settings-read-only` too (still needing `update` on `ichorinstallations/settings`): it clears a state Ichor wrote, it is not one of the deployment's settings;
- the halting operation completed (a person resumed it) or no longer exists (deleted);
- a later operation of the same cluster that the patcher did not start, of kind talos, kubernetes or cni, completed.

An acknowledged halt does not unblock a cluster: a paused operation still holds it (guard 5) until a person resumes or deletes it.

### 6. Where the rule "the controller never mutates spec" bends

The brief was that the world changes through a TalosOperation only. For Cilium that is exact: the operation carries the target. For Talos and Kubernetes it cannot be: the target version is `TalosCluster.spec.talosVersion` / `kubernetesVersion`, which the executors (`talos_upgrade.go`, `kubernetes_upgrade.go`) read, and which the renderer reads for the machine configuration of every node (`render.go`). An operation that carries its own version would leave the spec behind the nodes, and the next reconcile would render the old version back. Writing the spec is therefore the controller's only way, and the user's consent for it is the setting.

It is limited, three ways. It is a stated exception like ADR 0105 decision 8's seed. The controller's RBAC gains `update` on `talosclusters` (nothing else: no `patch`, no create, no delete), generated from the marker in `internal/controller/autopatch.go`. And `TalosClusterCustomValidator` refuses, for the controller's own username only, any update except one that changes `spec.talosVersion` or `spec.kubernetesVersion` to a newer patch of the same minor with labels, annotations, finalizers and every other spec field unchanged. A bug in the patcher therefore cannot edit a cluster's configuration, only move a version by a patch, and the compatibility webhook and both executors check the step again. A GitOps tool that owns the TalosCluster will see the version drift and revert it, so a deployment that keeps clusters in Git should leave auto-patch off (the default), or write the patch itself.

### 7. Not decided here

- A separate maintenance window per cluster, or a per-cluster switch: the setting is installation-wide. A cluster wanting to opt out keeps the hold annotation on every machine (ADR 0043), which the executors honour.
- Unsetting a field of `spec.autoPatch` from the UI or CLI (the same gap as the other settings, ADR 0105). An empty window written through the UI falls back to the chart's.
- Auto-applying anything else: minors, Ichor itself, and Cilium chart upgrades stay manual.

## Consequences

- Off by default; nothing changes for an installation that does not turn it on. The CRD gains optional fields only (append-only), the chart a value group and three flags, the proto optional fields.
- The controller's ClusterRole gains `update` on `talosclusters`; the webhook narrows it to a patch bump. The chart's `manager-role` and `config/rbac/role.yaml` carry it.
- The release check must be on for there to be anything to apply; with it off `status.upgradeTargets` is cleared and the patcher starts nothing.
- `ichor settings get` and the Settings page show the effective switch, components and window, and the halted operation.
- Tests: table tests of `autopatch.Decide` for every guard and of `autopatch.Resume`; an envtest of `AutoPatcher` for start (annotations, spec bump, no second start), the guards that need a cluster, halt on a paused operation, resume by acknowledgement and by a later manual operation; the webhook tests of the controller's limited write and of the settings gate (including read-only and the acknowledgement); the uiapi tests of the new fields; the UI's pure tests of the form.
