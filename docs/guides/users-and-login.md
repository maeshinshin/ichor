# Users and login

Ichor can keep its own users, so people can sign in to ichor-ui and get their
own kubeconfig with no shared secret. This guide covers the steps. The design
is,
which builds
for logins from an external identity provider.

Status: proposed. The steps below are what it decides. Commands
and flags it does not name are marked as unverified.

Users are off by default. Nothing here applies until you turn them on.

## Before you start

Turning users on needs three things:

- An https URL that browsers and the `ichor` CLI can reach. Ichor does not
  provide one.
- A Talos v1.14 contract or later. Older contracts cannot carry the issuer
  configuration.
- A rollout of the control planes with `ichor config apply`. The kube-apiserver
  reads its login settings from the machine config.

## Turn users on

On a new cluster, pass the URL to `ichor init`:

```
ichor init --ui-url https://ichor.example.com
```

This sets `ichorUI.users.*`, creates the first admin, sets the Ichor login in
`spec.authentication.ichor` and prints that `ichor config apply` must run. The
first admin gets a generated password. It is printed once and stored nowhere,
and the first login forces a change. Save it when it is printed.

On an existing install, set `ichorUI.users.enabled` to `true` and
`ichorUI.users.externalURL` to the https URL in the chart values. Then run
`ichor config apply`. Once users are on, every request to ichor-ui needs a
login.

Unverified: how to create the first admin on an install
where `ichor init` has already run. Check this before you switch.

## Expose ichor-ui over https

ichor-ui is a ClusterIP service by default (`ichorUI.service.type`). Ichor
installs no ingress controller, so pick one of these:

- An Ingress or a Gateway in front of ichor-ui, with TLS at the edge.
- A LoadBalancer service (`ichorUI.service.type: LoadBalancer`).
- TLS inside ichor-ui, with `ichorUI.tls.secretName`.

Port 8444 is an internal listener. The kube-apiserver uses it to fetch the
login keys. Do not publish it.

## Create users and roles

Each user is an `IchorUser` object in `ichor-system`. You can create one with
the CLI or write one in Git. Its name is the login name and cannot change. It
has:

- a display name and an e-mail (for information only);
- `roles`, a list of role names;
- `disabled`, which is false for a new user.

The built-in roles are:

| Role | Can |
|---|---|
| `viewer` | read clusters, machines, operations and nodes |
| `operator` | viewer, plus edit machines and config, and start routine operations |
| `admin` | operator, plus delete, user administration, sign-in settings and the destructive operations |

A role is a group in Kubernetes. A custom permission is a ClusterRole bound to
the group `ichor:<name>` of the user. No built-in role can read secrets.

A new user has no password until an admin sets one. The admin can set a
password, or ask for a generated one, which is shown once. The user changes it
at the next login. A password is 12 to 128 bytes long, with no other rules.

Users can change their own password in the account menu.

Unverified: the exact CLI flags for `ichor user` are not.

## Sign in and get a kubeconfig

To sign in from the terminal:

```
ichor auth login
```

This opens a browser for the login page. The password is typed only into that
page; the CLI never sees it. `ichor auth status` shows the current login.
`ichor auth logout` removes the local login cache.

To get a kubeconfig:

```
ichor auth kubeconfig -o ~/.kube/ichor.yaml
```

You can also use "Download my kubeconfig" in the account menu of ichor-ui. The
file holds the API server address and the cluster CA. It holds no secret. When
`kubectl` runs, it calls `ichor auth token`, which uses the login cache. You
need at least the `viewer` role to download it.

The login cache is a credential on your machine. It is stored in
`~/.ichor/auth/` with restricted permissions.

After a Kubernetes CA rotation, a kubeconfig downloaded earlier fails TLS. Get
a new one.

## Disable a user

Set `disabled: true` on the `IchorUser`, through the CLI or in Git. The effect
has two parts:

- In ichor-ui, the change applies within 10 seconds.
- For `kubectl`, the change applies at the next token refresh, at most 5
  minutes later.

Ichor has no way to take back a token that is already issued. The 5 minute
token lifetime is the limit.

To end every existing session of a user, revoke their sessions. Changing a
user's password also ends their earlier sessions. A session lasts at most 12
hours.

## Break-glass

Ichor keeps a break-glass credential that does not depend on login. It is the
`admin` client certificate in `~/.ichor/clusters/<name>/kubeconfig`, and the
Talos `os:admin` identity in the same folder. If ichor-ui is down, the issuer is
broken, or every user is locked out, use this credential.

Once users are on, keep this folder offline and use it only in an incident.
Do not confuse it with the break-glass write, which is a GitOps override.

## Not verified yet

These are unknown. Check them on a real cluster before you rely
on them:

- whether the kube-apiserver on a control plane can reach the ichor-ui service
  IP;
- whether Talos applies the login settings without a reboot;
- whether the kube-apiserver reloads them or restarts.
