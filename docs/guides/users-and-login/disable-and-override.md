---
sidebar_order: 6
---

# Disable a user and the override

## Disable a user

Set `disabled: true` on the `IchorUser`, through the CLI or with kubectl. The effect
has two parts:

- In ichor-ui, the change applies within 10 seconds.
- For `kubectl`, the change applies at the next token refresh, at most 5
  minutes later.

Ichor has no way to take back a token that is already issued. The 5 minute
token lifetime is the limit.

To end every existing session of a user without disabling them, revoke their
sessions (`ichor user revoke-sessions`). What ends a session, and what it leaves
running for a few minutes, is in [Sign-in and sessions](../../concepts/sign-in-and-sessions.md).

## Override

Ichor keeps an override credential that does not depend on login. It is the
`admin` client certificate in `~/.ichor/clusters/<name>/kubeconfig`, and the
Talos `os:admin` identity in the same folder. If ichor-ui is down, the issuer is
broken, or every user is locked out, use this credential.

> [!WARNING]
> Once users are on, keep this folder offline and use it only in an incident.
The bundle in that folder (`secrets.yaml`) is encrypted with a passphrase
(see [Getting started](../getting-started.md)); the kubeconfig is not, because
`kubectl` reads it directly. The bundle holds the CAs. With it, `ichor create credentials --bundle` makes a
new kubeconfig and talosconfig even when the cluster is unreachable and no
kubeconfig is left, given the API server's address and a Talos endpoint:
`ichor create credentials NAME --bundle secrets.yaml --kube-server
https://<address>:6443 --talos-endpoints <address>`. The bundle holds no
addresses, so note them where you keep the bundle.
Do not confuse it with the override write, which is an RBAC verb that guards destructive operations.
