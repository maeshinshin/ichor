---
sidebar_order: 1
---

# Turn users on

`ichor init` turns users on by default, with the issuer `https://localhost:8443`
(where `ichor ui` serves ichor-ui) unless you pass `--ui-url`. The Helm chart
has no default: it refuses an ichor-ui with neither `ichorUI.users.enabled` nor
`ichorUI.oidc` (see [Without any sign-in](without-sign-in.md)).

## Before you start

Turning users on needs three things:

- An https URL that browsers and the `ichor` CLI can reach. The default
  `https://localhost:8443` is reached through `ichor ui`'s port-forward; any
  other address needs one, and Ichor does not provide it.
- A Talos v1.14 contract or later. Older contracts cannot carry the issuer
  configuration.
- A rollout of the control planes with `ichor rollout config`. The kube-apiserver
  reads its login settings from the machine config.

## On a new cluster

On a new cluster, sign-in is on by default, and `ichor adopt` turns it on in the Ichor it installs the same way (same flags; see [Adopt a running cluster](../adopt-a-cluster.md)). To reach ichor-ui at an address of your own, pass that URL to `ichor init`:

```sh
ichor init --ui-url https://ichor.example.com --admin-user admin ...
```

The first install's version contract is the release set's (v1.14 in this build), so `--version-contract` accepts only that value
on `ichor init`; init refuses any other before it touches the node. `ichor init` installs Ichor from the manifest in
the binary, not from the Helm chart, so it applies in place what
`ichorUI.users.enabled` and `ichorUI.users.externalURL` render: the flags,
the internal port and the key volumes of the ichor-ui Deployment, the internal
port of its Service, and the empty `ichor-user-credentials` Secret. It then
creates the first admin (`--admin-user`, default `admin`, role `admin`) and
sets `spec.authentication.ichor` on the TalosCluster. The order is: Secret,
admin, then the switch, so nobody is locked out. At the end it prints, to
stderr, the Helm values that match and the generated password of the admin.

> [!WARNING]
> The password is printed once, also when a later step failed, and is stored
> nowhere; the first login forces a change. Save it when it is printed.

The kube-apiserver login settings reach the control planes with
`ichor rollout config`, which restarts the kube-apiserver on each control plane
in turn (see [Control plane rollout](#control-plane-rollout)). Init retries transient API errors (such as
an etcd timeout right after the API server comes up) for a few minutes before
it gives up.

`ichor init` and `ichor adopt` take four more flags. They are checked before anything is
written, and each has a chart value:

| `ichor init` and `ichor adopt` flag | Chart value | Effect on ichor-ui |
|---|---|---|
| `--ui-tls-secret NAME` | `ichorUI.tls.secretName` | Mounts the existing `kubernetes.io/tls` Secret `NAME` (in `ichor-system`), sets `--tls-cert-file` and `--tls-key-file`, and probes over HTTPS. |
| `--ui-tls-cert FILE` with `--ui-tls-key FILE` | `ichorUI.tls.secretName` | Alternative to `--ui-tls-secret`: init stores the PEM pair as the Secret `ichor-ui-tls` and uses it the same way. The key is never printed. |
| `--kube-api-server-url URL` | `ichorUI.users.kubeAPIServerURL` | The https URL put in users' kubeconfigs (default: the cluster's `spec.endpoint`). |
| `--kube-api-tls-server-name NAME` | `ichorUI.users.kubeAPITLSServerName` | The `tls-server-name` in users' kubeconfigs, for a URL host outside the kube-apiserver certificate. |

## Publish at another address

To publish ichor-ui at another address after init, set the new https URL in `spec.authentication.ichor.url` of the TalosCluster (a patch that needs the `update` verb on `talosclusters/authentication`), then run an Ichor upgrade (`ichor upgrade ichor`) so the installer applies it to ichor-ui and the controller. Whether an upgrade at the same version re-runs the installer is not covered here. Then `ichor rollout config` applies the kube-apiserver side. The URL and the token issuer must match, so sign-in breaks until both are done.

## On an existing install

On an existing install, set `ichorUI.users.enabled` to `true` and
`ichorUI.users.externalURL` to the https URL in the chart values, and create
the first admin before every request starts to need a login:

```sh
ichor create user admin --role admin --generate --bootstrap
```

`--bootstrap` needs no login, because nobody can log in yet. It uses your own
kubeconfig, so Kubernetes decides: you need `create` on `ichorusers` and `get`
and `patch` on the Secret `ichor-user-credentials`. The CLI creates the
`IchorUser` and writes the argon2id hash of the password into that Secret with
the same code ichor-ui uses. It refuses a name that already exists (it never
resets a password) and a cluster where the Secret does not exist, which means
users are not turned on yet (the chart creates it with
`ichorUI.users.enabled`). The generated password is printed once to stderr and
must be changed at the first login. Instead of `--generate` you can type the
password at a prompt with no echo, or pipe it with `--password-stdin`. Then set
`spec.authentication.ichor` (`enabled: true`, `url`) on the TalosCluster, which
needs `update` on `talosclusters/authentication`, and run `ichor rollout config`
if a change waits for a reboot. Once users are on, every request to ichor-ui
needs a login.

## Control plane rollout

Talos applies the login settings without a reboot, but restarts the
kube-apiserver on each change (some tens of seconds of `/readyz` down per control
plane). The machine controller does not apply the change by itself: the
machines report `APIServerRestartRequired`, and `ichor rollout config` (or Apply in the attention list of the UI's Overview) rolls it out one control plane at a time with
no drain, waiting for that node's `/readyz` before the next. On a cluster with
one control plane the API is unavailable while it restarts.
