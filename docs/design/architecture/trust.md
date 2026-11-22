---
sidebar_group: "Trust and recovery"
sidebar_order: 3
---

# Trust

![Trust in Ichor. The browser or CLI asks ichor-ui, which holds no CA key. For a read of the nodes or an admin download, ichor-ui makes a key pair in memory and creates a TalosCredentialRequest. The validating webhook checks the requester's Kubernetes permission, and the controller, which holds the CA keys from the secrets bundle Secret, signs a certificate for the least Talos role for ten minutes. The operator's own bundle in ~/.ichor reaches the nodes with no login.](../../images/trust.light.drawio.svg)

*Only the controller holds a CA key; ichor-ui gets a short-lived certificate for each call; the operator's bundle needs no login.*

## The secrets bundle

The Talos, Kubernetes and etcd CA keys are the root of trust. `ichor init`
generates them locally into `~/.ichor/clusters/<name>/` (0600, an age file under
a passphrase): the user's backup. They are pushed into a Secret that is the
source of truth from then on; CRs reference it and never contain it.

The passphrase comes from a file, an environment variable or, on a terminal, a
prompt or one generated and shown once. Nothing stores it. A plaintext file from
an earlier version loads with a warning; `ichor secrets encrypt` migrates it.

## Who holds what

- **Controller**: every CA private key; it can make any Talos or Kubernetes identity.
- **ichor-ui backend**: no CA key, and its role reads no Secret that holds one.
  It has no method that changes a node. It may impersonate only the groups and
  users named to it (the built-in `ichor:` groups, the Ichor users the
  controller lists, what the chart values name), never a `system:` group.
- **Browser**: no Talos or Kubernetes credential; its token is for ichor-ui only.

ichor-ui gets a certificate per call through a `TalosCredentialRequest`. The
webhook checks the requester's Kubernetes permission (the signed-in user, for a
download); the controller signs for the least role the call needs, for ten
minutes. The private key never leaves the backend.

| Call | Role |
|---|---|
| Node reads | `os:reader` |
| Support bundle, packet capture (admin) | `os:operator` |
| etcd snapshot (admin) | `os:etcd:backup` |
| New kubeconfig and talosconfig of a CA rotation in `AwaitingCredentials` | as the rotation needs; verb `download` on `talosclusters/credentials`, granted to nobody by default; a Warning Event first; valid a year unless asked for less |

An etcd restore into a live cluster is the controller's: the UI only lists the
snapshots a `TalosEtcdBackup` reports and writes the operation.

## The UI's TLS

ichor-ui serves HTTPS only, on 8443, with the operator's certificate
(`ichorUI.tls.secretName`) or a leaf from the Ichor serving CA. The controller
publishes that CA's public part as `ichor-ui-ca` (ConfigMap and Secret) for an
edge or a client with `--ca-file`. Certificates reload when their files change.
With an operator's certificate, a handshake for the Service name still gets the
Ichor certificate, so in-cluster clients verify ichor-ui by that name. `ichor ui`
(a port-forward) needs no https URL.

## Settings

Runtime settings (sign-in lifetimes, the default drift policy) are optional
fields of the `IchorInstallation` singleton, read live by the controller, the
webhooks and ichor-ui, and written as the signed-in user by the UI Settings page
or `ichor set settings`. The webhook splits write rights over
`ichorinstallations/version` and `/settings`, and requires a confirmation
annotation to enable reclaim. `--settings-read-only` makes them read-only.

## Sign-in

The kube-apiserver accepts both ways, so RBAC and audit apply per user.

- External OIDC (`ichorUI.oidc.*`): users are `oidc:<name>`.
- Ichor's own users (off until `ichorUI.users.enabled`): `ichor:<name>`, an
  `IchorUser` CR (display name, e-mail, roles, disabled flag) with no password
  field. Argon2id hashes live in the Secret `ichor-user-credentials`. ichor-ui
  is a small stateless OIDC issuer; the controller renders it into the
  kube-apiserver configuration.

A kubeconfig holds no secret: it runs `ichor auth token` as a client-go exec
plugin, which logs in through the browser and keeps a 5 minute ID token and a 12
hour refresh token on the user's machine. The browser keeps a refresh token in
`sessionStorage` (rotated on use, reuse revokes the family). Signing out, in the
UI or with `ichor auth logout`, revokes it at `/oidc/revoke` (RFC 7009).

## Roles

Plain Kubernetes RBAC: the ClusterRoles `ichor-viewer`, `ichor-operator` and
`ichor-admin`, bound to the groups `ichor:viewer`, `ichor:operator` and
`ichor:admin`. None gets `secrets`, `impersonate` or `cluster-admin`.

- The destructive starts (shutdown, CA rotation, key rotation, etcd restore,
  node reset) need their own verb, held by `ichor-admin` only.
- Setting a field that names a Secret needs `update` on
  `talosclusters/secret-references` and the like. The controller reads those
  Secrets with its own privileges, so an operator who may edit a cluster must
  not be able to repoint one.

## Talos credentials for people

No per-user Talos identity exists. Two admin-only paths hand a person Talos
admin credentials: the CLI hand-off and the UI download. A third hands out far
less: `ichor create talosconfig` and a UI download give an operator or admin an
`os:reader` talosconfig valid for one hour (twelve at most), signed through a
`TalosCredentialRequest`. It needs the verb `download` on
`talosclusters/talosconfig-reader`; each request is an Event naming the user.

## Override

The bundle in `~/.ichor/clusters/<name>/` keeps the `admin` client certificate
(`system:masters`) and the Talos `os:admin` identity. Neither depends on a login,
so a broken issuer, a stopped ichor-ui or a deleted user cannot lock the operator
out. Keep the bundle offline once users exist. See
[Users and login](../../guides/users-and-login.md).

## Reclaim: the one foreign credential

The controller holds no credential for a node of another cluster, except for
"Reset and add": a user with the `reclaim` verb on `machinereclaims` pastes a
foreign talosconfig into the UI to reset one machine. No installation switch exists.

- The credential is scoped (the controller dials only the request's address and
  ignores the file's endpoints), user-supplied and expiring: a Secret of type
  `ichor.maesh.dev/foreign-talosconfig` with a TTL (default 15 minutes, at most
  60), used for one reset and deleted at `Done`, `Failed` or `Expired`. A
  sweeper deletes any older than the maximum. It is the one credential deleted
  by time, not by an object's lifecycle.
- It is redacted from logs, Events, status, error text and the UI API's access log.
- `ichor-machine-reclaimer` (`create`, `get`, `list`, `update`, `reclaim` on
  `machinereclaims`) is bound to nobody by default. `ichor-admin` holds the same
  verbs; `ichor-operator` and `ichor-viewer` lack `reclaim`.
- The webhook checks the verb with a SubjectAccessReview on create and on every
  write of the typed UUID; `status.audit` keeps who requested and confirmed.
- A ValidatingAdmissionPolicy limits what ichor-ui's ServiceAccount may create
  in `ichor-system` to that Secret type.

## Approval

Approval uses Talos's insecure apply, which does not authenticate the node, so a
separate provisioning network is assumed. Ichor pins what it can: the
fingerprint of the maintenance certificate seen at discovery (or at the first
provisioning probe) is recorded in the machine's status, and the controller
refuses to send the first configuration, which carries the CA keys, to a node
that presents another. An operator who has checked a changed fingerprint accepts
it with the annotation `ichor.maesh.dev/trust-maintenance-fingerprint`.
