---
sidebar_order: 1
---

# Changing settings

Either way, a change is a write to the `IchorInstallation` named `ichor`.

:::tabs

::tab[In the UI]

Open "Settings" in the sidebar. Cards: Deployment (target and running version,
installer or Helm, read-only), Sign-in and Defaults. Each value shows "Set here"
or "Deployment default". A card that sets a value has a "Use the deployment value"
button, which clears it. The page is read-only, with an explanation, when your
role cannot change settings or the deployment manages them ([Read-only
mode](#read-only-mode)).

::tab[With the CLI]

```sh
ichor get settings
ichor set settings users.tokenTTL=15m defaults.driftPolicy=autoCorrect
```

`ichor get settings` shows each setting, its value in force and its source
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

`ichor set settings` writes only the keys you name and prints the table again.
`--reset KEY` (repeatable) clears a key instead, so the deployment value
applies again and follows later changes to the flag or chart value:

```sh
ichor set settings --reset users.tokenTTL --reset defaults.driftPolicy
```

The keys:

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

:::

> [!NOTE]
> `reclaim.enabled` and `spec.reclaim` are deprecated and ignored;
> `ichor set settings reclaim.enabled=...` is refused and the webhook warns when
> `spec.reclaim` is set. RBAC alone decides who may reset and add a machine
> ([Reclaim a machine](../reclaim-a-machine.md)).

Each accepted change leaves an Event on the `IchorInstallation` with the changed
fields, old and new values, and the user.

## Who may change what

Reading is part of `ichor-viewer`. Writing `ichorinstallations` is
`ichor-admin`'s; the webhook then asks Kubernetes whether the user may `update`
the subresource for each part of the change.

![Who may change what. ichor-admin writes the IchorInstallation. A change to spec.version or spec.deployment needs update on ichorinstallations/version; a change to users, defaults, updateCheck or autoPatch needs update on ichorinstallations/settings. ichor-ui acts as the signed-in user, so that user's own update decides it. The ichor-ui ServiceAccount holds no settings verb.](../../images/settings-who-may-change.light.drawio.svg)

*The webhook checks the subresource for each part of a change.*

| Changing | Needs `update` on |
|---|---|
| `spec.version`, `spec.deployment` | `ichorinstallations/version` |
| `spec.users`, `spec.defaults`, `spec.updateCheck`, `spec.autoPatch` | `ichorinstallations/settings` |

`ichor-admin` holds both on separate rule lines, so a custom role can hold fewer.
ichor-ui acts as the signed-in user, so that user's `update` decides; the
ichor-ui ServiceAccount holds no settings verb, since impersonating a user does
not give it that user's permissions.

## Read-only mode

Set the controller flag `--settings-read-only` (chart value
`settings.readOnly: true`) when Git or Helm owns the configuration. The webhook
denies writes to the settings fields, every consumer ignores what the
`IchorInstallation` holds, and the Settings page is read-only with a "managed by
the deployment" notice. Values come from the flags and chart values only.
