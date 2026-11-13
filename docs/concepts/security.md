---
title: "Security model"
description: "Who holds which credential in Ichor: the viewer, operator and admin roles and their subresource verbs, how credentials are issued through TalosCredentialRequest, and what the UI backend never holds."
sidebar_order: 40
---

# Security model

This page summarises the trust model. The full text is the "Trust" section of
[the architecture document](../design/architecture.md).

## Who can write to a node

Only the controller holds a Talos identity that can change a node. The CLI and
the UI change the world by writing custom resources, and the controller never
changes `spec`. This keeps validation, RBAC and audit identical however a
change arrives, including a hand-written resource: the admission webhooks
enforce the same checks the UI makes.

The secrets bundle (the Talos, Kubernetes and etcd CA keys) is the root of
trust. `ichor init` generates it locally, keeps your copy under
`~/.ichor/clusters/<name>/` as an age file sealed with a passphrase, and
pushes it into a Secret, which is the source of truth from then on. Resources
reference the Secret and never contain it. That local
copy, with the `admin` client certificate and the Talos `os:admin` identity in
it, works without any login: a broken issuer, a stopped ichor-ui or a deleted
user cannot lock you out. Keep it offline once users exist.

Which names and addresses each serving certificate carries, and what changing
them costs, is in [Certificate names](certificate-names.md).

## Roles

Roles are plain Kubernetes RBAC. Three ClusterRoles, `ichor-viewer`,
`ichor-operator` and `ichor-admin`, are bound to the groups `ichor:viewer`,
`ichor:operator` and `ichor:admin`. None of them holds `secrets`, `impersonate`
or `cluster-admin`.

![The three roles as nested sets: viewer inside operator inside admin, with the subresource verbs each one adds (approve and download for the operator; start, override, reclaim, update and download for the admin). Beside them, the path of a hand-written resource: authentication, RBAC, the admission webhooks, then storage and reconciliation. No role holds secrets, impersonate or cluster-admin.](../images/roles.light.drawio.svg)

*Each role includes the one inside it, and a hand-written resource gets the same checks as one made in the UI.*

| Role | Can |
|---|---|
| `viewer` | Read the Ichor resources. No Secrets, no users. |
| `operator` | Everything a viewer can, plus the routine writes: edit clusters, machines and patches, approve and reject machines, start non-destructive operations, and download a short-lived `os:reader` talosconfig. |
| `admin` | Everything an operator can, plus deleting machines and clusters, managing users and the trust configuration, the destructive operation starts, Cilium values, Secret references, the safety-check override, reclaim, and the downloads below. |

The checks that matter most are verbs on subresources, so that a role can be
cut precisely and the webhook can ask Kubernetes (with a SubjectAccessReview
for the requesting user) whether the caller holds one.

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

A requester can grant an Ichor user only roles below their own (an admin may grant any), and the last
admin cannot be deleted, disabled or demoted. The `IchorUser` webhook holds that
rule.

## Credentials: TalosCredentialRequest

ichor-ui has to read nodes, and to offer downloads, without holding a key that
can sign anything. It works like this:

1. The backend makes a key pair in memory.
2. It creates a [`TalosCredentialRequest`](../reference/crds/taloscredentialrequest.md)
   carrying the public key and what it wants to do.
3. The validating webhook checks the Kubernetes permission of the requester
   (for a download, the signed-in user, with the matching `download` verb).
4. The controller signs a certificate for the least Talos role the call needs
   (`os:reader`, `os:etcd:backup`, `os:operator`), valid for ten minutes, and
   deletes the request a couple of minutes later.

The private key never leaves the backend. Each admin-gated download leaves an
Event naming the user.

## What the UI backend never holds

- No CA private key, and no Kubernetes role that reads a Secret holding one.
- No Talos identity that can change a node: the backend has no method that
  writes to a node. Its own reads use an `os:reader` certificate.
- It may impersonate only the users and groups it is given by name (the
  built-in `ichor:` groups, the Ichor users the controller lists, and what the
  chart values name), never a `system:` group.
- The browser holds no Talos credentials at all, and no Kubernetes credential.
  Its token is for ichor-ui only.

ichor-ui serves HTTPS only.

## Users and sign-in

People sign in to ichor-ui through an external OIDC provider (users appear as
`oidc:<name>`), or through Ichor's own users, which are off until you turn them
on (users appear as `ichor:<name>`). Both are accepted by the kube-apiserver, so
RBAC and audit apply per user. A user's kubeconfig holds no secret: it runs
`ichor auth token` as an exec plugin. See
[Users and login](../guides/users-and-login.md) and
[Sign-in and sessions](sign-in-and-sessions.md).

Users never receive an admin talosconfig. Two paths hand Talos admin
credentials to a person, both admin-only: the CA-rotation hand-off.

## The provisioning network

Approving a machine uses Talos's maintenance API, which does not authenticate
the node at the other end. Ichor assumes a separate provisioning network and
does not enforce one. It pins the maintenance certificate seen at discovery,
and refuses to send the first configuration to a node that presents another
one. A machine that already runs another cluster's Talos is never given a
credential by the controller, with one exception: "Reset and add", where the
user supplies a short-lived talosconfig that is deleted when the reset ends.

For the settings that harden the clusters Ichor creates (SecureBoot, kernel
lockdown, kubelet serving certificates) see the
[hardening guide](../guides/hardening.md).
