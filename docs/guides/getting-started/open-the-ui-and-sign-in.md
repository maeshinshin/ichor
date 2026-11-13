---
sidebar_order: 5
---

# 5. Open the UI and sign in

If you turned sign-in on, open the `--ui-url` in a browser. The certificate is
yours, so a self-signed one makes the browser warn. Sign in as `admin` with the
password init printed. The first login forces you to choose a new password
(12 to 128 bytes); after that, users, roles and kubeconfigs are as described
in [Users and login](../users-and-login.md).

If you did not use `--ui-url`, there is no login: `ichor init` started
ichor-ui with `--allow-unauthenticated`, so anyone who can reach it can make
changes. Open it through a port-forward:

```sh
ichor ui
```

```text
✓ ichor-ui  https://localhost:8443/
  port-forward to ichor-system/ichor-ichor-ui-7d9c5b6f4-x2k8q · Ctrl-C to stop
```

It opens that address in your default browser (`--open-browser=false`
only prints it) and keeps the tunnel up until you press Ctrl-C. The
certificate comes from the Ichor serving CA, which a browser does not trust, so
it warns.

# Where to go next

- [Settings](../settings.md), [Hardening](../hardening.md) and
  [Users and login](../users-and-login.md) for what to configure first.
- [Install Ichor with Helm](../install-with-helm.md) and
  [Adopt and reclaim](../adopt-and-reclaim.md) for existing clusters.
- [Architecture](../../design/architecture.md) for how it works.
