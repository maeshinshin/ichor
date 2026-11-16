---
sidebar_order: 5
---

# Sign in and get a kubeconfig

To sign in from the terminal:

```sh
ichor auth login --issuer https://ichor.example.com --audience ichor-ui
```

This opens a browser for the login page. The password is typed only into that
page; the CLI never sees it. The default audience is `ichor-kube`, the login
`kubectl` needs; `--audience ichor-ui` is the one the `ichor user` commands use.
`ichor auth whoami` shows who you are and when the
cached tokens expire; it prints no token. `ichor auth logout` revokes the
refresh token of each cached login at the issuer, then removes the local login
cache; if the issuer cannot be reached it warns, and still removes the cache. If the certificate of ichor-ui comes from a private CA, add
`--ca-file ca.pem` (or `export ICHOR_CA_FILE=ca.pem`) to every `ichor auth`
and `ichor user` command.

To get a kubeconfig:

```sh
ichor create kubeconfig --issuer https://ichor.example.com --out ~/.kube/ichor.yaml
```

`--out -` prints it to stdout; without `--out` the file is named as ichor-ui
suggests and written to the current directory. The file is written with mode 0600 and the
command refuses to overwrite an existing file. With `--ca-file ca.pem`, the
kubeconfig carries `--ca-file <absolute path>` in the `ichor auth token`
arguments, so `kubectl` trusts the same CA; without the flag the arguments do
not change.

You can also use "Download my kubeconfig" in the account menu of ichor-ui. The
file holds the API server address and the cluster CA. It holds no secret. When
`kubectl` runs, it calls `ichor auth token`, which uses the login cache. You
need at least the `viewer` role to download it.

> [!WARNING]
> The login cache is a credential on your machine. It is stored in
> `~/.ichor/auth/` with restricted permissions.

After a Kubernetes CA rotation, a kubeconfig downloaded earlier fails TLS. Get
a new one.

How long a sign-in lasts, and what ends one early, is in
[Sign-in and sessions](../../concepts/sign-in-and-sessions.md).

## A read-only talosconfig for `talosctl`

`operator` and `admin` can take a talosconfig whose client certificate has the
Talos role `os:reader`: `talosctl` can read a node (`get members`, `logs`,
`dmesg`, `processes`) and the node refuses everything that changes it
(`reboot`, `upgrade`, `reset`). It needs no access to the secrets bundle.

```sh
ichor create talosconfig my-cluster --ttl 4h
TALOSCONFIG=~/.talos/config.ichor-reader talosctl get members
```

> [!WARNING]
> It lives one hour by default and at most twelve; there is no way to ask for
> longer, and it cannot be revoked before it expires, so ask for what you need.
The key is made on your machine. The UI offers the same under the cluster's
"More actions" menu. Each request leaves an Event on the TalosCluster that
names you (`kubectl get events --field-selector reason=TalosconfigReader`).

The permission is the Kubernetes verb `download` on
`talosclusters/talosconfig-reader`. `viewer` does not hold it, because a
talosconfig is the node's own API, without the filtering and masking of the UI.
To give it to a group, bind a ClusterRole with that rule to it. Ichor offers no
`os:operator` talosconfig: it could reboot and upgrade nodes outside the
operations Ichor plans and records. For admin access to the node API use
`ichor create credentials`.
