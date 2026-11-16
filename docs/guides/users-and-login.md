# Users and login

Ichor can keep its own users, so people can sign in to ichor-ui and get their
own kubeconfig with no shared secret. This guide covers the steps. The design
is in [ADR 0094](../adr/0094-ichor-users-its-own-oidc-issuer-roles-and-per-user-kubeconfigs.md),
which builds on [ADR 0058](../adr/0058-oidc-login-and-kubernetes-impersonation-for-ichor-ui.md)
for logins from an external identity provider.

Status: accepted. The steps in the pages below are what it decides, plus the CLI
flags the implementation added (`--ca-file`, `--out`, `--bootstrap`).

## How a sign-in reaches the cluster

Roles are groups in Kubernetes, so RBAC decides what each role may do once a
sign-in has produced a token.

![How a sign-in reaches the cluster. A browser, the ichor CLI or kubectl signs in to ichor-ui on port 8443, which checks the IchorUser and issues an ID token. The kube-apiserver checks that token against ichor-ui's keys on port 8444, and RBAC decides each request from the user's groups. Without a sign-in the request is refused; an issued token cannot be taken back and expires within 5 minutes.](../images/users-and-login-sign-in.light.drawio.svg)

*Ichor's sign-in issues the token; the Kubernetes API server and RBAC decide what it may do.*

## The pages

Read them in this order, or go to the one you need. The sidebar lists them too.

1. [Turn users on](users-and-login/turn-users-on.md): the first administrator,
   the settings a new cluster gets and how to turn users on for an existing install.
2. [Without any sign-in](users-and-login/without-sign-in.md): what ichor-ui refuses
   when no sign-in is configured, and what to do instead.
3. [Expose ichor-ui over https](users-and-login/expose-ichor-ui.md): which
   certificate ichor-ui serves, and the recipes for an Ingress, a Gateway or a
   load balancer.
4. [Create users and roles](users-and-login/create-users.md): the built-in roles,
   the rules that keep them from being worn away, and the `ichor user` commands.
5. [Sign in and get a kubeconfig](users-and-login/sign-in.md): the CLI login, the
   kubeconfig, and a read-only talosconfig for `talosctl`.
6. [Disable a user and the override](users-and-login/disable-and-override.md):
   ending a user's access, and the credential that works without login.

## Client addresses behind a load balancer

The failed-login counters (see [Sign-in and sessions](../concepts/sign-in-and-sessions.md#wrong-passwords-slow-down)) key on the address ichor-ui sees.

> [!IMPORTANT]
> If every client arrives with the same address, they share one counter: a stranger's wrong
> passwords for `admin` then slow `admin` down for everyone.

This is the case whenever something between the client and the pod replaces the source address:

- a LoadBalancer or NodePort Service with the default `externalTrafficPolicy:
  Cluster` (the node SNATs the connection, so every client looks like the
  node; behind an L4 load balancer on a NodePort, a workstation and a
  pod of the outer cluster are indistinguishable),
- an L4 load balancer that does not keep the client address (no PROXY protocol
  support in ichor-ui, and none is planned).

Pick one of these:

1. **An L7 proxy that sets `X-Forwarded-For`** (an Ingress controller, a
   Gateway, nginx, HAProxy in http mode). Set `ichorUI.users.trustedProxyCIDRs`
   (the flag `--trusted-proxy-cidrs` of ichor-ui; `ichor init` has no option
   for it, so on an `ichor init` install add it to the arguments of the
   `ichor-ichor-ui` Deployment) to the proxy's address range, and only to that: ichor-ui then takes the rightmost `X-Forwarded-For` entry that is
   not in the list. Do not list a range that clients can reach directly, or they
   can write the header themselves and pick their own counter.
2. **`externalTrafficPolicy: Local`** on the ichor-ui Service (the chart value
   `ichorUI.service.externalTrafficPolicy`, or the same field on the Service of
   an `ichor init` install, which `kubectl patch` sets), so the node does not SNAT and the pod sees the real
   source. The load balancer must send only to nodes that run an ichor-ui pod
   (it health-checks them), and it must itself keep the client address.
3. If neither is possible, accept the shared counter and know its effect: it
   is a slowdown (up to 15 minutes for one user name), never a lock-out, and
   an administrator can still be reached with `kubectl` and the override
   credential below. Pods inside the cluster have their own addresses and are
   not affected.
