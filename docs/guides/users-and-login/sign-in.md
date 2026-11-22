---
sidebar_order: 5
---

# Sign in and get a kubeconfig

![How the CLI signs in and what it can produce. ichor auth login opens the browser login page and caches tokens in ~/.ichor/auth. ichor create kubeconfig writes a kubeconfig, and kubectl then runs ichor auth token, which uses the cache. ichor create talosconfig issues a short-lived read-only talosconfig.](../../images/users-sign-in-flow.light.drawio.svg)

*One browser login feeds both `kubectl` and a read-only `talosctl`.*

```sh
ichor auth login --issuer https://ichor.example.com --audience ichor-ui
```

The browser opens the login page; the password is typed only there, never into
the CLI (`--no-browser` prints the URL instead).

- The default audience is `ichor-kube`, which `kubectl` needs; `--audience ichor-ui` is what the `ichor user` commands use.
- `ichor auth whoami` shows who you are and when the cached tokens expire; it prints no token.
- `ichor auth logout` revokes each cached login's refresh token at the issuer, then removes the cache. If the issuer is unreachable it warns and still removes the cache.
- For a private CA, add `--ca-file ca.pem` (or `export ICHOR_CA_FILE=ca.pem`) to every `ichor auth` and `ichor user` command.

> [!WARNING]
> The login cache, `~/.ichor/auth/`, is a credential on your machine. It is stored with restricted permissions.

## A kubeconfig

```sh
ichor create kubeconfig --issuer https://ichor.example.com --out ~/.kube/ichor.yaml
```

- `--out -` prints to stdout; without `--out` the file is named as ichor-ui suggests, in the current directory. It is written with mode 0600, and an existing file is never overwritten.
- With `--ca-file ca.pem`, the kubeconfig carries `--ca-file <absolute path>` in the `ichor auth token` arguments, so `kubectl` trusts the same CA.
- "Download my kubeconfig" in ichor-ui's account menu gives the same file (the `viewer` role is enough). It holds the API server address and the cluster CA, no secret. `kubectl` calls `ichor auth token`, which uses the login cache.
- After a Kubernetes CA rotation a downloaded kubeconfig fails TLS: get a new one.

How long a sign-in lasts, and what ends one early:
[Sign-in and sessions](../../concepts/sign-in-and-sessions.md).

## A read-only talosconfig for `talosctl`

`operator` and `admin` can take a talosconfig with the Talos role `os:reader`:
`talosctl` can read a node (`get members`, `logs`, `dmesg`, `processes`), and
the node refuses `reboot`, `upgrade` and `reset`. It needs no access to the
secrets bundle.

```sh
ichor create talosconfig my-cluster --ttl 4h
TALOSCONFIG=~/.talos/config.ichor-reader talosctl get members
```

> [!WARNING]
> It lives 1 hour by default and 12 at most, and cannot be revoked before it expires. Ask for what you need.

- The key is made on your machine. The UI offers the same under the cluster's "More actions".
- Each request leaves an Event on the TalosCluster naming you: `kubectl get events --field-selector reason=TalosconfigReader`.
- The permission is the verb `download` on `talosclusters/talosconfig-reader`. `viewer` lacks it, because a talosconfig is the node's own API without the UI's filtering and masking. To give it to a group, bind a ClusterRole with that rule.
- Ichor offers no `os:operator` talosconfig: it could reboot and upgrade nodes outside the operations Ichor plans and records. For admin access use `ichor create credentials`.
