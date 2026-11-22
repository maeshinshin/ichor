---
sidebar_order: 2
---

# Without any sign-in

![What happens without a sign-in. The open mode and its flags (allow-unauthenticated, ichorUI.allowUnauthenticated) are removed; ichor init, adopt, helm and ichor-ui startup refuse them. Instead enable ichorUI.users or ichorUI.oidc, or, on an install with no sign-in, let ichor upgrade ichor turn users on first.](../../images/users-without-sign-in.light.drawio.svg)

*There is no way to run ichor-ui open; every refusal names the fix.*

ichor-ui does not start without a sign-in (Ichor users or an external OIDC
provider, `ichorUI.oidc`), and a caller it cannot identify is served nothing,
reads included. Reachable without a login: the sign-in page, `/auth/config`
and the issuer's own endpoints.

> [!IMPORTANT]
> The open mode, where anyone who could reach ichor-ui wrote with its
> ServiceAccount's permissions, was removed, with `--allow-unauthenticated`
> and `ichorUI.allowUnauthenticated`. The ServiceAccount holds none of the
> `start` verbs, so no request without a user can start a shutdown, rotation or restore.

Where each is refused:

| Where | What happens |
|---|---|
| `ichor init`, `ichor adopt` | Refuse `--allow-unauthenticated` (and `ui.allowUnauthenticated: true` in an init file); sign-in is on in every cluster they set up |
| `ichor-ui` | Stops at startup on `--allow-unauthenticated` or `ICHOR_ALLOW_UNAUTHENTICATED=true` |
| `helm install`, `helm upgrade` | Fail when `ichorUI.allowUnauthenticated` is true. `--reuse-values` carries the old value: add `--set ichorUI.allowUnauthenticated=null` or drop `--reuse-values`. Then enable `ichorUI.users` or `ichorUI.oidc` |
| The chart | Refuses `ichorUI.enabled` with neither `ichorUI.users.enabled` (with `externalURL`) nor `ichorUI.oidc.enabled`. `ichorUI.enabled=false` installs Ichor without ichor-ui |

## An install with no sign-in yet

`ichor upgrade ichor` turns Ichor's own sign-in on first, as `ichor init` does:

- It asks (`--yes` skips), creates the first administrator, switches ichor-ui, sets `spec.authentication.ichor` on each `TalosCluster` that can carry it (Talos v1.14 or later) and prints the password once, at the end.
- It takes `--ui-url`, `--admin-user` and the TLS and kubeconfig flags of `ichor init`.
- The installer Job refuses an ichor-ui with no sign-in and says to use this command, so any other upgrade (a hand-written `TalosOperation`, the controller's patch upgrades) changes nothing. An install with the removed flag has it dropped, with a `NOTICE`.

A Helm install is not touched by `ichor upgrade ichor`. Before `helm upgrade`,
set `ichorUI.users` or `ichorUI.oidc` in the values, then create the first
administrator with `ichor create user admin --role admin --generate --bootstrap`
([Turn users on](turn-users-on.md)).
