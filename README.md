# Ichor

Ichor manages [Talos Linux](https://www.talos.dev/) clusters from Kubernetes.
Nodes, machine configuration, upgrades and etcd backups are Kubernetes
resources; you work with them through a CLI, a web UI, or Git.

Ichor takes care of Talos and installs one thing besides it: Cilium, the only
CNI, which Ichor keeps as a Helm release. Storage and monitoring are yours to
install.

> Status: alpha. Exercised end to end on Talos v1.14 clusters running as KubeVirt
> VMs; not yet on bare metal. The design is in
> [docs/design/architecture.md](docs/design/architecture.md); every decision
> is in [docs/adr](docs/adr/README.md).

## What it does

- Turns a node sitting in Talos maintenance mode into a cluster member:
  discover, approve in the UI or CLI, install, join. At approval you can also
  set the node's primary NIC (DHCP or static, optional VLAN) and the disk
  roles for EPHEMERAL and user volumes
  ([ADR 0092](docs/adr/0092-approval-sets-the-primary-nic-addressing-and-extra-disk-roles-as-typed-spec-fields.md)).
- Keeps machine configuration declarative. Patches are typed Kubernetes
  resources built on the Talos machinery types; the controller renders and
  applies them, and reports drift.
- Rolls out changes that need a reboot as one operation, a node at a time:
  control planes first with an etcd check before each, then workers. API
  server restarts follow the same rule
  ([ADR 0080](docs/adr/0080-reboot-requiring-config-changes-roll-out-through-a-config-operation.md)).
- Rolls Talos and Kubernetes upgrades through the cluster one node at a time,
  with etcd quorum in mind. A failed step pauses the operation; `resume`
  runs that step again.
- Runs node actions as operations: reboot, shutdown, restart a service, etcd
  defrag, forfeit etcd leadership, reset, and remove
  ([ADR 0085](docs/adr/0085-node-actions-are-controller-run-operations-of-kind-node.md)).
- Rotates the cluster CAs in verified passes: the Talos and Kubernetes API CAs
  by default, and with `--ca etcd`, `--ca kubernetes-aggregator` or `--all` the
  etcd CA and the aggregator CA too
  ([ADR 0120](docs/adr/0120-etcd-ca-and-aggregator-ca-rotation.md)). The
  operation waits until you
  have the new kubeconfig and talosconfig, from the CLI or the UI, before it
  stops trusting the old CAs, then restarts the in-cluster clients that keep
  the old one
  ([ADR 0063](docs/adr/0063-certificate-expiry-visibility-and-ca-rotation-decisions.md),
  [0091](docs/adr/0091-a-ca-rotation-waits-for-the-operators-new-credentials-before-anything-they-hold-stops-working.md),
  [0093](docs/adr/0093-the-ui-hands-over-a-ca-rotations-new-credentials-to-administrators-who-hold-the-download-verb.md)).
- Rotates the service-account signing key and the secretbox (encryption at rest)
  key in a dual-key period: both keys are accepted before either is dropped,
  every Secret is rewritten under the new key, and backups sealed before stay
  readable
  ([ADR 0121](docs/adr/0121-the-service-account-key-and-the-secretbox-key-rotate-in-a-dual-key-period-and-the-backup-key-is-versioned.md),
  [guide](docs/guides/rotate-keys.md)).
- Backs up etcd on a schedule or on demand (`ichor etcd backup now`, Back up now
  in the UI), restores a snapshot into a running cluster, and can rebuild a
  cluster from the secrets bundle and a snapshot alone.
- Can keep its own users with the roles viewer, operator and admin. People
  sign in to the UI and each gets a personal kubeconfig with no secret in it.
  Off by default. See [Users and login](docs/guides/users-and-login.md).
- Gates Secret references: changing a field that names a Secret needs the
  `secret-references` right in Kubernetes RBAC, which `ichor-admin` holds
  ([ADR 0096](docs/adr/0096-a-secret-reference-is-changed-only-with-update-on-secret-references.md)).
- Upgrades itself through the `IchorInstallation` resource
  ([ADR 0059](docs/adr/0059-ichor-installation-and-self-upgrade.md)).
- Bootstraps Cilium, the only CNI (pinned 1.20.4 chart embedded in the CLI;
  `--cni-tunnel vxlan|geneve` picks its tunnel) and keeps it as a Helm release.
  An admin edits its values in the UI (Cilium page) or with `ichor cni values`;
  upgrade it with `ichor cni upgrade`, and switch kube-proxy with
  `ichor cni kube-proxy`
  ([ADR 0098](docs/adr/0098-the-cilium-bootstrap-manifest-is-the-embedded-chart-rendered-with-helm.md),
  [0099](docs/adr/0099-argo-cd-and-kokumi-become-the-standard-stack.md),
  [0131](docs/adr/0131-changing-the-cilium-values-needs-an-admin-only-verb-and-the-secrets-namespaces-are-pinned.md)).
  `ichor init` waits for the hand-over of Cilium to Ichor.
- Locks down its own namespace with CiliumNetworkPolicies, including a
  name-based (FQDN) egress rule for ichor-ui's OIDC issuer; the controller's
  metrics port stays closed until you name a namespace
  ([ADR 0100](docs/adr/0100-ichor-ships-cilium-network-policies-for-its-own-namespace-applied-after-cilium-is-ready.md),
  [0126](docs/adr/0126-fqdn-network-policy-rules-and-a-metrics-namespace-restriction.md)).
- Offers typed, off-by-default hardening for the clusters it creates: SecureBoot
  (with the SecureBoot installer picked per node), kernel lockdown and kubelet
  serving certificates that the controller approves itself
  ([ADR 0123](docs/adr/0123-secureboot-uki-and-kernel-lockdown-as-typed-options.md),
  [0125](docs/adr/0125-kubelet-serving-certificates-are-requested-by-the-kubelets-and-approved-by-the-controller.md),
  [guide](docs/guides/hardening.md)).
- Hands operators a short-lived `os:reader` talosconfig for `talosctl`
  (`ichor cluster talosconfig`) without access to the secrets bundle
  ([ADR 0124](docs/adr/0124-an-os-reader-talosconfig-role.md)).
- Resolves the installer image of an Ichor upgrade to a digest and verifies its
  release signature before the installer Job runs
  ([ADR 0122](docs/adr/0122-the-installer-image-runs-by-digest-and-its-signature-is-verified-before-the-job.md)).
- Prepares disks for storage systems such as Longhorn, and waits for data to
  evacuate before draining or removing a node.

## What it doesn't do

- Run anything on the cluster beyond its own controller, UI and Cilium.
- Manage storage or monitoring, or know about GitOps tools ([ADR 0101](docs/adr/0101-argo-cd-and-kokumi-are-no-longer-part-of-ichor.md)).
- Boot machines. Get them to maintenance mode (ISO, PXE, whatever you like);
  Ichor takes it from there.
- Target air-gapped installs. Registry mirrors and private registries work
  ([ADR 0090](docs/adr/0090-air-gapped-installs-are-out-of-scope.md)).

## Components

| | |
|---|---|
| `ichor` | CLI. Bootstraps the first node, adopts existing clusters, and does everything the UI does. |
| `ichor-controller` | The only thing that talks to Talos nodes. Reconciles the CRDs. |
| `ichor-ui` | Web UI (Preact) and its backend. Changes the world only by writing custom resources; reads node state from Talos directly. |

## Quick start

From a checkout, inside `nix develop`, with a node booted from a Talos ISO
(maintenance mode) and a registry the nodes can reach:

```
make demo-images-build VERSION=v0.1.0
make demo-images-push VERSION=v0.1.0 REGISTRY=192.168.1.10:5000
bin/img/ichor/ichor init --help     # create the first control plane and install Ichor
bin/img/ichor/ichor machine approve --help
bin/img/ichor/ichor cluster get NAME
bin/img/ichor/ichor version         # CLI version and the installed Ichor version (--client skips the cluster)
```

[Getting started](docs/guides/getting-started.md) walks through all of it,
from the build to the first sign-in to the UI. `ichor <command> --help` lists
the rest. The CLI prints for people; add `-o yaml` or `-o json` in a script. `ichor init`
prints the passphrase of the local secrets bundle once and waits for Enter; a
script gives it `--bundle-passphrase-file`. Existing clusters are brought in with
`ichor adopt` (`ichor adopt --add-machine` adds a machine that already runs the
cluster's configuration).

## Security

Approving a node talks to Talos's maintenance-mode API, which does not
authenticate the node at the other end. Ichor assumes you keep a separate
provisioning network for that reason — it's not something Ichor itself
enforces. Only the controller holds an `os:admin`-equivalent Talos identity and writes
to nodes. The UI backend reads with `os:reader`; for the admin-gated downloads
(support bundle, etcd snapshot, packet capture, CA-rotation credentials) it uses the narrowest
role Talos accepts for that one call. The browser holds no credentials at all.
Users never receive an admin talosconfig, except an administrator during a CA
rotation; operators and administrators can download a one-hour `os:reader`
talosconfig.
The secrets bundle (Talos, Kubernetes and etcd CA keys) is the root of trust
and lives in a Secret, never in a custom resource. The copy `ichor init` keeps
on your machine is encrypted with a passphrase (an age file); keep the passphrase
apart from the file
([ADR 0127](docs/adr/0127-encrypting-the-local-secrets-bundle.md)). See the Trust section of
[docs/design/architecture.md](docs/design/architecture.md) for the full
picture.

## Development

Everything runs inside `nix develop`. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
