# Ichor architecture

Status: draft, pre-implementation. This document is the source of truth for the
project. Decisions it leaves open are recorded in `../adr/` as they are made.

## Summary

Ichor manages Talos Linux clusters through Kubernetes custom resources. A
controller renders machine configuration from typed resources and applies it
to nodes, walks upgrades through the cluster, backs up etcd, and handles the
lifecycle of nodes from maintenance mode to removal. A CLI and a web UI expose
the same operations. Git can be the source of truth for all of it through
Argo CD.

Ichor deliberately stops at the Talos layer. It bootstraps a CNI and Argo CD
once so that a new cluster can start syncing from Git, then steps back. CNI
operations, storage systems and monitoring are Argo CD's business; Ichor only
prepares the Talos side for them and coordinates with them around node
operations.

## Motivation

Running Talos with `talosctl` works well for one person and a handful of
nodes. It gets awkward when machine configs live in a directory of YAML files,
when several people need to see what's applied where, and when upgrades have
to be sequenced by hand. Putting the configuration into the Kubernetes API
gives you RBAC, audit, webhooks, GitOps, and a place to hang a UI, without
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
- Bootstrap of a CNI and Argo CD for a brand-new cluster, with a clean
  hand-over to Argo CD.
- CLI and UI parity: anything you can do in one, you can do in the other.
- Everything manageable from Git, including Ichor itself.

### Non-goals

- Getting machines to maintenance mode. Boot the ISO however you like.
- Installing or operating CNI, storage or monitoring beyond the initial
  bootstrap.
- Replacing the storage system's own controller. Volumes, replicas and disk
  state belong to Longhorn (or whatever you run).
- Multi-cluster management. The design keeps a hub mode possible, but it is
  not a first-release feature.
- IPv6, PXE provisioning, Flux PR mode. Later, maybe.

## Overview

```
operator ──┬── ichor CLI ─────────┐           Git (source of truth)
           └── browser ─▶ ichor-ui │           ├─ clusters/<name>/  Ichor CRs
                                   │           ├─ apps/cni, apps/argocd, apps/ichor
                                   ▼           └─ apps/...          ▲ PR    │ sync
                     ┌─────────────────────────────────────────────┴────────▼──┐
                     │              kube-apiserver (CRs, Secrets, Leases)         │
                     └──────▲──────────────────▲──────────────────────▲──────────┘
                  reconcile │            apply │               run Job │
                 ┌──────────┴───────┐ ┌────────┴───────┐ ┌─────────────┴─────┐
                 │ ichor-controller │ │ Argo CD        │ │ ichor-installer   │
                 └──────────┬───────┘ └────────────────┘ └───────────────────┘
                            │ Talos API
                            ▼
      node in maintenance mode ──approve──▶ Talos node (control plane / worker)
```

Three deliverables come out of one repository:

- `ichor`, the CLI. Bootstraps the first node, adopts existing clusters, and
  offers every operation the UI has. It is also the recovery path that does not
  depend on Argo CD or on a running controller.
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

Every write, from the UI, the CLI or Argo CD, is a change to a custom
resource. The controller is the only thing that turns those into Talos API
calls. This keeps validation, RBAC, audit and GitOps identical regardless of
how a change arrived. Reads come from two places: CR status (via the API
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

Only the controller holds an `os:admin`-equivalent Talos identity. The UI
backend holds a read-only one. The browser holds nothing: no kube token, no
Talos credentials. In the first release the UI is reached through
`ichor ui` (a port-forward); OIDC login with Kubernetes impersonation comes
later so that RBAC and audit apply per user.

Approval uses Talos's insecure apply, which does not authenticate the node.
A separate provisioning network is assumed; the docs say so.

### Deployment

The default is self-managed: Ichor runs inside the cluster it manages. The
controller and UI run with two replicas, a PodDisruptionBudget and anti-affinity,
except on clusters with a single schedulable node, where the installer drops to
one replica and no PDB (otherwise a drain never finishes). A hub mode, where
Ichor manages clusters from outside, is left open by the API but not built
first.

## Principles

1. Machine config is never stored in a CR. CRs hold the ingredients
   (versions, patches, per-node values); the controller renders on every
   reconcile.
2. Secrets are referenced, never embedded.
3. Status holds hashes and state, never rendered config.
4. The Talos version contract is pinned in the CR, so a library bump cannot
   silently change what gets applied.
5. The controller never writes `spec`. Argo CD should never see Ichor fighting
   it.
6. Disruptive operations (upgrades, reboots, CA rotation, topology changes)
   take a per-cluster Lease. One at a time.
7. Progress lives in status and resumes idempotently.
8. CLI and UI are equivalent. The only CLI-only operations are the ones that
   happen before a cluster exists.
9. There is always a recovery path that needs neither Argo CD nor a working
   controller.
10. Dangerous-operation checks live in shared code and are enforced by the UI
    backend and by webhooks, never only in the frontend.

## API

Group `ichor.maesh.dev`, version `v1alpha1`.

| Kind | Scope | Purpose | In Git |
|---|---|---|---|
| `TalosCluster` | namespaced | Cluster-wide settings: endpoint, versions, topology, CNI bootstrap, GitOps mode, monitoring, integrations, optional components | yes |
| `TalosMachine` | namespaced | One node: identity, role, labels, disk roles, per-node values | yes, once approved (always, in strict mode) |
| `TalosConfigPatch` | namespaced | A typed config patch with a layer, priority and target | yes |
| `TalosStorage` | namespaced | Thin integration with a storage system: node selector, disk preset, evacuation settings | yes |
| `TalosEtcdBackup` | namespaced | Backup schedule, destination, retention, encryption | yes |
| `TalosOperation` | namespaced | A one-off imperative action (reboot, reset, snapshot, rollback), Job-style | no |
| `IchorInstallation` | cluster, singleton | Ichor's own version and management mode | yes |

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
where the field catalogue lives. The evidence and the shape of the generator
are in `../adr/0003-typed-config-mirror-types.md`.

A handful of fields need the generator's type-keyed substitution and drop
rules to be overridden per field instead, because the type alone can't say
what to do with them:

- Eleven fields are `meta.Unstructured` — Talos's own escape hatch for a
  pass-through to another project's schema (kubelet's configuration, an
  audit policy, a resource list, a seccomp profile). A type-keyed rule can
  only drop all eleven or none; a field-level override table drops six
  (new dependency, controller-gen errors, or no single schema to mirror) and
  mirrors five as their upstream type instead, with no new dependency.
- Nine more fields are plain strings or certificate pairs, not
  `meta.Unstructured`, but carry cluster secret material — join tokens, CA
  key pairs, encryption keys. These are dropped unconditionally, by a
  separate table, for a different reason than the eleven above: not because
  they can't be represented, but because a `TalosConfigPatch` must not be
  able to overwrite the real values the secrets bundle generates, however
  narrowly the patch claims to be scoped.

Both are ADR 0003's follow-up.

Two questions that table left open are now settled.

`machine.kubelet.credentialProviderConfig` gets an override and takes the
`k8s.io/kubelet` dependency it needs, moving it out of the dropped six. It
generates cleanly and measures about 6.5 KB served; under a typed-only API
an absent field is an inexpressible one, so a commonly wanted absent field
is a product gap rather than a saved dependency. One official `k8s.io/*`
staging module, versioned in lockstep with Kubernetes, is the same class of
dependency the already-mirrored five come from. The counts above describe
what the generator does today, before that lands.
`machine.kubelet.extraConfig` still gets nothing: what blocks it was never
the dependency but a `float32`, which controller-gen only emits under
`allowDangerousTypes` — and that flag applies to the whole API package, so
one field would relax the type safety of every CRD in it.

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
immutable after creation; kube-proxy mode changes only with an explicit
annotation; disk roles that require reinstall are immutable after install;
`TalosCluster` and `TalosMachine` can only be deleted with
`ichor.maesh.dev/allow-delete: "true"` (so an Argo CD prune can't cascade into
node resets); direct writes to GitOps-managed CRs are rejected except through
break-glass; Talos/Kubernetes version pairs and upgrade paths are checked.

## Design

### Bootstrap

```
ichor init --preset standard --cni cilium [--argocd --repo URL --path clusters/NAME] [--gitops-dir DIR]
```

1. Generate the secrets bundle locally; nag the user to back it up.
2. Settle the shape of the cluster: topology preset, CNI preset, endpoint
   (a VIP is recommended even for one node, because changing the endpoint
   later means re-issuing certificates), disk roles for the first node.
3. Apply the rendered config to the first control plane. Its
   `cluster.inlineManifests` carry only the CNI (unless flannel) -- the one
   component Kubernetes itself cannot start without, since nodes never go
   Ready and nothing can be scheduled before it exists. Ichor itself is not
   inlined: once step 4 below gets a working kubeconfig, Ichor applies its
   own install manifest (CRDs, RBAC, the controller Deployment) directly
   through the API server, the same build's binary carries it (no fetch, no
   version to get wrong), and a failure there is an ordinary, visible error
   instead of something buried in Talos's boot log. Argo CD itself is never
   installed by `init` either -- deciding which third-party release to fetch
   and trust during bootstrap is not this command's call to make; with
   `--argocd` it only prepares the root Application (pointing at the repo)
   and the repository credentials, as a Secret from the CLI, never into Git,
   ready for whenever Argo CD is installed by some other means.
4. Bootstrap etcd, fetch the kubeconfig, store the bundle as a Secret.
5. Create the CRs. With `--gitops-dir`, also write them as YAML and generate
   the repository skeleton described below.
6. Hand over: once Argo CD reports the CNI Synced and Healthy, remove it from
   `inlineManifests`.

Adding later nodes goes through the normal approval flow; `init` is the
special case of approving the first control plane. `ichor adopt` brings an
existing cluster under management by diffing each node's current config against
a fresh render and storing the difference as initial patches, so ownership
transfers with zero drift.

### GitOps and Argo CD

What goes in Git: `TalosCluster`, `TalosConfigPatch`, `TalosStorage`,
`TalosEtcdBackup`, `IchorInstallation`, approved `TalosMachine`s, and the
Applications for CNI, Argo CD, Ichor and anything else. What doesn't: Secrets,
`TalosOperation`s, unapproved machines, and the labels and annotations Ichor
writes at runtime on Nodes and on the storage system's node objects. Putting
those in Git makes Argo CD and Ichor fight; Ichor warns if it sees them tracked.

Detecting "tracked" is two different questions, not one with two keys.
Argo CD marks the resource: `application.resourceTrackingMethod` in
`argocd-cm` picks `annotation` (`argocd.argoproj.io/tracking-id`, whose value
is `<app>:<group>/<kind>:<namespace>/<name>`), `label`
(`app.kubernetes.io/instance`), or `annotation+label`, and
`application.instanceLabelKey` can replace the label key outright. A resource
marked under one method is not detected under another, and the default has
moved between releases — so Ichor reads that ConfigMap rather than looking for
a marker it guessed. Flux marks nothing on the resource: ownership lives in
the owning Kustomization's `.status.inventory`, so the same question becomes
"list the Kustomizations and search their inventories", and the field-manager
name to look for in managed fields is `flux-client-side-apply`. Two code
paths, with different cost: a lookup on the object for Argo CD, a scan over
owners for Flux.

Two modes on `TalosCluster.spec.gitops.mode`:

- `relaxed` (default): approving a node writes the `TalosMachine` directly;
  you can export it to Git afterwards. Changes to GitOps-managed CRs go
  through PRs.
- `strict`: approving a node *is* a PR that adds the `TalosMachine`. The node
  stays pending until it merges and syncs. Nothing exists in the cluster that
  isn't in Git.

PR mode. If a CR is tracked by Argo CD and a repository connection exists, the
UI and CLI don't write it; they find the file via the Application's source,
edit the right node of the YAML while preserving comments and formatting, and
open a PR whose description includes the impact preview (affected nodes,
reboot required or not) and who asked. The UI then follows the PR through
merge, sync and apply. Plain YAML and Kustomize sources are supported; Helm
and Jsonnet sources get a diff to apply by hand. Without a repository
connection the CR is read-only with an export button. GitHub, GitLab and
Gitea/Forgejo are supported; the backend only opens PRs, never merges.

Break-glass exists for incidents: a permission-gated direct write that also
opens the matching PR, so Git catches up. Ichor never touches an Application's
sync policy.

Hand-over. The CNI is the only component that follows the inlined-then-removed
path: injected through `inlineManifests` at bootstrap (it has to be, since
nothing can schedule before it exists), then adopted by Argo CD, then removed
from `inlineManifests` once Argo CD reports it Synced and Healthy. It declares
its main workloads and emits an Argo CD Application template that matches
what was injected (same release name, namespace and values) so adoption
produces no diff. Removing the entry matters: Talos re-applies bootstrap
manifests during Kubernetes upgrades, and a stale entry would roll the CNI
back. Hand-over can be automatic, manual (`ichor gitops handoff cni`), or
disabled when Argo CD isn't used.

Ichor itself and Argo CD do not go through this path (`### Bootstrap` above):
Ichor is applied once, directly, through the API server, never through
`inlineManifests`, so it has no stale-entry risk to guard against on a
Kubernetes upgrade; Argo CD is never installed by Ichor at all. "One
mechanism with a component registry, not three special cases" turned out to
describe only one component once the other two were actually built -- see
ADR 0019.

Why removing the entry is what matters, verified in Talos v1.14.3:
`ManifestApplyController` only ever server-side applies and has no delete
path, and it refuses to apply over an object carrying a *foreign* inventory
annotation rather than fighting for it -- but Argo CD's tracking marker is
not that annotation, so adoption alone does not stop the re-apply. Handing
over without removing the entry looks finished and is not.

Repository skeleton (`ichor gitops export`):

```
bootstrap/root-app.yaml      app of apps
apps/argocd/                 Argo CD managing itself
apps/cni/                    from the CNI preset
apps/ichor/                  Helm chart reference + values
apps/storage/, apps/monitoring/   optional components, only if selected
clusters/<name>/             Ichor CRs
```

Sync waves: Argo CD → CNI → Ichor (including CRDs) → Ichor CRs → storage after
`TalosStorage` is healthy → everything else. Ichor CR Applications disable
prune and enable server-side apply. Argo CD health checks (Lua) for Ichor's
kinds ship with the chart, so rollouts are visible in Argo CD's UI.

Optional components (`TalosCluster.spec.components[]`) are things Ichor does
not install but can put in the skeleton with Talos-tuned values —
kube-prometheus-stack and Longhorn to start. Selecting one suggests the
matching Talos-side settings; a consistency check flags when the two sides
disagree. Users who don't use Argo CD get the same values as documentation.

Ichor under Argo CD. `IchorInstallation.spec.managementMode` is `ichor`
(upgrades run through the installer Job) or `external` (Argo CD owns Ichor's
manifests; upgrades are PRs that bump the chart version). Ichor suggests
`external` when it finds itself tracked by Argo CD, to avoid two writers. The
chart avoids anything that produces a diff on every sync, such as generated
certificates.

There is a cycle here: Argo CD manages the CNI and Ichor, and Ichor manages
the nodes Argo CD runs on. The CLI keeps a way out of every corner:
`ichor cni recover`, `ichor self-upgrade --local`,
`ichor restore --recover-cluster`.

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
link either. That is not an exotic case: it is what Ichor's own KubeVirt test
nodes look like. So identification falls back through WWID, serial, by-id path
and finally bus path, and "nothing stable to key on" is a state the approval
form has to show rather than treat as an error.

Each disk gets a role: `system` (install target plus EPHEMERAL — the recommended
layout is one dedicated disk for Talos), `ephemeral` (EPHEMERAL on its own
disk), `storage` (prepared for a storage system via a preset), `userVolume`, or
`unused`. A role becomes `machine.install` for `system` and a
`VolumeConfig`/`UserVolumeConfig`/`RawVolumeConfig` document for the rest,
each selecting its disk with a CEL expression over the disk's reported fields.
All three documents are on the mirror allow-list, so a role is expressible
today; what is still missing is the controller that sets one.

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
reused. `forceRemove` skips drain and reset for dead hardware. Control planes
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
resume it. Refusing finalizer removal except through break-glass belongs with
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
flag on `ichor init`, not a field (ADR 0008). `init` sets it from the preset,
and adding workers later never retaints the control planes underneath running
pods. Making control planes unschedulable with zero workers is rejected.
Presets: `single` (1+0, schedulable), `compact-ha` (3+0, schedulable),
`standard` (3+n, tainted, recommended), `large` (5+n, tainted).

Small clusters get specific treatment: single-node upgrades can't drain
anywhere, so the UI says there will be downtime and asks; a missing backup
configuration is a warning; three-node clusters without workers get a capacity
check before each drain.

### CNI

`TalosCluster.spec.cni.preset` selects one of `flannel` (Talos default, Ichor
does nothing), `cilium` (Helm, with the Talos-recommended values built in),
`custom` (your chart or manifests, you declare the Talos-side settings), and
later `calico`. A preset is a Go interface: Talos-side patches, manifest
rendering for the bootstrap, an Argo CD Application template, whether it can
replace kube-proxy, a consistency check against the live configuration, and
the workloads that signal a successful hand-over. New CNIs are new
implementations of that interface and nothing else.

The preset cannot be changed after the cluster exists; swapping a CNI takes
the network down and is out of scope.

`spec.cni.kubeProxy` is `enabled` or `replaced`. Replacing kube-proxy splits
the setting across two owners — Talos (`cluster.proxy.disabled`, Ichor) and
the CNI (Argo CD) — so the consistency check reads the CNI's live config and
reports mismatches as health failures. Switching modes is refused unless
annotated, and then runs in the only safe order: bring the new side up on all
nodes, verify, then turn the old side off.

Turning the old side off is a deletion Ichor has to perform, not a
consequence of the config change. Verified against Talos v1.14.3:
`ManifestController` renders the kube-proxy objects only under
`if cfg.ProxyEnabled`, and on `cluster.proxy.disabled` it destroys its own
`k8s.Manifest` COSI resources — but `ManifestApplyController` contains no
delete path at all, only a server-side `Apply`. So flipping the setting on a
running cluster stops Talos *managing* kube-proxy and leaves the DaemonSet,
ServiceAccount, ClusterRoleBinding and ConfigMap running. An operator who
sets `kubeProxy: replaced` and expects kube-proxy to go away gets two
proxies instead.

The same controller stamps every object it applies with an inventory
annotation and refuses to apply over an object carrying a *foreign* one,
reporting an error rather than fighting for it. That is the boundary
hand-over has to respect: Argo CD's own tracking label is not that
annotation, so adoption does not stop Talos re-applying — which is exactly
why removing the `inlineManifests` entry, not just handing over, is the step
that matters.

The generated Application template carries the known Argo CD pitfalls: server-
side apply for the large CRD sets, Cilium's Hubble certificates generated by a
CronJob rather than Helm (otherwise every sync shows a diff), and for Calico,
exclusions for operator-created resources.

### Storage

The work splits into four layers; Ichor owns the first and the last.

| | What | Owner |
|---|---|---|
| 1 | Talos-side preparation: volumes, extensions, kernel modules, sysctls | Ichor |
| 2 | Installing and upgrading the storage system | Argo CD |
| 3 | Volumes, replicas, disks, settings | The storage system's controller |
| 4 | Coordination around node operations: evacuate before drain, remove or upgrade | Ichor |

`TalosStorage` stays thin: which nodes, which disk preset, how disks are
handed to the storage system, and evacuation settings. The storage system's
own configuration lives in Argo CD's values. Presets start with `longhorn-v1`
(verify the `iscsi-tools` and `util-linux-tools` extensions are in the node's
installer image, create the `UserVolumeConfig` Longhorn expects), followed by
`longhorn-v2`, `raw` (for Rook/Ceph-style consumers) and `filesystem`.
Missing extensions block the preset with a clear message, since adding one is
an installer-image change and goes through the same rolling procedure as a
Talos upgrade.

Disks are registered with the storage system through its own node label and
annotation mechanism, not by writing its CRs. A consistency check confirms the
storage system is configured to honour that.

The node-operation hook is shared by removal, upgrades and reboot-requiring
config changes: mark the node unschedulable in the storage system, request
evacuation, wait until no replica lives only there (with a timeout that pauses
the rollout rather than proceeding), then drain; afterwards, re-enable
scheduling. The hook is an interface so other integrations can use it.

`TalosStorage` reports health so Argo CD's sync waves can hold the storage
Application until the Talos side is ready.

### Configuration reconcile

1. Load the bundle from the Secret and generate role-specific base configs
   with the pinned version contract.
2. Collect patches by layer and priority and apply them.
3. Validate, hash, and compare with the node's current config via COSI.
4. If different, apply. The mode (try, no-reboot, reboot, staged) is derived
   from the diff, because Talos will not derive it for us — its `AUTO` mode is
   a hardcoded alias for `NO_REBOOT` and never inspects the change (ADR 0009).
   A dry run first is still worth it: it is side-effect free and returns
   validation warnings worth surfacing. Reboot-requiring changes take the cluster lock and go one
   node at a time through the operation hook and drain, control planes with
   etcd checks in between.
5. Watch COSI for state and drift. Drift handling is per cluster: warn only
   (default), or auto-correct non-reboot differences.

### Upgrades

All three upgrade kinds share the lock, the status-held state machine, pause-
on-failure (no automatic cluster-wide rollback), `paused`, `maxUnavailable`
for workers, a per-machine hold annotation, label-based ordering, and a
progress view. Outstanding hand-overs are flagged before a Kubernetes upgrade
because of the bootstrap-manifest re-apply.

Talos: change `spec.talosVersion` or the schematic. Pre-checks cover etcd
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
A/B slot counts as a failure. The version contract is never bumped
automatically.

Kubernetes: change `spec.kubernetesVersion`, one minor at a time within the
skew policy. Warn about removed APIs in use, pre-pull images, switch the
control-plane components node by node through machine config, update
kube-proxy (skipped when replaced), roll the kubelets, sync the bootstrap
manifests. Reusing talosctl's `upgrade-k8s` logic is the first thing to
evaluate.

Ichor: in `ichor` mode, the controller only launches the installer Job for the
target version and never upgrades itself. The installer snapshots the current
manifests, updates CRDs (additive, never downgraded), the controller, then the
UI, runs post-checks, and rolls back from the snapshot on failure. In
`external` mode it's a PR. The CLI refuses writes when more than one minor
away from the server; the UI backend sends its version in a header so a stale
SPA prompts a reload.

### Backup, recovery, certificates

`TalosEtcdBackup` takes snapshots on a schedule from a healthy non-leader
control plane, verifies them, encrypts them (snapshots contain Secrets) and
stores them in S3-compatible storage with retention. Failures and staleness
surface as metrics and alerts.

Two recovery paths. Restoring into a live cluster stops etcd on all control
planes and restores the snapshot; the UI and CLI both offer it. Full-loss
recovery is CLI-only: `ichor restore --recover-cluster` rebuilds control
planes from the local bundle and the snapshot, after which the controller
re-reconciles every node the way `adopt` does and waits for confirmation
before applying anything, since the restored CRs may not match the hardware.
The goal is that bundle + snapshot is sufficient, and the e2e suite proves it
regularly.

Ichor's own Talos client certificates are short-lived and renewed
automatically; an expired one would lock Ichor out with no way back. CA
rotation (Talos and Kubernetes) follows the documented Talos procedure and
runs through the same machinery as upgrades. Expiry dates are visible in
status, the UI and metrics.

### Monitoring and air gap

Ichor does not install a monitoring stack. It can expose the control-plane
metrics that Talos binds locally by default (controller-manager, scheduler,
etcd) and restrict scraping to a CIDR with Talos's ingress firewall. The
controller and UI backend export Prometheus metrics, and ServiceMonitors,
alert rules and Grafana dashboards ship with the chart as an option. The
monitoring stack itself is an optional component in the Git skeleton, a
documented set of Helm values, or nothing — the user's choice.

Air-gap support is entirely optional and off by default: registry mirrors
pushed to every node through the system layer, an override for the installer
image source, a self-hosted Image Factory URL, and `ichor images list` to
print everything that needs mirroring for a given version set. The pre-checks
can verify mirror reachability before an upgrade.

What `ichor images list` has to print for a self-hosted Image Factory is
settled, from the Factory's own `docs/sources.md` and `docs/air-gapped.md`.
It pulls from `ghcr.io` unless given `-image-registry`, and needs
`siderolabs/imager`, `siderolabs/installer-base` (`siderolabs/installer`
below Talos 1.10.0), `siderolabs/extensions`, `siderolabs/overlays` and
`siderolabs/talosctl-all` (Talos 1.11.0 and up). Mirroring is
`hack/copy-artifacts.sh`, which copies signatures as well as images, and the
Factory is pointed at the result with `artifacts.core.registry` plus an
optional `artifacts.core.namespace` for registries that prefix paths.

The part that is easy to get wrong, and so the part `ichor images list` exists
for: `extensions` and `overlays` are *catalogs*. Mirroring them does not
mirror what they name — every extension and overlay image they reference has
to be mirrored too, and the `namespace` prefix applies to those as well. A
mirror that holds the five images above and nothing else looks complete and
fails at the first schematic that asks for an extension.

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
API, to Git hosting for PRs, and optionally to Prometheus for graphs; the
browser talks only to the backend. It survives drains: stateless, two replicas
where possible, graceful termination that asks clients to reconnect, and
streams that resume from a token (resourceVersion, last log timestamp,
progress sequence). `ichor ui` re-establishes its port-forward to another pod
when the current one goes away.

Screens: cluster and machine lists with pending approvals, rollout progress
and hand-over state; a node page with hardware, disks, network, live resource
usage, Kubernetes state, effective config and logs; config editing by layer
with an effective-config view annotated by origin and secrets masked, impact
preview before applying, and schema-driven forms; a merged multi-node log
viewer; a GitOps page showing each CR's Application, sync state and pending
PRs, and where Git, the cluster and the nodes disagree; deep links into
Grafana, Hubble, Longhorn and Argo CD when their URLs are registered.

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
bootstrap and hand-over per preset, Argo CD bootstrap, PR and strict modes,
prune protection, storage preparation and evacuation, all three upgrade kinds
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
4. `init` and `adopt`, CNI presets (flannel, cilium, custom), Argo CD
   bootstrap, hand-over, repository skeleton.
5. Read-only UI, approval UI, backup and restore, client certificate renewal.
6. Config editing UI, `TalosOperation`, Talos and Kubernetes upgrades,
   Ichor self-upgrade.
7. Storage integration (longhorn-v1, operation hooks), CA rotation.
8. PR mode, strict mode, break-glass, Argo CD health checks, GitOps page.
9. Monitoring settings, metrics and dashboards, optional components, external
   links, OIDC and impersonation, `ichor images list`.
10. Air-gap verification, UI localisation.
11. Later: calico, more storage presets, Flux PR mode, IPv6, hub mode, replica-
    based scaling.

## Open questions

- Exact Longhorn node label/annotation and evacuation field names per version.
- The Cilium and Calico values that avoid perpetual OutOfSync under Argo CD.

## Alternatives considered

Raw YAML patches. Easier to start with, impossible to validate, preview or
explain in a UI, and they tie user files to a Talos version. Rejected in
favour of typed documents.

Ichor installing CNI, storage and monitoring itself. It would make the first
run smoother and double the surface area, duplicate what Argo CD and the
storage controllers already do, and keep us on the upgrade treadmill of
every project we wrap. Bootstrap-and-hand-over gets the smooth first run
without the rest.

Fewer CRDs. `TalosEtcdBackup` and `TalosStorage` could fold into
`TalosCluster`. They are kept separate so their RBAC and Git diffs stay
focused; merging later would need a migration, splitting later wouldn't.

Multiple repositories. The three deliverables share the CRD types and the
Connect definitions; separate repositories would mean a fourth one for those
and version skew to manage. One repository, three release artifacts.
