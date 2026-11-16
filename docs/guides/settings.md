# Settings

Ichor's runtime settings live in the cluster-scoped `IchorInstallation` named
`ichor`. You edit them in the UI, with the CLI, or by writing the CR. The
controller, the webhooks and ichor-ui read them live: a change takes effect on
the next call, with no rollout.

![Two ways to write the IchorInstallation named ichor: the Settings page in the UI, or ichor set settings or a hand-written CR. Each write goes through the admission webhook, which asks Kubernetes whether you may update each part of the change. The controller, the webhooks and ichor-ui then read the values live.](../images/settings-write-path.light.drawio.svg)

*Where a setting is written, and who reads it.*

| Setting | Field | Meaning |
|---|---|---|
| `users.tokenTTL` | `spec.users.tokenTTL` | Lifetime of tokens ichor-ui issues, 1m to 1h. |
| `users.refreshLifetime` | `spec.users.refreshLifetime` | Refresh lifetime, 1h to 720h; must not be shorter than the token lifetime. |
| `defaults.driftPolicy` | `spec.defaults.driftPolicy` | `warnOnly` or `autoCorrect`: the drift policy a new `TalosCluster` gets. |
| `updateCheck.enabled` | `spec.updateCheck.enabled` | Whether Ichor looks for a newer release and shows it on the dashboard. On by default; see [Updates](settings/updates.md). |
| `autoPatch.enabled` | `spec.autoPatch.enabled` | Apply patch releases of the running minor automatically. Off by default; see [Automatic patching](settings/automatic-patching.md). |
| `autoPatch.components` | `spec.autoPatch.components` | Which of `talos`, `cilium`, `kubernetes`; empty means all three. |
| `autoPatch.window` | `spec.autoPatch.window` | A daily UTC window `HH:MM-HH:MM` an upgrade may start in; empty means any time. |

## Fields that are set and fields that are not

A field that is set wins. A field that is not set falls back to the flag or
chart value (the ichor-ui token flags) or to `warnOnly`. There is no way to
unset a field from the UI or the CLI. `status.effective` on the
`IchorInstallation` shows each value in force and its source, `spec` or
`deployment`.

The default drift policy applies when a `TalosCluster` is created, through a
defaulting webhook. The webhook has `failurePolicy: Ignore`: if it is down, a
cluster is created with the field empty, which is read as `warnOnly`. A cluster
that exists is never changed.

Deployment facts (images, replicas, the TLS Secret, the external URL) are not
settings. They stay with the installer or Helm.

## The pages

- [Changing settings](settings/changing.md): the UI, the CLI, who may change
  what, and read-only mode.
- [Updates](settings/updates.md): the release check, and what it shows.
- [Automatic patching](settings/automatic-patching.md): patch upgrades the
  controller starts by itself, and what stops them.

## A Helm install

A Helm install gets an `IchorInstallation` too: the controller creates it once,
if it is absent, with `spec.version` set to its own version and
`spec.deployment.manager: helm`. The chart never renders it, and the controller
never updates its `spec` afterwards, so `helm upgrade` does not revert your
settings.

On a Helm install the Settings page says it is managed by Helm; runtime settings
still work. Upgrade Ichor with `helm upgrade`: a `TalosOperation` of kind `ichor`
is refused and `ichor upgrade ichor` prints the Helm command.
