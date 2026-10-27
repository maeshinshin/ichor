# Ichor

Ichor manages [Talos Linux](https://www.talos.dev/) clusters from Kubernetes.
Nodes, machine configuration, upgrades and etcd backups are Kubernetes
resources; you work with them through a CLI, a web UI, or Git.

Ichor only takes care of Talos. Everything that runs on the cluster — the CNI,
storage, monitoring — is bootstrapped once and then handed over to Argo CD.

> Status: alpha. Exercised end to end on Talos v1.14 clusters running as KubeVirt
> VMs; not yet on bare metal. The design is in
> [docs/design/architecture.md](docs/design/architecture.md).

## What it does

- Turns a node sitting in Talos maintenance mode into a cluster member: discover,
  approve in the UI, install, join.
- Keeps machine configuration declarative. Patches are typed Kubernetes
  resources built on the Talos machinery types; the controller renders and
  applies them, and reports drift.
- Rolls Talos and Kubernetes upgrades through the cluster one node at a time,
  with etcd quorum in mind.
- Backs up etcd on a schedule, and can rebuild a cluster from the secrets
  bundle and a snapshot alone.
- Bootstraps a CNI (Cilium by default) and, optionally, Argo CD, then hands
  both over to Argo CD so that Git becomes the source of truth.
- Prepares disks for storage systems such as Longhorn, and waits for data to
  evacuate before draining or removing a node.

## What it doesn't do

- Run anything on the cluster beyond its own controller and UI.
- Manage the CNI, storage or monitoring after bootstrap. That's Argo CD's job.
- Boot machines. Get them to maintenance mode (ISO, PXE, whatever you like);
  Ichor takes it from there.

## Components

| | |
|---|---|
| `ichor` | CLI. Bootstraps the first node, adopts existing clusters, and does everything the UI does. |
| `ichor-controller` | The only thing that talks to Talos nodes. Reconciles the CRDs. |
| `ichor-ui` | Web UI (Preact) and its backend. Changes the world only by writing custom resources; reads node state from Talos directly. |

## Security

Approving a node talks to Talos's maintenance-mode API, which does not
authenticate the node at the other end. Ichor assumes you keep a separate
provisioning network for that reason — it's not something Ichor itself
enforces. Only the controller holds an `os:admin`-equivalent Talos identity and writes
to nodes. The UI backend reads with `os:reader`; for the admin-gated downloads
(etcd snapshot, packet capture) it uses the narrowest role Talos accepts for
that one call. The browser holds no credentials at all.
The secrets bundle (Talos, Kubernetes and etcd CA keys) is the root of trust
and lives in a Secret, never in a custom resource. See the Trust section of
[docs/design/architecture.md](docs/design/architecture.md) for the full
picture.

## Development

Everything runs inside `nix develop`. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
