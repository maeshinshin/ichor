# Ichor architecture

Status: living document; Ichor is alpha. This document is the source of truth for the
project.

## Summary

Ichor manages Talos Linux clusters through Kubernetes custom resources. A
controller renders machine configuration from typed resources and applies it
to nodes, walks upgrades through the cluster, backs up etcd, and handles the
lifecycle of nodes from maintenance mode to removal. A CLI and a web UI expose
the same operations.

Ichor stops at the Talos layer, with one component it installs itself: Cilium is the only CNI and Ichor's own Helm release. Storage systems,
monitoring and any GitOps tool are the user's business; Ichor only prepares the
Talos side for them and coordinates with them around node operations.

## Motivation

Running Talos with `talosctl` works well for one person and a handful of
nodes. It gets awkward when machine configs live in a directory of YAML files,
when several people need to see what's applied where, and when upgrades have
to be sequenced by hand. Putting the configuration into the Kubernetes API
gives you RBAC, audit, webhooks, and a place to hang a UI, without
inventing a new control plane.

### Goals

- Declarative machine configuration as Kubernetes resources, with drift
  detection and a view of the effective config per node.
- Node lifecycle: discover a node in maintenance mode, approve it in a UI,
  install, join, and later remove it safely (drain, etcd membership, reset).
- Rolling upgrades of Talos and Kubernetes, with the cluster lock, etcd quorum
  and storage evacuation taken care of.
- etcd backup to S3-compatible storage, and recovery from nothing but the
  secrets bundle and a snapshot.
- Bootstrap of Cilium for a brand-new cluster, with no Git repository needed.
- CLI and UI parity: anything you can do in one, you can do in the other.

### Non-goals

- Getting machines to maintenance mode. Boot the ISO however you like.
- Storage or monitoring, and any CNI other than Cilium.
- Workloads. Ichor manages the cluster (Talos, Kubernetes, Cilium) and not what
  runs on it, and it does not sync its own resources with a Git repository; GitOps
  of workloads, or of Ichor's resources, is the user's tooling.
- Replacing the storage system's own controller. Volumes, replicas and disk
  state belong to Longhorn (or whatever you run).
- Multi-cluster management. One Ichor manages one cluster, and the UI is built
  for that.
- Air-gapped environments. Nothing is built or tested for an
  installation with no route to public registries; registry mirrors exist for
  pull-through caches and private registries.
- IPv6, PXE provisioning. Later, maybe.
- Know about GitOps tools. Ichor does not detect GitOps tools, does not open
  pull requests and does not refuse a write because a tool tracks the resource.

## Overview

```
operator ──┬── ichor CLI ─────────┐
           └── browser ─▶ ichor-ui │
                                   ▼
                     ┌────────────────────────────────────────────────────────────┐
                     │              kube-apiserver (CRs, Secrets, Leases)         │
                     └──────▲──────────────────────────────────────▲──────────────┘
                  reconcile │                             run Jobs │
                 ┌──────────┴───────┐                 ┌────────────┴──────┐
                 │ ichor-controller │                 │ ichor-installer,  │
                 └──────────┬───────┘                 │ ichor-cni         │
                            │                         └───────────────────┘
                            │ Talos API
                            ▼
      node in maintenance mode ──approve──▶ Talos node (control plane / worker)
```

Three deliverables come out of one repository:

- `ichor`, the CLI. Bootstraps the first node, adopts existing clusters, and
  offers every operation the UI has. It is also the recovery path that does not
  depend on a running controller.
- `ichor-controller` (plus `ichor-installer`, a Job image). The controller is
  the only component that writes to Talos nodes. Two replicas, leader election.
- `ichor-ui`, a Go backend that embeds the Preact SPA. It reads and writes CRs,
  reads logs and state from the Talos API, and never writes to nodes.

`api/` (CRD types) and `proto/` (Connect definitions) are the shared contracts.
Deliverables don't import each other; shared code lives under `pkg/`. The
controller's entry point stays at kubebuilder's `cmd/main.go`, which the
generated Dockerfile and Makefile are tied to; the CLI and the UI backend are
added beside it. The Kustomize base under `config/` is the source of truth for
manifests, and the Helm chart in `dist/chart` is generated from it.

### Data flow

Every write, from the UI, the CLI or a user's own tooling, is a change to a custom
resource. The controller is the only thing that turns those into Talos API
calls. This keeps validation, RBAC and audit identical regardless of how a
change arrived. Reads come from two places: CR status (via the API
server) and raw node state and logs (via the Talos API, read-only).

Long-running work — upgrades, hand-overs, topology changes — keeps its state
machine in CR status, not in memory. A controller restart or a leader change
resumes from where it left off.

### Trust

The secrets bundle (Talos, Kubernetes and etcd CA keys) is the root of trust.
It is generated locally by `ichor init`, stored under `~/.ichor/clusters/<name>/`
with 0600 permissions as the user's backup, and pushed into a Secret that
becomes the source of truth from then on. CRs reference it; they never contain
it.

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
or `ichor settings` as the signed-in user. The webhook splits write rights over
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
CA rotation, etcd restore, node reset and shutdown) need their own verb, held
by `ichor-admin` only. So does setting or changing a field that names a Secret
(`update` on `talosclusters/secret-references` and the like): the
controller reads those Secrets with its own privileges, so an operator who may
edit a cluster must not be able to repoint one.

A user's kubeconfig holds no secret. It runs `ichor auth token` as a client-go
exec plugin. That command logs in through the browser and keeps a 5 minute ID
token and a 12 hour refresh token on the user's machine. The browser never
holds a Kubernetes credential; its token is for ichor-ui only.

Users never get a talosconfig, because no per-user Talos identity exists. Two
paths can hand Talos credentials to a person, and both stay admin-only: the
CLI hand-off and the UI download.

Override: the secrets bundle keeps the `admin` client certificate
(`system:masters`) and the Talos `os:admin` identity in
`~/.ichor/clusters/<name>/`. Neither depends on any login. A broken issuer, a
stopped ichor-ui or a deleted user cannot lock the operator out. Keep this
bundle offline once users exist. The guide is `../guides/users-and-login.md`.

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
is in no default role, admin included; the webhook checks the verb with a
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

### Deployment

The default is self-managed: Ichor runs inside the cluster it manages. The
controller and UI run with two replicas, a PodDisruptionBudget and anti-affinity,
except on clusters with a single schedulable node, where the installer drops to
one replica and no PDB (otherwise a drain never finishes).

Ichor's own users and login are off by default. Turning them on
needs three things a default install lacks: an https URL that browsers and the
CLI can reach (`ichorUI.users.externalURL`, set by `ichor init --ui-url`), a
Talos v1.14 contract or later (earlier contracts can carry only one issuer),
and an `ichor config apply` rollout of the control planes. ichor-ui is the
issuer. The kube-apiserver reads discovery and keys from a ClusterIP listener
on port 8444, signed by an Ichor-made CA, so the external URL does not have to
be reachable from the control planes. Ichor installs no ingress. Expose
ichor-ui with an Ingress, a Gateway or a LoadBalancer. ichor-ui is HTTPS only on
8443, so an edge re-encrypts to it and verifies the backend against
`ichor-ui-ca`, or a plain L4 load balancer passes through the certificate the
operator supplied with `ichorUI.tls.secretName`.

A Helm install gets an `IchorInstallation` created once by the
controller with `spec.deployment.manager: helm`; Helm then owns upgrades and
`ichor upgrade ichor` refuses.

Network policy. Cilium is guaranteed, so
`ichor-system` gets a `CiliumNetworkPolicy` default
deny plus one allow per flow, using Cilium's entities (`kube-apiserver`, `host`,
`remote-node`, `world`) for peers that run on the host network. They cannot ship
in `dist/install.yaml` because Cilium's CRD does not exist yet, so `ichor init`
applies Ichor's after Cilium is Ready, the installer Job re-applies them on
every upgrade when the CRD exists, and the chart renders them behind
`networkPolicy.cilium.enabled`. The webhook and OIDC ingress rules also admit `remote-node`,
because the apiserver's calls to a pod on another node arrive from the
control plane's `cilium_host` address.

Verified on hardware: a host-network kube-apiserver reaches a
Service ClusterIP; Talos applies the issuer document without a reboot but the
kube-apiserver restarts (15 to 45 s without `/readyz` per node), which is why
the rollout is one control plane at a time. Talos does not validate
the document, so Ichor validates it before rendering.

## Principles

1. Machine config is never stored in a CR. CRs hold the ingredients
   (versions, patches, per-node values); the controller renders on every
   reconcile.
2. Secrets are referenced, never embedded.
3. Status holds hashes and state, never rendered config.
4. The Talos version contract is pinned in the CR, so a library bump cannot
   silently change what gets applied.
5. The controller never writes `spec`, so it never fights whatever tool the user
   keeps the CRs in.
6. Disruptive operations (upgrades, reboots, CA rotation, topology changes)
   take a per-cluster Lease. One at a time.
7. Progress lives in status and resumes idempotently. (A live etcd restore
   cannot keep a status, since it rolls the API back; its marker in the backup
   storage is the exception)
8. CLI and UI are equivalent. The only CLI-only operations are the ones that
   happen before a cluster exists.
9. There is always a recovery path that needs no working controller.
10. Dangerous-operation checks live in shared code and are enforced by the UI
    backend and by webhooks, never only in the frontend.

## API

Group `ichor.maesh.dev`, version `v1alpha1`.

| Kind | Scope | Purpose | In Git |
|---|---|---|---|
| `TalosCluster` | namespaced | Cluster-wide settings: endpoint, versions, topology, Cilium, monitoring, integrations | yes |
| `TalosMachine` | namespaced | One node: identity, role, labels, disk roles, per-node values | yes, once approved |
| `TalosConfigPatch` | namespaced | A typed config patch with a layer, priority and target | yes |
| `TalosStorage` | namespaced | Thin integration with a storage system: node selector, disk preset, evacuation settings | yes |
| `TalosEtcdBackup` | namespaced | Backup schedule, destination, retention, encryption | yes |
| `TalosOperation` | namespaced | A Job-style run of one action over the cluster or a node: a Talos, Kubernetes or Ichor upgrade, a kube-proxy switch or Cilium chart upgrade (`cni`), shutdown, CA rotation, config rollout, node action, etcd restore | no |
| `IchorInstallation` | cluster, singleton | Ichor's own version | yes |
| `IchorUser` | namespaced | One of Ichor's own users: display name, e-mail, roles, disabled flag | yes |

There are no CRDs for installing CNI, storage or monitoring. Their Talos-side
needs are expressed on `TalosCluster`, `TalosMachine` and `TalosStorage`.

### Typed configuration

`TalosConfigPatch.spec.documents[]` holds the Talos config documents as typed
fields, restricted to an allow-list of kinds. There is no raw-YAML field. The
reasons:

- Machinery's types are Talos's own definition; we don't maintain a parallel
  catalogue that drifts.
- A CRD schema keeps an unknown field out of storage — by default the API
  server prunes it (logs a warning, persists the object without it) rather
  than rejecting the request, but either way the value a field's absence was
  meant to keep out never reaches a node.
- A newer machinery renders config for older Talos versions when a version
  contract is set. The reverse doesn't hold, so supporting a newly released
  Talos means bumping machinery.

The types cannot be embedded directly. Machinery is YAML-first and carries no
JSON tags, so controller-gen refuses them outright, and several field types
(`ByteSize`, CEL expressions, the `netip` wrappers in the network documents)
have no exported fields to describe. Instead the mirror types are generated
from machinery with a `go generate` tool, together with conversion in both
directions and a round-trip test per kind; machinery stays the only place
where the field catalogue lives.

A handful of fields need the generator's type-keyed substitution and drop
rules to be overridden per field instead, because the type alone can't say
what to do with them:

- Eleven fields are `meta.Unstructured` — Talos's own escape hatch for a
  pass-through to another project's schema (kubelet's configuration, an
  audit policy, a resource list, a seccomp profile). A type-keyed rule can
  only drop all eleven or none; a field-level override table drops five
  (controller-gen errors, or no single schema to mirror) and mirrors six as
  their upstream type instead: five with no new dependency, plus
  `machine.kubelet.credentialProviderConfig`, which takes one (`k8s.io/kubelet`,
  an official `k8s.io/*` staging module versioned in lockstep with
  Kubernetes, the same class of dependency the other five come from). Under
  a typed-only API an absent field is an inexpressible one, so a commonly
  wanted field being absent is a product gap, not a saved dependency —
  that's why this one took the dependency where the other five didn't need
  to. `machine.kubelet.extraConfig` still gets nothing: what blocks it was
  never a dependency but a `float32`, which controller-gen only emits under
  `allowDangerousTypes` — and that flag applies to the whole API package, so
  one field would relax the type safety of every CRD in it.
- Nine more fields are plain strings or certificate pairs, not
  `meta.Unstructured`, but carry cluster secret material — join tokens, CA
  key pairs, encryption keys. These are dropped unconditionally, by a
  separate table, for a different reason than the eleven above: not because
  they can't be represented, but because a `TalosConfigPatch` must not be
  able to overwrite the real values the secrets bundle generates, however
  narrowly the patch claims to be scoped.

Both are the follow-up.

No validating admission webhook rejects a `TalosConfigPatch` carrying a
secret field. Dropping the fields from the schema is what keeps the value
out of storage, and that holds whatever the caller does, so a webhook adds
nothing to the security property and only improves the error message. In
exchange it would put `failurePolicy` on the write path: `Fail` means a
controller outage blocks every configuration change, `Ignore` means the
guard is absent exactly when it is invoked. Ichor's own clients ask for
strict field validation instead, so the CLI and the UI backend report the
field loudly, and `kubectl` is strict by default. A client that opts out of
strict validation gets pruning, which is the safe outcome anyway.

Ichor adds what the types can't carry — whether a field needs a reboot,
whether it can only be set at install time, which Talos version introduced
it — in a table keyed by field path. Unknown paths are treated as
reboot-required.

`adopt` reads existing node configs with the same types, so an existing cluster
imports as-is. Kinds outside the allow-list are listed, and the user chooses
between extending the list and dropping them. Nothing is dropped silently.

### Patch layers

Patches apply bottom-up:

1. System layer, generated by Ichor: CNI settings, kube-proxy, KubePrism,
   volume configs derived from disk roles, kernel modules and sysctls for
   storage presets, metrics exposure, control-plane taints.
2. Cluster, 3. role, 4. selector, 5. machine (per-node values and
   machine-specific patches).

Within a layer, `priority` orders them. The effective-config view in the UI
shows which layer each value came from.

### Validation

Webhooks enforce, among other things: control-plane count in {1, 3, 5}; no
approving more control planes than configured; never deleting the last
control plane; no `schedulable: false` with zero workers; CNI preset is
`cilium` and immutable; Cilium values are validated and may not set Ichor-owned
keys; kube-proxy mode changes only with an explicit
annotation; disk roles that require reinstall are immutable after install;
`TalosCluster` and `TalosMachine` can only be deleted with
`ichor.maesh.dev/allow-delete: "true"` (so a prune by the user's own tooling can't cascade into
node resets); removing an Ichor finalizer, resetting a node and restoring etcd
need the override verb; Talos/Kubernetes version pairs and upgrade paths are checked, and a version the
webhook admits is the release set's, never a downgrade.

## Design

### Bootstrap

```
ichor init --preset standard [--cni-tunnel vxlan|geneve] [--ui-url URL]
```

1. Generate the secrets bundle locally; nag the user to back it up.
2. Settle the shape of the cluster: topology preset, endpoint (a VIP is
   recommended even for one node, because changing the endpoint later means
   re-issuing certificates), disk roles for the first node.
3. Apply the rendered config to the first control plane. Its inline manifest
   carries only Cilium, the one component Kubernetes cannot start without: the
   chart pinned in the binary, rendered with Helm's Go SDK, no network at init
   time and the same bytes every run. Everything else is applied
   later through the API server, with every write retried for a few minutes on
   a transient error.
4. Bootstrap etcd, fetch the kubeconfig, store the bundle as a Secret, apply
   Ichor's install manifest.
5. Create the CRs and the `IchorInstallation`, then apply
   Ichor's NetworkPolicies once Cilium is Ready.
6. Wait for the `CNIHandover` condition on the `TalosCluster`. `init` prints an
   `init: waiting for ...` line per poll, and the last reason when the wait runs
   out; the controller keeps working either way.

There are no flags to choose a CNI.
Adding later nodes goes through the normal approval flow; `init` is the
special case of approving the first control plane.

`ichor adopt` brings a running Talos cluster that Ichor did not create under
management. It needs Talos v1.12 or later on every node it adopts
and refuses an older or mixed cluster with "upgrade Talos first", writing
nothing. Above that floor, a cluster older than this Ichor's release set is adopted as "behind"
and rolled with `ichor upgrade cluster`; a newer one is refused. It works in four phases, and nothing is applied to a node until the
last one:

1. Plan (`--plan` stops here and writes nothing). Read-only inspection with
   the operator's own talosconfig: Talos and Kubernetes versions, the CNI and
   where it came from, the kube-proxy setting, KubePrism, inline manifests
   and each approved machine's live config. The `versionContract` is probed
   from the running version down to v1.12 and the one with the fewest
   unrepresentable paths is chosen (`--version-contract` overrides it).
2. Write, after consent. The bundle Secret, the `TalosCluster` (annotation
   `ichor.maesh.dev/adopted`), the `TalosMachine`s (identity read over the
   authenticated API), the `TalosConfigPatch`es and, if Ichor is not running,
   Ichor's install manifest. Every `TalosMachine` carries the annotation
   `ichor.maesh.dev/hold-apply`: the reconciler computes and reports drift
   (`AdoptionHeld`) and never applies.
3. Cilium takeover (see "Cilium"), skipped for an unmanaged CNI. A cluster
   with no CNI at all is different: its nodes are NotReady, so
   Ichor's own pods could not start. Adopt then installs Ichor's pinned Cilium
   itself, in process, as Ichor's own Helm release, and waits for the nodes to
   be Ready before it installs Ichor or writes any CR; the order is Cilium,
   Ichor, CRs, hand-over, release. The cluster is adopted as the managed
   `cilium` preset and `--cni-unmanaged` is refused there. This needs KubePrism
   on port 7445 on every node and a pod CIDR on every node.
4. Release of the hold, one machine at a time, control planes first, only
   after a live zero-drift check passes. The controller then applies the
   first render under the cluster lock like any rollout.

The old "zero drift or nothing" rule now sorts differences into three classes.
Class A is representable: sysctl and kernel-module documents, and values that
have spec fields (subnets, install disk); adopt stores them as
patches. Class B is Ichor-owned (the Cilium inline entry, KubePrism,
kube-proxy when replaced, removal of Talos's flannel); the first render
changes them, so they are listed and need consent, and they do not block.
Class C is everything else, including other inline or extra manifests; it
blocks that machine, with the path list and a hint, and adopt leaves it
untouched.

Secrets. `--secrets-bundle FILE` takes a `talosctl gen secrets` file. With
`--recover-secrets` (and `--recover-from ADDRESS`, a control plane) adopt
rebuilds the bundle from that node's machine config, read-only, and stores it
under `~/.ichor/clusters/<name>/` with mode 0600. A wrong or partial bundle
cannot get through, because it shows up as secret-field drift and no patch is
produced. After adoption the controller uses a certificate minted from the
bundle. The previous administrators' client certificates stay valid, so adopt
prints, and the UI shows as a dismissible notice (condition
`CARotationAdvised`), the advice to run `ichor cluster rotate-ca`. Nothing
starts a rotation.

Ichor can also be installed with Helm: `helm install` of the chart
in `dist/chart` (published as `oci://ghcr.io/maeshinshin/charts/ichor` once a
release tag is pushed) installs Ichor, not a cluster. `ichor adopt` then
detects the Helm release, skips its own install step, never creates an
`IchorInstallation`, requires Ichor's version to equal the CLI's, and registers
the cluster. A Helm-installed Ichor is upgraded with `helm upgrade`. The chart
refuses to install on a cluster with no pod network (`preflight.cni`); that one
case is covered by `ichor cni bootstrap`, which runs the same Cilium step as
adopt. The chart never ships Cilium. See `docs/guides/install-with-helm.md`.

### Cilium

Cilium is Ichor's own Helm release. It boots from the inline manifest;
once the DaemonSet is Ready the `ichor-cni` Job (installer image, hidden
`ichor cni helm apply`, host network via KubePrism) adopts the bootstrapped
objects as release `cilium` with `TakeOwnership`, and the controller then
removes the inline entry (`CNIHandover`). Removing it matters: Talos only
server-side applies bootstrap manifests and re-applies them on a Kubernetes
upgrade, so a stale entry would roll Cilium back. The Job needs
cluster-admin-equivalent rights, so the controller does not run Helm itself.

Values. Cilium's live values are Ichor defaults, then the user's layer in
`spec.cni.cilium.values`, then Ichor-owned keys (the Talos-required ones, the
KubePrism address, `kubeProxyReplacement`, `tunnelProtocol`). They are edited
in the UI (Cilium page) or with `ichor cni values get|set|edit|diff`, validated
by one function in the webhook, the backend and the controller, and applied by
the Job.

Upgrades. A new Ichor release carries new pinned charts. Cilium is upgraded
only when the user starts a `cni` operation (`ichor cni upgrade`, or the UI),
at most one minor at a time, rolled back by Helm on failure. The cluster rollout runs that operation last, as its Cilium step.

Takeover. `ichor adopt` can make a Cilium that someone else
installed (a Helm release, the Cilium CLI, a Talos inline manifest or an
applied `helm template`) Ichor's release. Before anything is written it
checks that the live minor equals the pinned chart's minor or is one lower,
that `ipam.mode` is `kubernetes` (a `cluster-pool` Cilium is refused), that
the tunnel protocol is carried over, and that Cilium and KubePrism are
healthy on every node. The user values come from the newest Helm release
record; where there is none (a Talos inline manifest or an applied
`helm template`) Ichor's defaults are used and the diff shows what is missing. Ichor then renders the
pinned chart with the effective values and compares every object with the
live one. A difference needs `--accept-cilium-diff`, and a change to an
Ichor-owned key on the running data plane needs `--accept-cilium-changes`
(IPAM and the tunnel protocol are never accepted). The `ichor-cni` Job runs
`cilium.Apply` with `--takeover`, so Helm installs with `TakeOwnership`. Once
the rollout is Ready the foreign release record Secrets are copied into
`ichor-cilium-prior-release` in `ichor-system`, read back byte for byte, and
only then deleted (`RemoveForeignReleaseRecord`; never `helm uninstall`). An
inline manifest entry is found by content, its name is recorded in
`status.cni.takenOverInlineManifest`, and it is removed from the machine
config only after `CNIHandover` is true. There is no rollback of an adoption.

### Git, override and recovery

A user may keep Ichor's CRs in their own Git and apply them with any tool. A write from the UI or CLI lands in the cluster and the user's tool
decides what to do with the drift. Putting runtime-written state in Git (`TalosOperation`s,
unapproved machines, the labels and annotations Ichor writes on Nodes and on
the storage system's node objects) makes that tool and Ichor fight, so keep
those out. Ichor writes no repository skeleton.

Override is the Kubernetes RBAC verb `override`. It guards the dangerous
operations: removing an Ichor finalizer from a deleting object, resetting a
node, restoring etcd and overriding the last-worker guard. It is checked with a
SubjectAccessReview made as the user.

Upgrades always run through the installer Job. Its CRD check lets only the
retired fields disappear and refuses every other removed field.

The CLI keeps a way out of the corners: `ichor upgrade ichor VERSION --local`
(no ichor-ui) and `ichor restore --recover-cluster`.

### Nodes

Discovery starts from a node in maintenance mode: either registered by IP or
found by scanning a subnet for the Talos API port. Scanning really is the only
option — Talos offers no beacon, mDNS or announcement to listen for — and it is
tractable because an unconfigured node brings up a DHCP client on every
physical link by itself, so it has an address before anyone looks for it.

The controller reads hardware identity, disks and NICs over an insecure
connection and creates a pending `TalosMachine`. The identity is the tuple of
**SMBIOS UUID, serial number and the permanent MAC addresses**, not the UUID
alone. Talos does not trust the UUID either: it ships two META keys to work
around it, and the doc comment on one of them says plainly that the UUID "may
repeat or be filled with zeros". Both need an admin-level connection to write,
which a maintenance-mode connection never has — it is read-only by
construction — so at the one moment Ichor keys a new resource off this
identity, it has no way to detect or repair a collision from the node's side.
The MACs are in the tuple because they are hardware-level, present when the
UUID is not, and survive a reset.

So **Ichor does not auto-register on a weak identity**. An empty UUID, or one
that already maps to a live `TalosMachine`, produces a pending entry that an
operator has to resolve — the same machine returning, or a different one — with
the address, MACs, serial and manufacturer shown to decide on. Writing a
disambiguating token to the node is not a fix: it needs an authenticated
connection Ichor only has after approval, and a reset wipes META anyway. The
durable record is Ichor's own `TalosMachine.spec.identity`, which is consistent
with the controller owning durable state rather than the node.

A machine that already runs a configured Talos cannot be discovered this way:
its API demands a client certificate. Discovery reports it as
`AlreadyConfigured` (an Event, and in the add-by-IP response) and creates
nothing. Joining it needs a reset of STATE and EPHEMERAL, which is
called a reclaim and is never automatic. Both paths run the same code
(`pkg/reclaim`) and the same refusals: a machine that is already an Ichor
machine or cluster, a control plane whose removal would break its cluster's
etcd quorum, and the last control plane of a reachable cluster (`--force`
overrides the last two with a second confirmation). The reset is graceful
unless the foreign cluster is dead (`--force-ungraceful`). It sends the
request, built by one shared function; user disks are not touched
but are listed, and encrypted ones are called out. Consent is the node's UUID,
typed. After the reboot the machine answers in maintenance mode with the same
UUID and the normal add-by-IP flow registers it as pending.

- CLI: `ichor machine reclaim ADDRESS --talosconfig FILE`. The CLI writes to
  the node itself, with the operator's own credential, which never leaves the
  operator's machine; like `init` and `adopt` it acts on a node that belongs
  to no Ichor cluster.
- UI: the Add node page and Machines, "Reset and add". Offered to users who
  hold the `reclaim` verb on `machinereclaims`. The UI API creates a `MachineReclaim` and a
  credential Secret as the signed-in user; the controller inspects the node,
  stops at `AwaitingConfirmation`, and resets only after the user has typed
  the UUID. See "Trust" for the credential.

Approval, in the UI or `ichor machine approve`, sets the role, labels, disk
roles and basic networking (which NIC, DHCP or static, VLAN or bond if
needed) through typed forms; no YAML required. "Which NIC" is expressed as a
`LinkAliasConfig` whose CEL selector matches the permanent MAC discovered
earlier, and the other documents refer to that alias — which is also the fix
for kernel interface names changing across boots. Bond members take names or
aliases rather than selectors, so a form that picks members by MAC emits one
alias per member.

The controller then renders the config, applies it insecurely, and follows the
node through install, reboot and join. "Follows" is load-bearing: the apply RPC
returns **before** the install starts, because Talos's own controllers react to
the stored config asynchronously. The approval flow therefore watches
`MachineStatus`'s stage move from maintenance through installing and rebooting
to running, and treats the RPC returning as nothing more than "the config was
stored".

Disks are identified by WWID, serial or by-id path, not `/dev/sdX` — but all
three can be absent. Talos fills WWID and serial from sysfs, with a udev
fallback only for `ata` and `scsi` transports, so NVMe is reliable while a plain
`virtio-blk` disk usually has neither, and without one udev creates no by-id
link either. So identification falls back through WWID, serial, by-id path
and finally bus path, and "nothing stable to key on" is a state the approval
form has to show rather than treat as an error.

Each disk gets a role: `system` (install target plus EPHEMERAL — the recommended
layout is one dedicated disk for Talos), `ephemeral` (EPHEMERAL on its own
disk), `storage` (prepared for a storage system via a preset), `userVolume`, or
`unused`. A role becomes `machine.install` for `system` and a
`VolumeConfig`/`UserVolumeConfig`/`RawVolumeConfig` document for the rest,
each selecting its disk with a CEL expression over the disk's reported fields.
All three documents are on the mirror allow-list, so a role is expressible
today; `TalosMachine.spec.disks` sets `ephemeral` and `userVolume` at approval
and `spec.network` the primary NIC, DHCP or static, and a VLAN;
`storage`, `unused` and bonds are not done. The `system`
disk is the exception to "a document per role": it is
`TalosMachine.spec.install.disk` (WWID, serial, by-id path, bus path, or
device path as a last resort), rendered as `UnattendedInstallConfig` from the
v1.14 contract and as `machine.install` before it.

Two things about editing a disk role after the fact, both verified against
Talos's volume manager. **A changed selector on a volume that already exists
does nothing, and nothing says so**: Talos locates EPHEMERAL by its partition
label before it ever consults the selector, and config validation accepts the
edit regardless. Ichor has to detect that itself and report "accepted, no
effect" rather than letting it look applied. Likewise a *lowered* `maxSize` is
silently ignored — partitions only ever grow. (A negative `maxSize` is
meaningful rather than invalid, and `PartitionSpec.ResolveMaxSize` in Talos
v1.14.3 says exactly what it means: the value is *space to leave free*, so the
volume gets `available - value`. A relative one is a percentage of available,
not of the disk -- `-25%` of 200 GiB available leaves 50 GiB free and takes
150. If the reservation exceeds what is available the call fails with
"partition size cannot be negative"; it does not clamp, so the volume simply
does not provision. That is the mechanism for sharing a disk: EPHEMERAL
reserves with a negative `maxSize`, and the user volume provisions in what is
left.)

The confirmation before wiping a disk matters most for the install target,
because `machine.install.wipe` defaults to **true** — Talos wipes the install
disk unless told not to. For every other disk Talos is the opposite: it refuses
to provision over any existing partition table or filesystem, so Ichor's
confirmation there explains why provisioning will not happen rather than
guarding against loss.
Single-disk nodes can use `systemDiskLayout: shared`, which caps EPHEMERAL and
leaves the rest for storage — Talos makes that work through partition waves,
with EPHEMERAL claiming its region before user volumes, and EPHEMERAL's own
minimum is 2 GiB; it is supported but discouraged and warned about,
especially on control planes where etcd would share I/O with storage. Disk
roles are fixed at install time because Talos ignores volume config changes
after provisioning; changing `system` or `ephemeral` later is a reset-and-
reinstall operation and is treated as such. Adding a `storage` role to an
`unused` disk doesn't need a reset. Disks with existing partitions or
filesystems require explicit confirmation.

Removal is a finalizer-driven sequence: storage evacuation hook (if
configured) → cordon and drain with a timeout → remove the etcd member if it's
a control plane (refused if quorum would break, if the count would drop below
`spec.controlPlane.count`, or if it's the last one) → graceful Talos reset →
delete the Node, in that order. The machine reappears as pending and can be
reused. A `node remove` action is different: it deletes the
TalosMachine but not the Kubernetes Node, because the machine is not wiped and
its kubelet would register the Node again; the plan ends with "power it off or
reset it, then delete the Node", and the controller does not do that last step. `forceRemove` skips drain and reset for dead hardware. Control planes
are replaced add-then-remove, never the other way round — a 3-node cluster
replacing a member goes 3→4→3, never 3→2, because four members tolerate one
failure while two tolerate none.

Four things about that sequence, each verified against Talos 1.14.3's source
and each easy to get fatally wrong:

- **The reset must name the partitions to wipe.** With its default mode and no
  partitions named, `Reset` wipes the *entire system disk*, boot and EFI
  included, which means a reinstall rather than a return to maintenance mode.
  Passing `STATE` and `EPHEMERAL` explicitly is what leaves the installed
  system in place and brings the node back up with no configuration — which is
  the behaviour the reuse story depends on.
- **Ichor's refusals are Ichor's alone.** Talos's member-removal call requires
  only that a leader exists right now; it does not ask whether the remaining
  members can still reach quorum. The one quorum guard Talos ships refuses at
  exactly two members and only on the upgrade path. So the three refusals above
  live in shared code, as principle 10 requires, and are the thing most worth
  testing before any of this points at real hardware.
- **A graceful reset already drains and already leaves etcd.** Ichor's own
  drain and member removal are therefore belts rather than the trousers for a
  live node: they matter for `forceRemove`, where the node cannot run its own
  sequence, and for refusing *before* Talos is asked to do anything.
- **Reset is fire-and-forget.** It returns an actor id immediately and the
  sequence runs asynchronously, so status has to record that a reset was
  requested *before* the controller can crash — otherwise a resumed reconcile
  fires a second concurrent reset at the same node. Every other step is
  naturally idempotent: re-draining a cordoned node is harmless, and removing
  an already-removed etcd member reports not-found, which counts as success.

The finalizer cannot defend against one thing: a person removing it by hand
mid-sequence. That deletes the resource immediately and orphans whatever state
existed — a cordoned node, possibly still an etcd voter, with nothing left to
resume it. Refusing finalizer removal except through override belongs with
the other dangerous-operation checks.

Draining uses `k8s.io/kubectl`'s own drain helper rather than a hand-written
eviction loop: it already encodes the DaemonSet, mirror-pod, local-storage and
PodDisruptionBudget rules, and it reports a timeout as an error, which is
exactly the "pause, don't escalate" signal the design wants.

Topology. Control-plane count is 1, 3 or 5. Two is rejected: with two
members etcd needs both to be up, so it is strictly less available than one.
Four has the same fault tolerance as three. Even counts are allowed only
transiently during a change and shown as "transitioning", during which the
cluster lock blocks other disruptive work. Workers can be zero; control-plane-
only clusters are a supported configuration, not a corner case.

`controlPlane.schedulable` is `true` or `false`, and required. It has no
`auto`: nothing on the object can resolve one, since a cluster is created
before any machine exists and the topology preset that carries the intent is a
flag on `ichor init`, not a field. `init` sets it from the preset,
and adding workers later never retaints the control planes underneath running
pods. Making control planes unschedulable with zero workers is rejected.
Presets: `single` (1+0, schedulable), `compact-ha` (3+0, schedulable),
`standard` (3+n, tainted, recommended), `large` (5+n, tainted).

Small clusters get specific treatment: single-node upgrades can't drain
anywhere, so the UI says there will be downtime and asks; a missing backup
configuration is a warning; three-node clusters without workers get a capacity
check before each drain.

### CNI

Cilium is the only CNI; `spec.cni.preset` is `cilium` and
immutable. `spec.cni.tunnel` (`ichor init --cni-tunnel`) picks `vxlan` or
`geneve` and is immutable once set. Talos's own CNI is removed (the
`KubeFlannelCNIConfig` document, or `cni.name: none` before v1.14). Pod and
service subnets are `spec.network.{podSubnets,serviceSubnets}`, fixed once set. A cluster stored with another preset is refused, not guessed at:
the controller sets `CNIUnsupported` and changes nothing.

A cluster adopted with no CNI is a managed `cilium` cluster: adopt
installs the pinned Cilium with `spec.cni.tunnel` from `--tunnel` (or the
default) and `spec.cni.kubeProxy` read from Talos (kube-proxy enabled gives
`enabled`, disabled gives `replaced`); `--cilium-values FILE` sets the initial
`spec.cni.cilium.values`. `ichor init` keeps its inline-manifest bootstrap.

The one exception is `spec.cni.preset: unmanaged`, which exists for
adoption only. `ichor init` has no flag for it and the webhook refuses it on
create unless the object carries the adoption annotation. The adopted cluster
keeps whatever CNI it runs (flannel, Calico, another, or none); Ichor does not
install, upgrade or configure it, and never migrates one. `ichor adopt` refuses
a non-Cilium CNI unless `--cni-unmanaged` is given, and refuses a cluster with
two CNIs even then. In this mode:

- `spec.cni.cilium` and `spec.cni.tunnel` are refused. `spec.cni.unmanaged.talosCNI`
  is `default` (render says nothing about the CNI) or `none` (render keeps
  `cluster.network.cni.name: none`). `spec.cni.kubeProxy` only records what
  the node has.
- The hand-over, the `ichor-cni` Job, `ichor cni` commands, `cni` operations
  and the kube-proxy switch are refused or skipped. A Kubernetes upgrade is
  never blocked on a hand-over. A CA rotation restarts no CNI workload.
- The preset is immutable. Moving between managed and unmanaged means a new
  cluster.
- `status.cni.detected` (`cilium`, `flannel`, `calico`, `other`, `none`) is
  read for display and refreshed on a slow requeue.

Network policy gate. Ichor's `CiliumNetworkPolicy` objects are
applied only when the detected CNI is Cilium and the CRD exists. Otherwise
`ichor-system` is not isolated by Ichor and the condition
`NetworkPoliciesApplied` says so. A hands-off Cilium also gets them.

How Cilium is installed, owned and edited is described under "Cilium" above. The Talos-recommended values are built in; Hubble certificates
come from a CronJob, not Helm, so a re-render does not change them.

`spec.cni.kubeProxy` is `enabled` or `replaced`. Replacing kube-proxy splits
the setting across two owners, Talos (`KubeProxyConfig`, Ichor) and Cilium's
`kubeProxyReplacement` (the Helm release, also Ichor), so the consistency check
reads Cilium's live config and reports mismatches as health failures.
Switching modes (`ichor cni kube-proxy`) is refused unless annotated (the
server sets the annotation for that one write), and then runs in the only safe
order: upgrade the Cilium release through the `ichor-cni` Job, verify on all
nodes, then turn the Talos side off.

Turning the old side off is a deletion Ichor has to perform, not a
consequence of the config change. Verified against Talos v1.14.3:
`ManifestController` renders the kube-proxy objects only while proxying is
enabled, but `ManifestApplyController` has no delete path, only a server-side
`Apply`. Flipping the setting stops Talos managing kube-proxy and leaves the
DaemonSet, ServiceAccount, ClusterRoleBinding and ConfigMap running, so an
operator would get two proxies. The same controller stamps every object with an
inventory annotation and refuses to apply over a *foreign* one.

### Storage

The work splits into four layers; Ichor owns the first and the last.

| | What | Owner |
|---|---|---|
| 1 | Talos-side preparation: volumes, extensions, kernel modules, sysctls | Ichor |
| 2 | Installing and upgrading the storage system | You |
| 3 | Volumes, replicas, disks, settings | The storage system's controller |
| 4 | Coordination around node operations: evacuate before drain, remove or upgrade | Ichor |

`TalosStorage` stays thin: which nodes, which disk preset, how disks are
handed to the storage system, and evacuation settings. The storage system's
own configuration is yours. Presets start with `longhorn-v1`
(verify the `iscsi-tools` and `util-linux-tools` extensions are in the node's
installer image, create the `UserVolumeConfig` Longhorn expects), followed by
`longhorn-v2`, `raw` (for Rook/Ceph-style consumers) and `filesystem`.
Missing extensions are reported by the informational `ExtensionsPresent`
condition on `TalosStorage`, read from the Nodes' `extensions.talos.dev/*`
labels; it never gates `Ready`. Adding one is an installer-image
change: a new schematic, which the Talos upgrade treats as an upgrade even when
the version does not move.

Disks are registered with the storage system through its own node label and
annotation mechanism, not by writing its CRs. A consistency check confirms the
storage system is configured to honour that.

The node-operation hook is shared by removal, upgrades and reboot-requiring
config changes: mark the node unschedulable in the storage system, request
evacuation, wait until no replica lives only there (with a timeout that pauses
the rollout rather than proceeding), then drain; afterwards, re-enable
scheduling. The hook is an interface so other integrations can use it.

For `longhorn-v1` the write side of that hook is settled, from
longhorn-manager v1.9.1's own type declarations rather than from the UI's
labels: `nodes.longhorn.io/v1beta2`'s `NodeSpec` carries `allowScheduling`
(bool) and `evictionRequested` (bool), and `DiskSpec` carries the same two
under `spec.disks[<name>]`, so a hook can evict a whole node or one disk.
The order is not a preference: Longhorn's own documentation states eviction
only takes effect on a node or disk whose scheduling is already disabled, so
`allowScheduling: false` has to land first.

The read side is now settled too, from longhorn-manager v1.9.1's own
controller source rather than from the UI's replica count: there is no
dedicated "eviction done" status field. `DiskStatus.ScheduledReplica` (the
status map that looked like the obvious candidate) is the scheduler's
capacity bookkeeping -- it is read for free space, not for residency, and
does not reliably go to zero when eviction finishes. The actual mechanism
is in `VolumeController.cleanupEvictionRequestedReplicas`: once a volume
has a healthy, non-evicting replica elsewhere, it deletes the `Replica`
object whose `spec.evictionRequested` is true. So "no replica lives only
there" is "no `replicas.longhorn.io` object remains with
`spec.nodeID` (whole-node eviction) or `spec.diskID` (one-disk eviction)
still naming the target" -- an absence of objects, not a counter reaching
zero. `EvacuationStatus` (`pkg/storage/evacuation.go`) takes that count
already read by the caller, plus elapsed time against
`TalosStorage.spec.evacuationTimeout`, and returns done, still-waiting, or
paused -- paused, never proceeding to drain, once the timeout is reached
with replicas still remaining.

`TalosStorage` reports health, so whatever installs the storage system can wait
until the Talos side is ready.

### Configuration reconcile

1. Load the bundle from the Secret and generate role-specific base configs
   with the pinned version contract.
2. Collect patches by layer and priority and apply them.
3. Validate, hash, and compare with the node's current config via COSI.
4. If different, apply. The mode (try, no-reboot, reboot, staged) is derived
   from the diff, because Talos will not derive it for us — its `AUTO` mode is
   a hardcoded alias for `NO_REBOOT` and never inspects the change.
   A dry run first is still worth it: it is side-effect free and returns
   validation warnings worth surfacing. Reboot-requiring changes take the cluster lock and go one
   node at a time through the operation hook and drain, control planes with
   etcd checks in between. They never start by themselves: the machine reports
   `RebootRequired` with the fields, and a `config` operation, started from
   `ichor config apply` or Apply in the Overview page's attention list after a plan, rolls them out.
   A third class, `APIServerRestart` (for example `spec.authentication`), needs
   no reboot but restarts the kube-apiserver; the machine controller never
   applies it to a control plane, and the same `config` operation does, one
   control plane at a time with no drain, waiting for that node's `/readyz`
   before the next.
   Talos's `Reboot` does not cordon or drain (unlike its `Upgrade` and `Shutdown`),
   so the rollout does both itself before the reboot and uncordons afterwards.
5. Watch COSI for state and drift. Drift handling is per cluster: warn only
   (default), or auto-correct non-reboot differences. A change in what the
   renderer emits (an Ichor upgrade, `render.Version`) is a desired-state change,
   not drift: an Ichor upgrade may apply configuration to nodes, and
   reboot-requiring changes wait for a config rollout.

### Upgrades

The release set. Each Ichor build embeds one supported set of versions, `pkg/release.Pinned`:
the Talos release and its version contract, the Kubernetes release, and the Cilium chart. Init and adopt
use it, the admission webhook refuses any version a TalosCluster or an operation would move to that is
not the set's, and the API reports each component of a cluster against it (`Cluster.versions`: up to
date, behind, or ahead). A cluster is never moved to a version the set does not name, and never down.
Adopt admits an older cluster as "behind"; a newer one is refused.

A cluster that is behind is rolled to the set by one user action, `ichor upgrade cluster` or the Versions
card's button, which starts one step at a time: Talos first (with the version contract, since Talos does not
upgrade Kubernetes and Kubernetes must be a release the running Talos supports), then the configuration
the new contract changes if a machine still has it pending, then Kubernetes, then Cilium (last, so the chart
meets the Kubernetes the set was built for). Each step is its own operation with its own plan and
confirmation, and the next one starts only when it has finished. Nothing starts on its own. An Ichor upgrade
does not move the cluster; the new build's set does, through this rollout.

A step that fails pauses its operation with a `failureReason` and is never
retried by itself; `ichor operation resume` (or Resume in the UI) on such an
operation re-runs the failed step through an annotation, not through `spec`.

All three upgrade kinds share the lock, the status-held state machine, pause-
on-failure (no automatic cluster-wide rollback), `paused`, `maxUnavailable`
for workers, a per-machine hold annotation, label-based ordering, and a
progress view. Outstanding hand-overs are flagged before a Kubernetes upgrade
because of the bootstrap-manifest re-apply.

Talos: change `spec.talosVersion` or the schematic (a machine is upgraded when its running version or
running schematic, read from its `ImageFactorySchematic` resource, differs from the target). Pre-checks cover etcd
health, node readiness, the supported upgrade path and the Kubernetes pairing.
Control planes go first, one at a time; workers follow within
`maxUnavailable`. Per node: hook → Upgrade API → reboot → verify version,
readiness and etcd membership. Cordon and drain are not Ichor's to do here:
Talos's own Upgrade sequence already cordons (a controller does it reactively
once the upgrade actor starts) and drains (via the kubelet's own kubeconfig,
the standard eviction API) before it stops services and reboots, and
uncordons the same way once the upgrade clears — confirmed in Talos 1.14.3's
source (`Sequencer.Upgrade`'s `"drain"` phase, `CordonAndDrainNode`, and
`NodeApplyController.ApplyCordoned`). Doing it again on Ichor's side would
only be redundant, not more correct, and would blur which side's cordon
"owns" the uncordon afterwards; the one thing still Ichor's to do beforehand
is the storage-evacuation hook, which Talos has no notion of. No
etcd-preserving flag handling is needed either: `UpgradeRequest.Preserve` is
explicitly deprecated in that same source ("only used for legacy upgrade
path, to be removed in Talos 1.18"), and the installer ignores it in upgrade
mode ("system disk wipe on upgrade is not supported anymore, option
ignored") — a plain upgrade reboots in place and never touches the etcd data
directory, for one member or many. A node that falls back to the previous
A/B slot counts as a failure. The version contract is never bumped by the
controller on its own: it moves only with the rollout's Talos step, to the
release set's contract.

Kubernetes: change `spec.kubernetesVersion` to the release set's version, one minor at a time within the skew policy. Warn about removed APIs in use, pre-pull images, then in three
stages, each finished on every machine before the next (no kubelet is ever
newer than an API server): switch the control-plane components node by node
through machine config; update kube-proxy and sync the bootstrap manifests
(the rollout check is skipped when kube-proxy is replaced); roll the
kubelets, control planes first. The intermediate configurations are renders
with the kubelet and kube-proxy pinned to the version the node runs, never
a write to `spec`. A minor upgrade of several control planes behind an
endpoint of type `node` warns that the endpoint is one apiserver. This is
`upgrade-k8s`'s order, run on Ichor's own machinery.

Ichor: the controller only launches the installer Job for the target version
and never upgrades itself. The Job runs with the RBAC of the release being
replaced, so the installer applies its own RBAC first; its rights are
cluster-scoped, plus a Role in Ichor's namespace. It then snapshots the current
manifests, updates CRDs (additive, never downgraded), the controller, then the
UI, applies the network policies when Cilium's CRD exists, runs post-checks, and
rolls back from the snapshot on failure. The CLI refuses writes when more than one minor
away from the server; the UI backend sends its version in a header so a stale
SPA prompts a reload.

Shutdown: a `TalosOperation` of kind `shutdown` stops the whole cluster for
planned downtime. Workers go first, in parallel and drained by Talos's
own `Shutdown` sequence; then every control plane, and the controller's own node
last, in one forced burst, so quorum is lost only in that last stage and a
self-managed controller needs to live only until the requests are sent. A
shutdown does not remove etcd members, so power-on loses no data. While the
operation has not completed, the cluster lock refuses every other disruptive
operation. Ichor cannot power machines on: the operation waits, and completes by
itself once every machine answers on a new boot, which lifts the refusal.

### Backup, recovery, certificates

`TalosEtcdBackup` takes snapshots on a schedule from a healthy non-leader
control plane, verifies them, encrypts them (snapshots contain Secrets) and
stores them in S3-compatible storage with retention. A snapshot can also be
taken outside the schedule (`ichor etcd backup now`, Back up now on the Backups
page), which sets an annotation the controller acts on. Failures and staleness
surface as metrics and alerts.

Two recovery paths. Restoring into a live cluster stops etcd on all control
planes and restores the snapshot; the UI and CLI both offer it, as a
`TalosOperation` that needs the override right on the cluster and the
cluster's name typed. Because the restore rolls the Kubernetes API back past the
operation itself, its progress is kept in a marker next to the backups, not in
status, and the operation is recreated from the marker. The backup
store must therefore live outside the cluster being restored (not a Service
such as `minio.minio.svc`, nor an address in the cluster's pod or service
range): the marker is written to the store after the API is down, so a store
inside the cluster deadlocks the restore. The plan refuses it, override does
not override that, and the `TalosEtcdBackup` carries a `StoreInCluster`
warning condition meanwhile. Full-loss
recovery is CLI-only: `ichor restore --recover-cluster` rebuilds control
planes from the local bundle and the snapshot, after which the controller
re-reconciles every node the way `adopt` does and waits for confirmation
before applying anything, since the restored CRs may not match the hardware.
The goal is that bundle + snapshot is sufficient, and the e2e suite proves it
regularly.

Ichor's own Talos client certificates are short-lived and renewed
automatically; an expired one would lock Ichor out with no way back. CA
rotation (Talos and Kubernetes; the etcd and aggregator CAs, the service-account key and the secretbox key are not rotated) follows the documented Talos procedure and
runs through the same machinery as upgrades, except that it waits, after every
node accepts the new CAs, until the operator has credentials that work with
both CAs; otherwise the first stage that changes what the API
servers present would lock the operator out. The new credentials are written by
the CLI or downloaded by an administrator in the UI. In-cluster
clients cannot hold both Kubernetes CAs (`kube-root-ca.crt` switches at
Issuing), so before dropping the old CA the controller waits for the new one in
every namespace and then a settle delay. Its own client trusts the new
Kubernetes CA ahead of the switch through a process-wide trust pool, and its
leader election outlives the API server restart. After the old CAs are dropped
and every Node has been Ready for a while, it restarts, one at a time, the
clients that keep the old CA (CoreDNS, kube-proxy, the CNI, ichor-ui); other
in-cluster clients that load the CA once may still need a restart.
Expiry dates are visible in
status, the UI and metrics.

### Monitoring and registry mirrors

Ichor does not install a monitoring stack. It can expose the control-plane
metrics that Talos binds locally by default (controller-manager, scheduler,
etcd) and restrict scraping to a CIDR with Talos's ingress firewall. The
controller and UI backend export Prometheus metrics, and ServiceMonitors,
alert rules and Grafana dashboards ship with the chart as an option. The
monitoring stack itself is a documented set of Helm values, or nothing — the
user's choice.

Registry mirrors are optional and off by default: they are pushed to every
node through the system layer, alongside an override for the installer image
source and a self-hosted Image Factory URL. They serve pull-through caches and
private registries, not air-gapped installs. The pre-checks can verify mirror
reachability before an upgrade.

## CLI and UI

The rule is parity. Both write CRs; both share the same Go packages for
rendering, planning (diff, impact, pre-checks, upgrade paths) and
dangerous-operation checks; both use the same Connect API, the CLI falling
back to local computation when it can't reach the backend. The CLI offers
`-o yaml|json`, `--follow`/`--watch`, and interactive confirmation with
`--yes`. The exceptions are `init`, `adopt` and full-loss recovery, which
predate the cluster.

The UI is a Vite + Preact + TypeScript SPA built with preactcn, embedded in
the Go backend. The backend talks to the API server, read-only to the Talos
API, and optionally to Prometheus for graphs; the
browser talks only to the backend. It survives drains: stateless, two replicas
where possible, graceful termination that asks clients to reconnect, and
streams that resume from a token (resourceVersion, last log timestamp,
progress sequence). `ichor ui` re-establishes its port-forward to another pod
when the current one goes away.

The UI is built for one cluster: there is no cluster list, and a top
bar shows the cluster's name, its Talos, Kubernetes and Ichor versions and a
health badge. Screens: an Overview, built from stock preactcn components, with
tiles for nodes (and the nodes waiting for approval), the running operation,
backups and versions, a needs-attention list, recent operations, and a
collapsible cluster details section (a config apply waiting for a reboot shows in the
needs-attention list with its own Apply action), with Add node and cluster
actions (rotating the CAs, shutting down) in the header; a Nodes list and an add-node
(approval) page; a node page with tabs for Summary, Storage & network, Talos,
Kubernetes & etcd, Configuration and Logs; Operations, with a detail page per
operation (per-machine state, pause, resume, and the CA-rotation credentials
hand-over); Backups, with each `TalosEtcdBackup`, its snapshots,
Back up now and restore; Configuration, for editing patches by layer with an
effective-config view annotated by origin and secrets masked, impact preview
before applying, and schema-driven forms; Cilium, with the effective values
read-only, a YAML editor for the user layer, validation and a diff preview, and
an Upgrade card when the embedded chart is newer; Users, only when Ichor's own users are on. A merged multi-node log
viewer is planned.

## Testing

e2e runs against throwaway clusters from `talosctl cluster create qemu` from
the first phase onwards. QEMU is not a preference but a requirement for most of
it: the Docker provisioner is capped at one control plane, has no disks, and
its own documentation excludes the `upgrade` and `reset` APIs, so install, disk
roles, reboot-requiring patches, removal, topology changes and upgrades can
only run under QEMU. Docker stays useful for the cheap half — config applies
that need no reboot, and drift. Node discovery needs a node that is up but
unconfigured, which `--presets maintenance` provides. Scenarios follow the
features:
bootstrap and join, patches with and without reboot, drift, removal and
replacement, topology changes including the even-count rejection, CNI
Cilium bootstrap, adoption and values edits, prune protection, storage preparation and evacuation, all three upgrade kinds
with rollback, backup, restore and full-loss recovery, certificate rotation.
Each scenario runs through both the CLI and the backend API. Chaos cases kill
the controller or reboot a node mid-rollout and check that status-driven
resumption works. A short set runs per PR, the full set nightly.

## Roadmap

1. CRDs and the kubebuilder project, Helm chart and Kustomize base, deletion
   protection, e2e scaffolding.
2. Typed config (the mirror-type generator first), rendering and apply, drift,
   the cluster lock.
3. Discovery, approval, disk roles, removal, topology. The deletion guard
   phase 1 ships only checks for the `allow-delete` annotation; the quorum and
   last-control-plane rules land here, and an explicit test has to show that
   the annotation alone does **not** let the last control plane go.
4. `init` and `adopt`, Cilium.
5. Read-only UI, approval UI, backup and restore, client certificate renewal.
6. Config editing UI, `TalosOperation`, Talos and Kubernetes upgrades,
   Ichor self-upgrade.
7. Storage integration (longhorn-v1, operation hooks), CA rotation.
8. Override.
9. Monitoring settings, metrics and dashboards, optional components, OIDC and impersonation, Ichor's own users and issuer.
10. UI localisation.
11. Later: more storage presets, IPv6, replica-based
    scaling.

## Alternatives considered

Raw YAML patches. Easier to start with, impossible to validate, preview or
explain in a UI, and they tie user files to a Talos version. Rejected in
favour of typed documents.

Ichor installing storage and monitoring itself. It would double the surface
area, duplicate what the storage controllers already do, and keep
us on the upgrade treadmill of every project we wrap. Ichor installs Cilium only.

Fewer CRDs. `TalosEtcdBackup` and `TalosStorage` could fold into
`TalosCluster`. They are kept separate so their RBAC and Git diffs stay
focused; merging later would need a migration, splitting later wouldn't.

Multiple repositories. The three deliverables share the CRD types and the
Connect definitions; separate repositories would mean a fourth one for those
and version skew to manage. One repository, three release artifacts.
