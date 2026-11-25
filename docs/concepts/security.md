---
title: "Security model"
description: "Who holds which credential in Ichor: the viewer, operator and admin roles and their subresource verbs, how credentials are issued through TalosCredentialRequest, and what the UI backend never holds."
sidebar_group: "Trust and access"
sidebar_order: 40
---

# Security model

Summary of the "Trust" section of [the architecture document](../design/architecture.md).

## Who can write to a node

Only the controller holds a Talos identity that can change a node. The CLI and
UI write custom resources, and the controller never changes `spec`, so
validation, RBAC and audit are the same however a change arrives, including a
hand-written resource: the admission webhooks enforce the UI's checks. Three CLI
commands write to a node with a credential you hold: `init` and
`restore cluster` (before the cluster exists or after it is lost) and
`reclaim node --talosconfig` (your own Talos credential, never stored).

The secrets bundle (Talos, Kubernetes and etcd CA keys) is the root of trust.
`ichor init` generates it locally, keeps your copy under
`~/.ichor/clusters/<name>/` as an age file sealed with a passphrase, and pushes
it into a Secret that is the source of truth from then on. Resources reference
the Secret and never contain it. The local copy (the `admin` client certificate
and the Talos `os:admin` identity) works without any login, so a broken issuer,
a stopped ichor-ui or a deleted user cannot lock you out. Keep it offline once
users exist.

Names and addresses in the serving certificates: [Certificate
names](certificate-names.md).

## Roles

Plain Kubernetes RBAC: ClusterRoles `ichor-viewer`, `ichor-operator` and
`ichor-admin`, bound to the groups `ichor:viewer`, `ichor:operator` and
`ichor:admin`. None holds `secrets`, `impersonate` or `cluster-admin`.

![The three roles as nested sets: viewer inside operator inside admin, with the subresource verbs each one adds (approve and download for the operator; start, override, reclaim, update and download for the admin). Beside them, the path of a hand-written resource: authentication, RBAC, the admission webhooks, then storage and reconciliation. No role of the three holds secrets, impersonate or cluster-admin.](../images/roles.light.drawio.svg)

*Each role includes the one inside it, and a hand-written resource gets the same checks as one made in the UI.*

| Role | Can |
|---|---|
| `viewer` | Read Ichor resources. No Secrets, no users. |
| `operator` | Plus the routine writes: edit clusters, machines and patches, approve and reject machines, start non-destructive operations, download a short-lived `os:reader` talosconfig. |
| `admin` | Plus deleting machines and clusters, users and trust configuration, destructive operation starts, Cilium values, Secret references, the safety-check override, reclaim and the downloads below. |

Checks are verbs on subresources, so a role can be cut precisely and the
webhook can ask Kubernetes (a SubjectAccessReview for the requesting user)
whether the caller holds one.

| Verb on | What it allows | Default holder |
|---|---|---|
| `start` on `talosoperations/ca-rotation`, `key-rotation`, `etcd-restore`, `node-reset`, `node-shutdown`, `shutdown` | Starting an operation that can take the cluster down or rewind it | admin |
| `update` on `talosclusters/cilium-values` | Changing the Cilium Helm values | admin |
| `update` on `talosclusters/secret-references` and the like | Setting or changing a field that names a Secret, which the controller reads with its own rights | admin |
| `update` on `talosclusters/authentication`, `ichorusers/password` | Trust configuration and user passwords | admin |
| `update` on `ichorinstallations/version` and `/settings` | Upgrading Ichor, changing its runtime settings | admin |
| `approve` on `talosmachines/approval` | Approving or rejecting a machine | operator, admin |
| `override` on `talosclusters`, `talosmachines`, `talosconfigpatches` | Going past a safety check, recorded as a Warning Event | admin |
| `reclaim` on `machinereclaims` | Resetting a machine of another cluster | admin (bind `ichor-machine-reclaimer` to delegate it) |
| `download` on `talosmachines/support-bundle`, `etcd-snapshot`, `packet-capture` | The admin-gated downloads | admin |
| `download` on `talosclusters/credentials` | The new kubeconfig and talosconfig of a CA rotation | admin |
| `download` on `talosclusters/talosconfig-reader` | A one-hour `os:reader` talosconfig for `talosctl` | operator, admin |

A requester grants an Ichor user only roles they hold themselves (an admin may
grant any); the last admin cannot be deleted, disabled or demoted. The `IchorUser`
webhook holds that rule.

`ichor-bundle-secret-role` is bound to nobody by default. It grants `get` on the
Secrets named in the chart value `rbac.bundleSecret.names` (a cluster's
`secretsRef`, `<cluster>-secrets` by default) and nothing else; with no names it
is not installed. The four commands that read the bundle (`ichor create
credentials`, `ichor export bundle`, the credential hand-off of `ichor rotate
ca`, `ichor adopt --add-machine`) need it bound by a RoleBinding in that
namespace. It holds every CA key, so bind it only to people you would give
`os:admin`.

## Credentials: TalosCredentialRequest

ichor-ui reads nodes and offers downloads without holding a key that can sign
anything:

1. The backend makes a key pair in memory.
2. It creates a [`TalosCredentialRequest`](../reference/crds/taloscredentialrequest.md)
   with the public key and what it wants to do.
3. The validating webhook checks the requester's Kubernetes permission (for a
   download, the signed-in user, with the matching `download` verb).
4. The controller signs a ten-minute certificate for the least Talos role the
   call needs (`os:reader`, `os:etcd:backup`, `os:operator`) and deletes the
   request a couple of minutes later.

The private key never leaves the backend. Each admin-gated download leaves an
Event naming the user.

## What the UI backend never holds

- No CA private key, and no Kubernetes role that reads a Secret holding one.
- No Talos identity that can change a node: it has no method that writes to a
  node, and its own reads use an `os:reader` certificate.
- Impersonation only of users and groups it is given by name (the `ichor:`
  groups, the Ichor users the controller lists, what the chart values name),
  never a `system:` group.
- The browser holds no Talos or Kubernetes credentials; its token is for
  ichor-ui only. ichor-ui serves HTTPS only.

## Users and sign-in

People sign in to ichor-ui through an external OIDC provider (`oidc:<name>`) or
Ichor's own users, off until you turn them on (`ichor:<name>`). The
kube-apiserver accepts both, so RBAC and audit apply per user. A user's
kubeconfig holds no secret: it runs `ichor auth token` as an exec plugin. See
[Users and login](../guides/users-and-login.md) and [Sign-in and
sessions](sign-in-and-sessions.md). Users never receive an admin talosconfig;
the CA-rotation hand-off is admin-only.

## The provisioning network

The maintenance API does not authenticate the node at the other end. Ichor
assumes a separate provisioning network and does not enforce one. It pins the
maintenance certificate seen at discovery and refuses to send the first
configuration to a node presenting another. A machine running another cluster's
Talos is never given a credential by the controller, except "Reset and add",
where you supply a short-lived talosconfig deleted when the reset ends.

Settings that harden the clusters Ichor creates (SecureBoot, kernel lockdown,
kubelet serving certificates): [hardening guide](../guides/hardening.md).
