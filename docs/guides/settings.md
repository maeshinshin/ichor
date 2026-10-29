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
`ichor settings set` writes only the keys you name:

```
  users.tokenTTL=15m           token lifetime, 1m to 1h
  users.refreshLifetime=168h  refresh lifetime, 1h to 720h
  defaults.driftPolicy=warnOnly|autoCorrect   default for a new TalosCluster
```

There is no switch for "Reset and add" (MachineReclaim) any more: `reclaim.enabled`
and `spec.reclaim` are deprecated and ignored (ADR 0107), `ichor settings set
reclaim.enabled=...` is refused, and the webhook warns when `spec.reclaim` is
set. Who may reset and add a machine is decided by RBAC alone; see
`adopt-and-reclaim.md`. Each accepted change leaves an Event on the
`IchorInstallation` with the changed fields, old and new values and the user.

## Who may change what

Reading is part of `ichor-viewer`. Writing `ichorinstallations` stays with
`ichor-admin`; the webhook then asks Kubernetes whether the user may `update`
the subresource for each part of the change:

| Changing | Needs `update` on |
|---|---|
| `spec.version`, `spec.deployment` | `ichorinstallations/version` |
| `spec.users`, `spec.defaults` | `ichorinstallations/settings` |

`ichor-admin` holds both on separate rule lines, so a custom role can hold
fewer. The ichor-ui ServiceAccount holds `update` on `settings` for the users it
impersonates. Read the other documents in this guide set for
roles in general.

## Read-only mode

Set the controller flag `--settings-read-only` (chart value
`settings.readOnly: true`) when Git or Helm owns the configuration. The webhook
then denies writes to the settings fields, every consumer ignores whatever the
`IchorInstallation` holds, and the Settings page is read-only with a "managed by
the deployment" notice. The values come from the flags and chart values only.

## A Helm install

A Helm install gets an `IchorInstallation` too: the controller creates it once,
if it is absent, with `spec.version` set to its own version and
`spec.deployment.manager: helm`. The chart never renders it, and the controller
never updates its `spec` afterwards, so `helm upgrade` does not revert your
settings. On a Helm install the Settings page says it is managed by Helm; runtime
settings still work. Upgrade Ichor with `helm upgrade`: a `TalosOperation` of
kind `ichor` is refused and `ichor upgrade ichor` prints the Helm command.
