---
sidebar_order: 5
---

# 5. Open the UI and sign in

Sign-in is on by default. Open ichor-ui in a browser at its issuer: the
`--ui-url` you gave init, or `https://localhost:8443` when you gave none. The
certificate is Ichor's own, so the browser warns. Sign in as `admin` with the
password init printed. The first login forces you to choose a new password
(12 to 128 bytes); after that, users, roles and kubeconfigs are as described
in [Users and login](../users-and-login.md).

If you ran init with `--allow-unauthenticated` there is no login: anyone who can
reach ichor-ui can change the cluster, including resetting nodes. Open it through
the port-forward that `ichor ui` sets up, at `https://localhost:8443`:

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
