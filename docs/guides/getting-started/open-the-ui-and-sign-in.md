---
sidebar_order: 5
---

# 5. Open the UI and sign in

Sign-in is on by default. ichor-ui is opened at its issuer: the `--ui-url` you
gave init, or `https://localhost:8443` when you gave none.

If you gave `--ui-url`, open that address. Making it reachable was your job when
you chose it; init installs nothing for it.

If you gave none, `https://localhost:8443` is not a service on your network. It
is the address `ichor ui` serves ichor-ui on, through a port-forward from your
workstation, and nothing answers there until that command runs. Run it first
and leave it running for as long as you use ichor-ui. It reaches the cluster
with the kubeconfig the other commands use, so set `KUBECONFIG` as in
[step 4](check-the-cluster.md) first, unless you merged it:

```sh
ichor ui
```

```text
✓ ichor-ui  https://localhost:8443/
  port-forward to ichor-system/ichor-ichor-ui-7d9c5b6f4-x2k8q · Ctrl-C to stop
```

It opens that address in your default browser (`--open-browser=false` only
prints it) and keeps the tunnel up until you press Ctrl-C.

The certificate is Ichor's own, from the Ichor serving CA, which a browser does
not trust, so it warns. Sign in as `admin` with the password init printed. The
first login forces you to choose a new password (12 characters or more, at most
128 bytes); after that, users, roles and kubeconfigs are as described in
[Users and login](../users-and-login.md).

There is no way to run ichor-ui without a login: `--allow-unauthenticated` was removed from `ichor init` and `ichor adopt`, and both refuse it.

# Where to go next

- [Settings](../settings.md), [Hardening](../hardening.md) and
  [Users and login](../users-and-login.md) for what to configure first.
- [Install Ichor with Helm](../install-with-helm.md) and
  [Adopt and reclaim](../adopt-and-reclaim.md) for existing clusters.
- [Architecture](../../design/architecture.md) for how it works.
