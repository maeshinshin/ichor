---
sidebar_order: 5
---

# 5. Open the UI and sign in

ichor-ui opens at its issuer: the `--ui-url` you gave init, else `https://localhost:8443`.

![Two ways to reach ichor-ui. With --ui-url, the browser reaches the address you chose, which you make reachable yourself, and that address leads to ichor-ui on port 8443. With no --ui-url, the browser reaches https://localhost:8443 on the workstation, where ichor ui port-forwards to the ichor-ui pod in ichor-system for as long as it runs.](../../images/open-the-ui-paths.light.drawio.svg)

*Two ways to reach ichor-ui: the address you chose, or a port-forward from your workstation.*

- With `--ui-url`, open that address. Making it reachable is yours; init installs
  nothing for it.
- With none, `https://localhost:8443` exists only while `ichor ui` runs: it
  port-forwards ichor-ui using the kubeconfig the other commands use (set
  `KUBECONFIG` as in [step 4](check-the-cluster.md) unless you merged it).

```sh
ichor ui
```

```text
✓ ichor-ui  https://localhost:8443/
  port-forward to ichor-system/ichor-ichor-ui-7d9c5b6f4-x2k8q · Ctrl-C to stop
```

> [!IMPORTANT]
> Leave `ichor ui` running while you use ichor-ui. It opens your default browser
> (`--open-browser=false` only prints the address) and keeps the tunnel until Ctrl-C.

The certificate is from Ichor's own serving CA, so the browser warns.

Sign in as `admin` with the password init printed. The first login forces a new
password (12 to 128 bytes). Users and roles: [Users and login](../users-and-login.md).
Ichor-ui cannot run without a login.

## Where to go next

- [Settings](../settings.md), [Hardening](../hardening.md) and
  [Users and login](../users-and-login.md) for what to configure first.
- [Install Ichor with Helm](../install-with-helm.md) and
  [Adopt a running cluster](../adopt-a-cluster.md) for existing clusters.
- [Architecture](../../design/architecture.md) for how it works.
