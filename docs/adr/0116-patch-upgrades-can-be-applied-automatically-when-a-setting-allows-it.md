# 0116. Patch upgrades can be applied automatically when a setting allows it

Status: Accepted · Date: 2026-11-04 · Design section: Upgrades · Touches: 0043, 0057, 0059, 0095, 0099, 0105, 0114, 0115

Decided: `IchorInstallation.spec.autoPatch` (and the chart's `autoPatch`, three controller flags) is a setting that is off by default; when it is on, a leader-only controller loop, once a minute, starts at most one upgrade `TalosOperation` per cluster for the newest patch of the running minor of Talos, then Cilium, then Kubernetes, taken only from the candidates the release check recorded in `status.upgradeTargets` and filtered by the compatibility table, only inside the configured daily UTC window, only while every approved machine is Ready and nothing else holds the cluster; the operation carries the annotation `ichor.maesh.dev/auto-patch: "true"` and `started-by: ichor-auto-patch` and runs through the normal executor; a failed or paused automatic operation halts the patcher (condition `AutoPatchHalted`, `status.autoPatch.haltedOperation`) until a person writes `spec.autoPatch.acknowledgedHalt`, or the operation completes or is deleted, or a later manual upgrade of that cluster completes; the patcher never retries. The controller itself never writes a `TalosCluster` spec: the version of a Talos or Kubernetes patch, and the operation, are written by a separate identity, the ServiceAccount `ichor-auto-patch`, which the controller impersonates (a namespaced Role that names that one ServiceAccount); the account holds only `get` and `update` on `talosclusters` and `create` on `talosoperations`, and the admission webhook refuses any write of it except a newer patch of the same minor in those two fields, and any spec write by the controller's own username. The audit log shows the writes as `system:serviceaccount:<namespace>:ichor-auto-patch`.

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

All fields are optional and append-only. A set `spec.autoPatch` wins field by field over the flags (`--auto-patch`, `--auto-patch-components`, `--auto-patch-window`, from the chart's `autoPatch.enabled`, `components`, `window`); `--settings-read-only` ignores the whole spec group exactly as for the other settings (ADR 0105, 0114). `status.effective` gains `autoPatch`, `autoPatchComponents` and `autoPatchWindow`. Changing the first three needs `update` on `ichorinstallations/settings`, as the other settings do, and leaves the usual `SettingsChanged` Event; `ichor set settings autoPatch.enabled=true autoPatch.components=talos,cilium autoPatch.window=02:00-05:00` and the Settings page (a Switch, one Checkbox per component, an Input for the window) write it through SettingsService. The webhook and the CRD both reject a malformed window or an unknown component.

### 2. What it may start

Only a patch: the same minor as what runs, newer, a final release. The candidate is the newest of `release.TalosChoices`, `release.KubernetesChoices` or `release.CiliumChoices` (the choices the UI offers, built from `status.upgradeTargets` and the table of ADR 0115) that has the running minor. So it is never a minor, never a version only `--override` would pass, never a version the release check did not list, and for Cilium only an image set whose signatures the check verified. Kubernetes needs the Cilium the cluster runs to be known (nothing is compatible with an unknown one), exactly as in the UI. The unmanaged CNI preset has no Cilium patch.

The order is Talos, Cilium, Kubernetes, one component per decision. Kubernetes comes last because its supported range is limited by the other two; a patch never changes a minor, so no component's patch makes another's candidate invalid. After one operation completes, the next minute's decision picks the next component.

### 3. When

Every guard is a field of the pure function `autopatch.Decide`:

1. the setting is on, and not halted;
2. no Ichor upgrade is in progress (`status.installedVersion` is `spec.version`);
3. now (UTC) is inside the window;
4. the release lists exist and are younger than 48 hours (the check runs every six hours, so older means it is failing or off);
5. no TalosOperation of the cluster is unfinished (running, paused or pending; a node shutdown resting in `ShutDown` does not count), and the cluster lock (the Lease of ADR 0043) is not held;
6. the cluster has approved machines and every one is Ready (a machine with a pending configuration change is not Ready, so a pending apply blocks it);
7. no operation was started by the patcher on this cluster in the last two minutes (the guard for reads that have not caught up; the patcher reads without the cache as well).

### 4. How it runs: its own loop

`controller.AutoPatcher` is a leader-only Runnable that ticks every minute with jitter. It does not reuse the release check's loop: that one runs every six hours and the window is a time of day, so reusing it would miss most windows or force the check to run every minute. It does not hang off the TalosCluster reconcile either: that reconcile is per cluster and event-driven, while the halt state is global and the setting is read once. The release check stays the only thing that reaches a registry; the patcher only reads what it wrote. While the setting is off and nothing is halted the tick returns after reading the settings.

A start, for Talos or Kubernetes: the operation's creation is dry-run first, then the version is written into the cluster's spec as ichor-auto-patch (compare-and-set: only if the field still holds what was decided on, so a person's edit in between is not overwritten), then the operation is created, also as ichor-auto-patch. This is the order the UI uses because the executors read the target from the spec when a machine's turn comes. For Cilium the operation carries the target itself (`spec.cni.chartVersion` and `imageVersion`, as StartCniUpgrade does). The operation gets `ichor.maesh.dev/from-version`, `ichor.maesh.dev/started-by: ichor-auto-patch` and `ichor.maesh.dev/auto-patch: "true"`; the UI's operation page shows "Started by: Automatic patching". There is no spec field for the starter: the annotations are what the audit trail and the UI read, as for `started-by` today. Each start emits a Normal Event `AutoPatchStarted` on the TalosCluster and records `status.autoPatch.lastOperation`. If the version was written but the operation could not be created in three tries, the patcher halts and says so; it does not leave the half-start unseen.

### 5. Halt and resume

An automatic operation (by the annotation) in phase Paused, which is how a failed step and a person's pause both show (ADR 0095), halts the patcher: `status.conditions[AutoPatchHalted]=True` with the operation and its failure reason, `status.autoPatch.haltedOperation: <namespace>/<name>`, a Warning Event `AutoPatchHalted`, and an item in the Overview's attention list. While halted nothing starts on any cluster, and nothing is ever retried by the patcher: running a failed step again stays `ichor resume operation` (ADR 0095).

The halt ends when:

- a person writes `spec.autoPatch.acknowledgedHalt` equal to `haltedOperation` (the Settings page's "Clear the halt" button, `ichor set settings autoPatch.acknowledgeHalt=<namespace>/<name>`). The controller never edits spec, so it only compares; the value is not removed, and a later halt names another operation, so an old acknowledgement does not clear it. This is allowed under `--settings-read-only` too (still needing `update` on `ichorinstallations/settings`): it clears a state Ichor wrote, it is not one of the deployment's settings;
- the halting operation completed (a person resumed it) or no longer exists (deleted);
- a later operation of the same cluster that the patcher did not start, of kind talos, kubernetes or cni, completed.

An acknowledged halt does not unblock a cluster: a paused operation still holds it (guard 5) until a person resumes or deletes it.

### 6. Who writes the version: a delegated identity, not the controller

The target of a Talos or Kubernetes upgrade is `TalosCluster.spec.talosVersion` / `kubernetesVersion`, which the executors (`talos_upgrade.go`, `kubernetes_upgrade.go`) read and the renderer (`render.go`) uses for every node's machine configuration. An operation that carried its own version would leave the spec behind the nodes, and the next reconcile would render the old version back. So the spec has to change, and the architecture says the controller never writes `spec` (principle 5). The rule is kept whole: the write is made by another identity acting for the user who turned the setting on.

- **`ichor-auto-patch`** is a ServiceAccount in the controller's namespace with a ClusterRole `ichor-auto-patch-role` of three rules: `get` and `update` on `talosclusters`, `create` on `talosoperations`. Nothing else, and nobody else is bound to it. It writes the version and creates the operation, so both are attributed to it. Config (`config/rbac/ichor-auto-patch/`) and chart (`templates/rbac/ichor-auto-patch.yaml`) are identical. Its objects carry the installer's label like every other installed object, so the prune of ADR 0111 treats them as any other; the installer's `bind`/`escalate` lists name the ClusterRole and the Role.
- **The controller impersonates it.** `AutoPatcher.Actor` is a client built with `Impersonate: system:serviceaccount:<ns>:ichor-auto-patch`. The controller's right is `impersonate` on `serviceaccounts` with `resourceNames: [ichor-auto-patch]` in a namespaced Role, so it can act as that one account in its own namespace and no other. Its own ClusterRole holds no `update` or `patch` on `talosclusters`. Cilium takes the same path for uniformity, though its operation carries the target itself.
- **The webhook limits both.** `TalosClusterCustomValidator` refuses any update of a `TalosCluster` spec by the controller's own username, and limits the `ichor-auto-patch` username to one change: `spec.talosVersion` or `spec.kubernetesVersion` to a newer patch of the same minor, with labels, annotations, finalizers and every other spec field unchanged. The compatibility webhook and both executors check the step again. A bug in the patcher therefore cannot edit a cluster's configuration.
- **A GitOps tool that owns the TalosCluster** will see the version drift and revert it, so a deployment that keeps clusters in Git should leave auto-patch off (the default).

### 7. Not decided here

- A separate maintenance window per cluster, or a per-cluster switch: the setting is installation-wide. A cluster wanting to opt out keeps the hold annotation on every machine (ADR 0043), which the executors honour.
- Unsetting a field of `spec.autoPatch` from the UI or CLI (the same gap as the other settings, ADR 0105). An empty window written through the UI falls back to the chart's.
- Auto-applying anything else: minors, Ichor itself, and Cilium chart upgrades stay manual.

## Consequences

- Off by default; nothing changes for an installation that does not turn it on. The CRD gains optional fields only (append-only), the chart a value group and three flags, the proto optional fields.
- A new ServiceAccount, ClusterRole and binding `ichor-auto-patch`, and a namespaced Role that lets the controller impersonate that account only; the controller's own ClusterRole is unchanged (`manager-role` equals `config/rbac/role.yaml` as before). The installer's escalation lists name the new Role and ClusterRole.
- The release check must be on for there to be anything to apply; with it off `status.upgradeTargets` is cleared and the patcher starts nothing.
- `ichor get settings` and the Settings page show the effective switch, components and window, and the halted operation.

## Amendment 2026-11-04: Cilium has no automatic patch

ADR 0115, amendment 2026-11-04 (3), removed the Cilium patches that run on the embedded chart, so there is nothing for the patcher to apply for Cilium: `autopatch.Candidate("cilium", ...)` returns nothing and `Cluster.CiliumChart` is gone. `cilium` remains an accepted value of `components` (stored settings and the chart defaults keep working) and selects no operation. The patcher's order is Talos, then Kubernetes. A Cilium patch reaches a cluster with an Ichor release; the chart upgrade stays a manual operation.
