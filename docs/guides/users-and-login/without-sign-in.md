---
sidebar_order: 2
---

# Without any sign-in

ichor-ui does not start without a sign-in, Ichor users or an external OIDC provider
(`ichorUI.oidc`), and a caller it cannot identify is served nothing: every call is
refused, reads included. What is reachable without a login is only what the sign-in
page needs: the page itself, `/auth/config` and the issuer's own endpoints.

> [!IMPORTANT]
> There is no way to let ichor-ui make changes without a sign-in. The open mode, where
> anyone who can reach ichor-ui writes with its ServiceAccount's permissions, was
> removed; so were `--allow-unauthenticated` and `ichorUI.allowUnauthenticated`, and
> ichor-ui's ServiceAccount holds none of the `start` verbs, so no request
> without a user can start a shutdown, a rotation or a restore.

Each of them is refused and says what to do instead:

- `ichor init` and `ichor adopt` refuse `--allow-unauthenticated` (also as
  `ui.allowUnauthenticated: true` in an init file). Sign-in is on in every cluster
  they set up.
- `ichor-ui` stops at startup when it is given `--allow-unauthenticated` or
  `ICHOR_ALLOW_UNAUTHENTICATED=true`.
- `helm install` and `helm upgrade` fail when `ichorUI.allowUnauthenticated` is true.
  `helm upgrade --reuse-values` carries the old value forward; pass
  `--set ichorUI.allowUnauthenticated=null` with it, or drop `--reuse-values`.
  Then enable `ichorUI.users` or `ichorUI.oidc`.
- `ichor upgrade ichor` on an install whose ichor-ui has no sign-in turns Ichor's own
  sign-in on first, as `ichor init` does: it asks (`--yes` skips the question), creates
  the first administrator, switches ichor-ui, sets `spec.authentication.ichor` on each
  `TalosCluster` that can carry it (Talos v1.14 or later) and prints the password once,
  at the end. It takes `--ui-url`, `--admin-user` and the TLS and kubeconfig flags of
  `ichor init`. The installer Job refuses an ichor-ui with no sign-in and says to use this
  command, so an upgrade started any other way (a hand-written `TalosOperation`, the
  controller's own patch upgrades) changes nothing. An install with the removed flag has
  it dropped, with a `NOTICE`.
- The chart refuses `ichorUI.enabled` with neither `ichorUI.users.enabled` (with
  `ichorUI.users.externalURL`) nor `ichorUI.oidc.enabled`, naming what to set. A Helm
  install is not touched by `ichor upgrade ichor`: before `helm upgrade` to this release,
  set one of them in the values, then create the first administrator with
  `ichor create user admin --role admin --generate --bootstrap` (see [Turn users on](turn-users-on.md)).
  `ichorUI.enabled=false` installs Ichor without ichor-ui.
