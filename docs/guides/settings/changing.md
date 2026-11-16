---
sidebar_order: 1
---

# Changing settings

You change a setting in the UI or with the CLI, and either way it is a write to
the `IchorInstallation` named `ichor`. This page covers how, who may do it, and
what changes when a deployment owns the settings.

## In the UI

Open "Settings" in the sidebar. The page is a set of cards: Deployment (target
and running version, installer or Helm, read-only), Sign-in and Defaults. Each
value shows whether it is "Set here" or a "Deployment default".

The page is read-only, with an explanation, when your role cannot change
settings or when the deployment manages them (see [Read-only mode](#read-only-mode)).

## With the CLI

```sh
ichor get settings
ichor set settings users.tokenTTL=15m defaults.driftPolicy=autoCorrect
```

`ichor get settings` shows each setting, the value in force and its source
(`spec` or `deployment`); `-o yaml` or `-o json` print the full response.

```console
$ ichor get settings
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

`ichor set settings` writes only the keys you name, and prints the table again
with the new values:

```text
  users.tokenTTL=15m           token lifetime, 1m to 1h
  users.refreshLifetime=168h  refresh lifetime, 1h to 720h
  defaults.driftPolicy=warnOnly|autoCorrect   default for a new TalosCluster
  updateCheck.enabled=true|false              look for a newer Ichor release
  autoPatch.enabled=true|false                apply patch releases automatically
  autoPatch.components=talos,cilium           which components (empty: all)
  autoPatch.window=02:00-05:00                daily UTC window (empty: any time)
  autoPatch.acknowledgeHalt=NAMESPACE/NAME    clear a halt (see Automatic patching)
```

> [!NOTE]
> There is no switch for "Reset and add" (MachineReclaim) any more. `reclaim.enabled`
> and `spec.reclaim` are deprecated and ignored, `ichor set settings
> reclaim.enabled=...` is refused, and the webhook warns when `spec.reclaim` is
> set. Who may reset and add a machine is decided by RBAC alone; see
> [Reclaim a machine](../reclaim-a-machine.md).

Each accepted change leaves an Event on the `IchorInstallation` with the changed
fields, old and new values and the user.

## Who may change what

Reading is part of `ichor-viewer`. Writing `ichorinstallations` stays with
`ichor-admin`; the webhook then asks Kubernetes whether the user may `update`
the subresource for each part of the change:

![Who may change what. ichor-admin writes the IchorInstallation. A change to spec.version or spec.deployment needs update on ichorinstallations/version; a change to users, defaults, updateCheck or autoPatch needs update on ichorinstallations/settings. ichor-ui acts as the signed-in user, so that user's own update decides it. The ichor-ui ServiceAccount holds no settings verb.](../../images/settings-who-may-change.light.drawio.svg)

*The webhook checks the subresource for each part of a change.*

| Changing | Needs `update` on |
|---|---|
| `spec.version`, `spec.deployment` | `ichorinstallations/version` |
| `spec.users`, `spec.defaults`, `spec.updateCheck`, `spec.autoPatch` | `ichorinstallations/settings` |

`ichor-admin` holds both on separate rule lines, so a custom role can hold
fewer. ichor-ui makes the change as the signed-in user, so that user's own
`update` decides it. The ichor-ui ServiceAccount holds no settings verb:
impersonating a user does not give the ServiceAccount that user's permissions.

## Read-only mode

Set the controller flag `--settings-read-only` (chart value
`settings.readOnly: true`) when Git or Helm owns the configuration. The webhook
then denies writes to the settings fields, every consumer ignores whatever the
`IchorInstallation` holds, and the Settings page is read-only with a "managed by
the deployment" notice. The values come from the flags and chart values only.
