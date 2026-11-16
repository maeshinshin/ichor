---
sidebar_order: 6
---

# Sign-in

Sign-in is on by default in adopt, as it is in `ichor init`. In the Ichor that
adopt installs, it creates the first administrator (`--admin-user`, default
`admin`), switches ichor-ui to sign-in with the issuer `--ui-url` (default
`https://localhost:8443`, where `ichor ui` serves ichor-ui), and sets
`spec.authentication.ichor` on the `TalosCluster`. It does this after the plan
and the releases, so the zero-drift first render goes out first and the
authenticator comes after it. The generated password is printed once, as the
last thing adopt prints before `Next`, and must be changed at the first sign-in.
If adopt stops before it can print it, adopt prints it then.

> [!WARNING]
> The consent prompt names the cost. Setting `spec.authentication.ichor` adds an
> authenticator to every control plane's `KubeAuthenticationConfig`, so each
> control plane reports `Ready=False APIServerRestartRequired`. That is a change
> to your cluster's kube-apiserver: it restarts on each control plane, one at a
> time, only when you run `ichor rollout config` (or Apply in the Overview page's
> attention list). Until then it keeps running as it is; with a single control
> plane the Kubernetes API is unavailable while it restarts. Choose the moment, and
> run it then.

> [!IMPORTANT]
> Sign-in cannot be turned off: `--allow-unauthenticated` was removed and adopt refuses
> it, so every adoption of a cluster Ichor does not manage yet creates the administrator and
> sets the authenticator. That needs Talos v1.14 or later on the cluster; a cluster below it is
> refused, writing nothing, until Talos is upgraded.

To reach ichor-ui at an https address of your own, adopt takes the flags `ichor init` has:
`--ui-url`, `--ui-tls-secret` or `--ui-tls-cert` with `--ui-tls-key`, `--kube-api-server-url`
and `--kube-api-tls-server-name` (see [Users and login](../users-and-login.md)). They are checked
before anything is written, and refused where adopt would not apply them: a Helm-managed Ichor
(set the chart values `ichorUI.tls.secretName`, `ichorUI.users.kubeAPIServerURL` and
`ichorUI.users.kubeAPITLSServerName`) and an ichor-ui that already has sign-in on.

If adopt stops after creating the administrator, run it again: it sets what is
still missing and creates no second administrator. A cluster adopted on a node
that has its own jwt authenticator is refused (move the identity provider into
`spec.authentication.external` first); the issuer Ichor itself set is not
counted as foreign when you adopt again or add a machine.
