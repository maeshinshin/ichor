---
sidebar_group: "Trust and recovery"
sidebar_order: 3
---

# Trust

The secrets bundle (Talos, Kubernetes and etcd CA keys) is the root of trust.
It is generated locally by `ichor init`, stored under `~/.ichor/clusters/<name>/`
with 0600 permissions as the user's backup, encrypted as an age file under a
passphrase, and pushed into a Secret that becomes the source of truth
from then on. CRs reference it; they never contain it. The passphrase comes from
a file, an environment variable or, on a terminal, a prompt or one generated and
shown once; nothing stores it. A plaintext file from an earlier version still
loads, with a warning, and `ichor secrets encrypt` migrates it.

Only the controller holds a CA private key and so can make any Talos or
Kubernetes identity. The UI backend holds none, and its Kubernetes
role reads no Secret that holds one: for its own reads of the nodes (an
`os:reader` certificate) and for the three admin-gated downloads (support bundle,
etcd snapshot, packet capture) it makes a key pair in memory and asks
the controller to sign it by creating a `TalosCredentialRequest`. The validating
webhook checks the Kubernetes permission of the requester (the signed-in user,
for a download) and the controller signs for the least Talos role the call needs
(`os:reader`, `os:etcd:backup`, `os:operator`) and for ten minutes; the private
key never leaves the backend. The browser holds no Talos credentials, and the
backend has no method that changes a node. A fourth admin-gated download hands the operator the new kubeconfig and talosconfig of a CA rotation
that waits in `AwaitingCredentials`; it is signed the same way, needs the
verb `download` on `talosclusters/credentials`, is granted to nobody by default,
leaves a Warning Event first, and lasts a year unless the operator asks for less.
The backend may impersonate only the users and groups it is given by name: the built-in `ichor:` groups, the Ichor users the controller lists,
and what the chart values name; never a `system:` group. The restore of an etcd
snapshot into a live cluster is the controller's, not the UI's: the UI backend only lists the
snapshots a `TalosEtcdBackup` reports and writes the operation. In the first release the UI is reached through
`ichor ui` (a port-forward), which needs no https URL.

ichor-ui serves HTTPS only, on 8443, never plain HTTP to the network.
The certificate is the operator's (`ichorUI.tls.secretName`) or a leaf from the
Ichor serving CA that the controller keeps for every install. The controller
publishes the CA, public part only, as `ichor-ui-ca` (ConfigMap and Secret) so an
edge or a client with `--ca-file` can verify the backend; certificates are
reloaded when their files change. With an operator's certificate, a handshake
for the Service name still gets the Ichor certificate, so in-cluster clients
verify ichor-ui by that name (2026-11-03).

Ichor's runtime settings (the sign-in lifetimes, the default
drift policy) are optional fields of the `IchorInstallation` singleton, read live
by the controller, the webhooks and ichor-ui, and written by the UI Settings page
or `ichor set settings` as the signed-in user. The webhook splits write rights over
the subresources `ichorinstallations/version` and `/settings`, and
requires a confirmation annotation to enable reclaim. A deployment can make them
read-only with `--settings-read-only`.

Users sign in to ichor-ui in one of two ways, and the kube-apiserver accepts
both, so RBAC and audit apply per user.

- An external OIDC provider, configured with `ichorUI.oidc.*`. Its
  users are named `oidc:<name>` in Kubernetes.
- Ichor's own users, off until `ichorUI.users.enabled` is set. A
  user is an `IchorUser` CR with a display name, an e-mail, roles and a
  disabled flag. It has no password field. Password hashes (argon2id) live in
  one Secret, `ichor-user-credentials`. ichor-ui is a small stateless OIDC
  issuer for these users, and the controller renders it into the
  kube-apiserver's configuration beside the external provider. Its users are
  named `ichor:<name>`.

Roles are plain Kubernetes RBAC. Three built-in ClusterRoles, `ichor-viewer`,
`ichor-operator` and `ichor-admin`, are bound to the groups `ichor:viewer`,
`ichor:operator` and `ichor:admin`. None of them gets `secrets`,
`impersonate` or `cluster-admin`. The destructive operation starts (shutdown,
CA rotation, key rotation, etcd restore, node reset and shutdown) need their own verb, held
by `ichor-admin` only. So does setting or changing a field that names a Secret
(`update` on `talosclusters/secret-references` and the like): the
controller reads those Secrets with its own privileges, so an operator who may
edit a cluster must not be able to repoint one.

A user's kubeconfig holds no secret. It runs `ichor auth token` as a client-go
exec plugin. That command logs in through the browser and keeps a 5 minute ID
token and a 12 hour refresh token on the user's machine. The browser never
holds a Kubernetes credential; its token is for ichor-ui only. The browser keeps
its session going with a refresh token held in `sessionStorage` (rotated on use,
reuse revokes the family), and signing out, in the UI or with `ichor auth
logout`, revokes it at the issuer's RFC 7009 endpoint `/oidc/revoke`.

Users never get an admin talosconfig, because no per-user Talos identity exists. Two
paths can hand Talos admin credentials to a person, and both stay admin-only: the
CLI hand-off and the UI download. A third hands out much
less: `ichor create talosconfig` and a UI download give an operator or admin an
`os:reader` talosconfig valid for one hour (twelve at most), signed through a
`TalosCredentialRequest` like the backend's own reads; it needs the verb
`download` on `talosclusters/talosconfig-reader`, and each request is an Event
with the user's name.

Override: the secrets bundle keeps the `admin` client certificate
(`system:masters`) and the Talos `os:admin` identity in
`~/.ichor/clusters/<name>/`. Neither depends on any login. A broken issuer, a
stopped ichor-ui or a deleted user cannot lock the operator out. Keep this
bundle offline once users exist. The guide is [Users and login](../../guides/users-and-login.md).

Exception: reclaim. Ichor's controller never holds a credential for
a node of another cluster, with one exception, "Reset and add".
A user who holds the `reclaim` verb on `machinereclaims` can paste a foreign
talosconfig into the UI to reset one machine; there is no installation switch. The credential is scoped (the controller dials only the
request's address and ignores the endpoints in the file), user-supplied, and
expiring: it is a Secret of type `ichor.maesh.dev/foreign-talosconfig` with a
TTL (default 15 minutes, at most 60), used for one reset, and deleted at
`Done`, `Failed` or `Expired`; a sweeper deletes any such Secret older than the
maximum TTL. It is the one credential in Ichor that is deleted by time rather
than by an object's lifecycle. It is redacted from logs, Events, status, error
text and the UI API's access log. The ClusterRole `ichor-machine-reclaimer`
(`create`, `get`, `list`, `update` and the verb `reclaim` on `machinereclaims`)
is not bound by default, but `ichor-admin` (group `ichor:admin`) holds the
same verbs by default, `reclaim` included, and the ClusterRole is there for
granting them to anyone else; `ichor-operator` and `ichor-viewer` do not get
the verb; the webhook checks the verb with a
SubjectAccessReview on create and on every write of the typed UUID, and
`status.audit` keeps who requested and confirmed it. A
ValidatingAdmissionPolicy limits what ichor-ui's ServiceAccount may create in
`ichor-system` to that Secret type.

Approval uses Talos's insecure apply, which does not authenticate the node.
A separate provisioning network is assumed; the docs say so. Ichor pins what it can:
the fingerprint of the maintenance certificate seen at discovery (or at the first
provisioning probe) is recorded in the machine's status, and the controller refuses to
send the first configuration, which carries the CA keys, to a node that presents another
one. An operator who has checked a changed fingerprint accepts it with the
annotation `ichor.maesh.dev/trust-maintenance-fingerprint`.
