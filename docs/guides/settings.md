# Settings

Ichor's runtime settings live in the cluster-scoped `IchorInstallation` named
`ichor` (ADR 0105). You edit them in the UI, with the CLI, or by writing the CR.
The controller, the webhooks and ichor-ui read them live: a change takes effect
on the next call, with no rollout.

| Setting | Field | Meaning |
|---|---|---|
| `users.tokenTTL` | `spec.users.tokenTTL` | Lifetime of tokens ichor-ui issues, 1m to 1h. |
| `users.refreshLifetime` | `spec.users.refreshLifetime` | Refresh lifetime, 1h to 720h; must not be shorter than the token lifetime. |
| `defaults.driftPolicy` | `spec.defaults.driftPolicy` | `warnOnly` or `autoCorrect`: the drift policy a new `TalosCluster` gets. |
| `updateCheck.enabled` | `spec.updateCheck.enabled` | Whether Ichor looks for a newer release and shows it on the dashboard. On by default; see [Updates](#updates). |

| `autoPatch.enabled` | `spec.autoPatch.enabled` | Apply patch releases of the running minor automatically. Off by default; see [Automatic patching](#automatic-patching). |
| `autoPatch.components` | `spec.autoPatch.components` | Which of `talos`, `cilium`, `kubernetes`; empty means all three. |
| `autoPatch.window` | `spec.autoPatch.window` | A daily UTC window `HH:MM-HH:MM` an upgrade may start in; empty means any time. |

A field that is set wins. A field that is not set falls back to the flag or
chart value (the ichor-ui token flags) or to `warnOnly`. There
is no way to unset a field from the UI or the CLI. `status.effective` on the
`IchorInstallation` shows each value in force and its source, `spec` or
`deployment`.

The default drift policy applies when a `TalosCluster` is created, through a
defaulting webhook. The webhook has `failurePolicy: Ignore`: if it is down, a
cluster is created with the field empty, which is read as `warnOnly`. A cluster
that exists is never changed.

Deployment facts (images, replicas, the TLS Secret, the external URL) are not
settings. They stay with the installer or Helm.

## In the UI

Open "Settings" in the sidebar. The page is a set of cards: Deployment (target
and running version, installer or Helm, read-only), Sign-in and Defaults. Each
value shows whether it is "Set here" or a "Deployment default". The page is read-only, with an explanation, when your role cannot change
settings or when the deployment manages them (below).

## With the CLI

```
ichor settings get
ichor settings set users.tokenTTL=15m defaults.driftPolicy=autoCorrect
```

`ichor settings get` shows each setting, the value in force and its source
(`spec` or `deployment`); `-o yaml` or `-o json` print the full response.

```
$ ichor settings get
SETTING                VALUE        SOURCE
version                v0.4.0       spec
installedVersion       v0.4.0       status
deployment.manager     helm         spec
users.tokenTTL         15m          spec
users.refreshLifetime  168h         deployment
defaults.driftPolicy   autoCorrect  spec
updateCheck.enabled    true         deployment
autoPatch.enabled      false        deployment
autoPatch.components   -            -
autoPatch.window       -            -
```

`ichor settings set` writes only the keys you name, and prints the table again
with the new values:

```
  users.tokenTTL=15m           token lifetime, 1m to 1h
  users.refreshLifetime=168h  refresh lifetime, 1h to 720h
  defaults.driftPolicy=warnOnly|autoCorrect   default for a new TalosCluster
  updateCheck.enabled=true|false              look for a newer Ichor release
  autoPatch.enabled=true|false                apply patch releases automatically
  autoPatch.components=talos,cilium           which components (empty: all)
  autoPatch.window=02:00-05:00                daily UTC window (empty: any time)
  autoPatch.acknowledgeHalt=NAMESPACE/NAME    clear a halt (below)
```

There is no switch for "Reset and add" (MachineReclaim) any more: `reclaim.enabled`
and `spec.reclaim` are deprecated and ignored (ADR 0107), `ichor settings set
reclaim.enabled=...` is refused, and the webhook warns when `spec.reclaim` is
set. Who may reset and add a machine is decided by RBAC alone; see
[Adopt and reclaim](adopt-and-reclaim.md). Each accepted change leaves an Event on the
`IchorInstallation` with the changed fields, old and new values and the user.

## Who may change what

Reading is part of `ichor-viewer`. Writing `ichorinstallations` stays with
`ichor-admin`; the webhook then asks Kubernetes whether the user may `update`
the subresource for each part of the change:

| Changing | Needs `update` on |
|---|---|
| `spec.version`, `spec.deployment` | `ichorinstallations/version` |
| `spec.users`, `spec.defaults`, `spec.updateCheck`, `spec.autoPatch` | `ichorinstallations/settings` |

`ichor-admin` holds both on separate rule lines, so a custom role can hold
fewer. The ichor-ui ServiceAccount holds `update` on `settings` for the users it
impersonates.

## Read-only mode

Set the controller flag `--settings-read-only` (chart value
`settings.readOnly: true`) when Git or Helm owns the configuration. The webhook
then denies writes to the settings fields, every consumer ignores whatever the
`IchorInstallation` holds, and the Settings page is read-only with a "managed by
the deployment" notice. The values come from the flags and chart values only.

## Updates

The controller lists the tags of the chart repository (`ghcr.io/maeshinshin/charts/ichor`) on start and every
six hours, and keeps the newest final release in `status.latestRelease` (ADR 0114). Prereleases and versions that
are not semver are ignored. The dashboard then shows "v<X> available" next to Upgrade on the Ichor row, and the
attention list names it. Nothing is installed by the check: Upgrade is still how Ichor moves, and a Helm install is
upgraded with `helm upgrade`. A dev build is never told about a release.

The request is one anonymous tag listing; it carries nothing about the cluster. Turning the check off (`ichor settings
set updateCheck.enabled=false`, or the chart's `updateCheck.enabled: false`, which passes the flag) stops the requests and
removes the controller's egress rule for them (`ichor-controller-release-check`). `status.latestRelease` keeps the
last result and its error: a failed check shows nothing on the dashboard, and Settings says why.

The CLI shows the same result. After a command that reaches a cluster, `ichor` prints one line on stderr when a
newer release is known, or when the `ichor` binary itself is older than it. It does this on a terminal only, at most
once a day per cluster, and not with `-o`. Set `ICHOR_NO_UPDATE_NOTICE=1` to turn the line off. The CLI makes no
request of its own: the line comes from the `IchorInstallation` the command already reads (ADR 0114).

## Automatic patching

Off by default (ADR 0116). When you turn it on (the Settings page's "Automatic patching" card, `ichor settings set
autoPatch.enabled=true`, or the chart's `autoPatch.enabled`), the controller starts upgrades to **patch releases of
the running minor** by itself: Talos, then Cilium, then Kubernetes, one component at a time and at most one
operation per cluster. It never moves to a new minor and never starts anything that would need `--override`.

It only offers what the release check listed (so the update check must be on), what the compatibility table accepts
for the cluster, and for Cilium only images whose signatures verified. It starts one only when all of this holds: it
is inside the window (UTC, `HH:MM-HH:MM`, may wrap midnight; no window means any time), every approved machine is
Ready, no operation of the cluster is unfinished, and the cluster lock is free. The operation is an ordinary
`TalosOperation` that the normal executor runs; it carries the annotation `ichor.maesh.dev/auto-patch: "true"`, shows
"Started by: Automatic patching" on its page, and each start leaves an `AutoPatchStarted` Event on the cluster.

For a Talos or Kubernetes patch the new version goes into the `TalosCluster` spec before the operation is created. The
controller never writes a spec: it impersonates the ServiceAccount `ichor-auto-patch`, which holds only `get` and
`update` on `talosclusters` and `create` on `talosoperations`, and the webhook refuses anything it writes except a newer
patch of the same minor in the two version fields. The audit log shows those writes as that user. If your clusters are
kept in Git by a tool that reverts edits, leave automatic patching off.

If an automatic operation fails or pauses, automatic patching stops everywhere (condition `AutoPatchHalted`, a
warning Event, an item in the Overview's attention list) and never retries. Look at the operation, resume it with
`ichor operation resume` or delete it, then clear the halt: the Settings page's "Clear the halt" button, or `ichor
settings set autoPatch.acknowledgeHalt=NAMESPACE/NAME`. It also ends by itself when the operation completes or is
deleted, or when a later manual upgrade of that cluster completes. Clearing the halt is allowed under read-only mode.

## A Helm install

A Helm install gets an `IchorInstallation` too: the controller creates it once,
if it is absent, with `spec.version` set to its own version and
`spec.deployment.manager: helm`. The chart never renders it, and the controller
never updates its `spec` afterwards, so `helm upgrade` does not revert your
settings. On a Helm install the Settings page says it is managed by Helm; runtime
settings still work. Upgrade Ichor with `helm upgrade`: a `TalosOperation` of
kind `ichor` is refused and `ichor upgrade ichor` prints the Helm command.
