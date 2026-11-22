# Users and login

Ichor can keep its own users, so people sign in to ichor-ui and get their own
kubeconfig with no shared secret. The design is in
[ADR 0094](../adr/0094-ichor-users-its-own-oidc-issuer-roles-and-per-user-kubeconfigs.md),
which builds on [ADR 0058](../adr/0058-oidc-login-and-kubernetes-impersonation-for-ichor-ui.md)
for an external identity provider. The steps below add the CLI flags the
implementation added (`--ca-file`, `--out`, `--bootstrap`).

## How a sign-in reaches the cluster

![How a sign-in reaches the cluster. A browser, the ichor CLI or kubectl signs in to ichor-ui on port 8443, which checks the IchorUser and issues an ID token. The kube-apiserver checks that token against ichor-ui's keys on port 8444, and RBAC decides each request from the user's groups. Without a sign-in the request is refused; an issued token cannot be taken back and expires within 5 minutes.](../images/users-and-login-sign-in.light.drawio.svg)

*Ichor's sign-in issues the token; the Kubernetes API server and RBAC decide what it may do.*

## The pages

In this order, or jump to the one you need.

1. [Turn users on](users-and-login/turn-users-on.md): the first administrator and the settings.
2. [Without any sign-in](users-and-login/without-sign-in.md): what ichor-ui refuses, and what to do instead.
3. [Expose ichor-ui over https](users-and-login/expose-ichor-ui.md): the certificate, and recipes for an Ingress, a Gateway or a load balancer.
4. [Create users and roles](users-and-login/create-users.md): the built-in roles and the `ichor user` commands.
5. [Sign in and get a kubeconfig](users-and-login/sign-in.md): the CLI login, the kubeconfig and a read-only talosconfig.
6. [Disable a user and the override](users-and-login/disable-and-override.md): ending access, and the credential that works without login.

## Client addresses behind a load balancer

The failed-login counters (see [Sign-in and sessions](../concepts/sign-in-and-sessions.md#wrong-passwords-slow-down))
key on the address ichor-ui sees.

> [!IMPORTANT]
> If every client arrives with one address, they share one counter: a stranger's
> wrong passwords for `admin` slow `admin` down for everyone.

That happens when something between client and pod replaces the source address:
a LoadBalancer or NodePort Service with the default `externalTrafficPolicy:
Cluster` (the node SNATs the connection), or an L4 load balancer that does not
keep the client address (ichor-ui has no PROXY protocol support, and none is
planned). Pick one:

1. **An L7 proxy that sets `X-Forwarded-For`** (an Ingress controller, a Gateway, nginx, HAProxy in http mode). Set `ichorUI.users.trustedProxyCIDRs` (flag `--trusted-proxy-cidrs` of ichor-ui) to the proxy's range and only that: ichor-ui takes the rightmost `X-Forwarded-For` entry not in the list. A range clients can reach directly lets them pick their own counter. `ichor init` has no option for it: on an `ichor init` install, add the flag to the arguments of the `ichor-ichor-ui` Deployment.
2. **`externalTrafficPolicy: Local`** on the ichor-ui Service (chart value `ichorUI.service.externalTrafficPolicy`; `kubectl patch` on an `ichor init` install), so the pod sees the real source. The load balancer must send only to nodes that run an ichor-ui pod and keep the client address itself.
3. **Accept the shared counter.** It is a slowdown (up to 15 minutes for one user name), never a lock-out; an administrator still gets in with `kubectl` and the [override credential](users-and-login/disable-and-override.md#override). Pods inside the cluster have their own addresses and are unaffected.
