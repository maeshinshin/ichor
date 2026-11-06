---
title: "Ichor"
description: "Ichor manages Talos Linux clusters from Kubernetes: machines, configuration, upgrades, backups and Cilium are custom resources, worked with through a CLI, a web UI or Git."
sidebar_order: 1
---

# Ichor

Ichor manages [Talos Linux](https://www.talos.dev/) clusters from Kubernetes.
Nodes, machine configuration, upgrades and etcd backups are Kubernetes
resources, and you work with them through the `ichor` CLI, the ichor-ui web UI,
or by writing the resources yourself.

> Status: alpha (v0.1.0). Exercised end to end on Talos v1.14 clusters running as
> KubeVirt VMs, not yet on bare metal.

The commands in these docs are written as if their tools are on your `PATH`.
Run `nix develop` in a checkout to get them all, or install them yourself:
[Before you begin](guides/getting-started.md#before-you-begin) lists which and
at what versions.

## What Ichor manages

- **Clusters and machines.** A node that sits in Talos maintenance mode is
  discovered, approved by a person in the UI or the CLI, installed and joined to
  a cluster. Existing Talos clusters can be adopted, and machines that another
  cluster configured can be reclaimed.
  See [Clusters and machines](concepts/clusters-and-machines.md).
- **Machine configuration.** Patches are typed resources built on the Talos
  machinery types, with no raw YAML. The controller renders and applies them and
  reports drift.
- **Operations.** Talos, Kubernetes and Ichor upgrades, reboot-requiring
  configuration changes, node actions, shutdown, etcd restore, and rotation of
  CAs and keys are `TalosOperation` resources that run one node at a time with
  etcd quorum in mind. See [Operations](concepts/operations.md) and
  [CA and key rotation](concepts/ca-and-key-rotation.md).
- **etcd backups.** Scheduled or on-demand, encrypted, stored in S3-compatible
  storage; restore into a running cluster, or rebuild a cluster from the secrets
  bundle and a snapshot alone.
- **Cilium.** The only CNI Ichor supports, kept as a Helm release. It is the
  one thing Ichor installs on the cluster besides itself. See
  [CNI and Cilium](concepts/cni.md).

Ichor does not boot machines (get them to maintenance mode by ISO, PXE or
whatever you like), does not manage storage or monitoring, knows nothing about
GitOps tools, and does not target air-gapped installs.

## How the pieces fit

```text
operator ──┬── ichor CLI ─────────┐
           └── browser ─▶ ichor-ui │
                                   ▼
                     kube-apiserver (CRs, Secrets, Leases)
                                   ▲
                                   │ reconcile
                           ichor-controller ── Talos API ──▶ nodes
```

| Piece | Role |
|---|---|
| **Custom resources** | The API. `TalosCluster`, `TalosMachine`, `TalosConfigPatch`, `TalosOperation`, `TalosEtcdBackup`, `TalosStorage`, `MachineReclaim`, `TalosCredentialRequest`, `IchorInstallation` and `IchorUser`. See the [CRD reference](reference/crds/taloscluster.md). |
| **`ichor-controller`** | The only component that writes to Talos nodes. It reconciles the resources, keeps the state machine of long-running work in resource status, and never changes `spec`. |
| **`ichor` CLI** | Bootstraps the first node (`ichor init`), adopts existing clusters, and does everything the UI does. See the [CLI reference](reference/cli/ichor.md). |
| **`ichor-ui`** | A Preact web UI with its Go backend. It changes the world only by writing resources, reads node state from the Talos API read-only, and holds no CA key. |

Every write, from the UI, the CLI or your own tooling, is a change to a custom
resource, so validation, RBAC and audit are the same whichever way it arrived.
The design is in the [architecture document](design/architecture.md), and every
decision is recorded in the [ADRs](adr/README.md).

## What is in v0.1.0

- Discovery and approval of machines, with NIC, addressing and disk roles set at
  approval.
- Typed machine configuration with drift reporting, and reboot-requiring changes
  rolled out as one operation.
- Talos, Kubernetes, Cilium and Ichor upgrades, with the pause on failure and
  `resume` model described in [Operations](concepts/operations.md). A first
  install uses Talos v1.14.5, Kubernetes v1.36.6 and Cilium 1.20.4.
- CA rotation (Talos API, Kubernetes API, and optionally etcd and aggregator)
  and service-account and secretbox key rotation.
- etcd backup, restore and full-loss recovery.
- Optional Ichor-owned users with the roles viewer, operator and admin, and
  personal kubeconfigs with no secret in them.
- Admission that enforces the UI's checks on hand-written resources, an
  admin-only right to change Cilium's values, and an encrypted local secrets
  bundle. See [Security model](concepts/security.md).
- Typed, off-by-default hardening for the clusters Ichor creates (SecureBoot,
  kernel lockdown, kubelet serving certificates).

The complete list is in the [changelog](https://github.com/maeshinshin/ichor/blob/main/CHANGELOG.md).

## Where to go next

- [Getting started](guides/getting-started.md): from a build to a one-node
  cluster and the first sign-in.
- [Install with Helm](guides/install-with-helm.md) and
  [Adopt and reclaim](guides/adopt-and-reclaim.md): install Ichor on an existing
  cluster, or bring existing Talos machines in.
- [Users and login](guides/users-and-login.md),
  [Settings](guides/settings.md) and [Hardening](guides/hardening.md).
- [Rotating the service-account and secretbox keys](guides/rotate-keys.md).
- Concepts: [Clusters and machines](concepts/clusters-and-machines.md),
  [Operations](concepts/operations.md), [CNI and Cilium](concepts/cni.md),
  [Security model](concepts/security.md),
  [CA and key rotation](concepts/ca-and-key-rotation.md).
- [Reference](reference/index.md): every `ichor` command and every field of
  every custom resource.
