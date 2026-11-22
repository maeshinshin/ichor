# Settings

Runtime settings live in the cluster-scoped `IchorInstallation` named `ichor`.
Edit them in the UI, with the CLI or by writing the CR. The controller,
webhooks and ichor-ui read them live, with no rollout.

![Two ways to write the IchorInstallation named ichor: the Settings page in the UI, or ichor set settings or a hand-written CR. Each write goes through the admission webhook, which asks Kubernetes whether you may update each part of the change. The controller, the webhooks and ichor-ui then read the values live.](../images/settings-write-path.light.drawio.svg)

*Where a setting is written, and who reads it.*

| Setting | Field | Meaning |
|---|---|---|
| `users.tokenTTL` | `spec.users.tokenTTL` | Lifetime of tokens ichor-ui issues, 1m to 1h. |
| `users.refreshLifetime` | `spec.users.refreshLifetime` | Refresh lifetime, 1h to 720h; must not be shorter than the token lifetime. |
| `defaults.driftPolicy` | `spec.defaults.driftPolicy` | `warnOnly` or `autoCorrect`: the drift policy a new `TalosCluster` gets. |
| `updateCheck.enabled` | `spec.updateCheck.enabled` | Whether Ichor looks for a newer release and shows it on the dashboard. On by default; see [Updates](settings/updates.md). |
| `autoPatch.enabled` | `spec.autoPatch.enabled` | Apply patch releases of the running minor automatically. Off by default; see [Automatic patching](settings/automatic-patching.md). |
| `autoPatch.components` | `spec.autoPatch.components` | Which of `talos`, `kubernetes`; empty means both. `cilium` is accepted and does nothing. |
| `autoPatch.window` | `spec.autoPatch.window` | A daily UTC window `HH:MM-HH:MM` an upgrade may start in; empty means any time. |

- A field that is set wins. An unset one falls back to the flag or chart value
  (the ichor-ui token flags) or to `warnOnly`. You cannot unset a field from the
  UI or CLI. `status.effective` shows each value in force and its source (`spec`
  or `deployment`).
- The default drift policy is applied to a new `TalosCluster` by a defaulting
  webhook with `failurePolicy: Ignore`. If it is down the field is empty, read
  as `warnOnly`. An existing cluster is never changed.
- Deployment facts (images, replicas, the TLS Secret, the external URL) are not
  settings; they stay with the installer or Helm.
- The condition `InstallerImageUnverified` on the `IchorInstallation` is `True`
  once an installer image ran with its signature unchecked
  (`spec.installerSignature.mode: skip`), and stays until a person removes it.

## The pages

- [Changing settings](settings/changing.md): UI, CLI, who may change what,
  read-only mode.
- [Updates](settings/updates.md): the release check.
- [Automatic patching](settings/automatic-patching.md): patch upgrades the
  controller starts by itself.

## A Helm install

The controller creates the `IchorInstallation` once, if absent, with
`spec.version` set to its own version and `spec.deployment.manager: helm`. The
chart never renders it and the controller never updates its `spec`, so `helm
upgrade` does not revert your settings. The Settings page says it is managed by
Helm; runtime settings still work. Upgrade Ichor with `helm upgrade`: a
`TalosOperation` of kind `ichor` is refused, and `ichor upgrade ichor` prints the
Helm command.
