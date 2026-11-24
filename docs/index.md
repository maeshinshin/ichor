---
title: "Ichor"
description: "Ichor manages Talos Linux clusters from Kubernetes: machines, configuration, upgrades, backups and Cilium are custom resources, worked with through a CLI, a web UI or Git."
sidebar_order: 1
---

# Ichor

Ichor manages [Talos Linux](https://www.talos.dev/) clusters from Kubernetes.
Nodes, machine configuration, upgrades and etcd backups are Kubernetes
resources, worked with through the `ichor` CLI, the ichor-ui web UI or by
writing the resources yourself. Status: alpha.

The CLI is published with each release; [Getting
started](guides/getting-started/install-the-cli.md) installs it, and [Build from
source](development/build-from-source.md) builds it.

## How the pieces fit

![Architecture of Ichor. The browser, the ichor CLI and kubectl reach the Kubernetes API of the cluster that Ichor manages. Inside that cluster, in the ichor-system namespace, run ichor-ui, the ichor-controller with its admission webhooks, and the Jobs (ichor-installer installs and upgrades Ichor, ichor-cni runs Helm for Cilium); they read and write Ichor's custom resources through the API. The API server holds Ichor's custom resources, Secrets and Leases. In steady state only the controller configures the Talos machines, over the Talos API; a new machine waits in maintenance mode until you approve it, and then becomes a Talos node, a control plane or a worker.](images/architecture.light.drawio.svg)

*Ichor runs inside the cluster it manages, and every change goes through the Kubernetes API as a custom resource.*

| Piece | Role |
|---|---|
| Custom resources | The API: `TalosCluster`, `TalosMachine`, `TalosConfigPatch`, `TalosOperation`, `TalosEtcdBackup`, `TalosStorage`, `MachineReclaim`, `TalosCredentialRequest`, `IchorInstallation`, `IchorUser` ([reference](reference/crds/taloscluster.md)) |
| `ichor-controller` | The only writer to Talos nodes, apart from three CLI commands (`ichor init`, `ichor restore cluster`, `ichor reclaim node --talosconfig`). Keeps long-running state in resource status and never changes `spec` |
| `ichor` CLI | Bootstraps the first node, adopts clusters, does everything the UI does ([reference](reference/cli/ichor.md)) |
| `ichor-ui` | Preact UI with a Go backend. Writes only resources, reads node state read-only, holds no CA key |

Every write is a change to a custom resource, so validation, RBAC and audit are
the same from the UI, the CLI or your own tooling. Design: [architecture
document](design/architecture.md).

## What Ichor manages

| Area | What | Read |
|---|---|---|
| Clusters and machines | Discovery of Talos maintenance-mode nodes, approval, install and join; adopting a running cluster; reclaiming a machine another cluster configured | [Clusters and machines](concepts/clusters-and-machines.md) |
| Machine configuration | Typed patches on the Talos machinery types, no raw YAML; drift reporting | |
| Operations | Talos, Kubernetes, Cilium and Ichor upgrades, reboot-requiring changes, node actions, shutdown, etcd restore, pause on failure and `resume`; one node at a time with etcd quorum in mind | [Operations](concepts/operations.md) |
| Rotation | CAs, service-account and secretbox keys | [CA and key rotation](concepts/ca-and-key-rotation.md) |
| etcd | Scheduled or on-demand encrypted backups to S3-compatible storage; restore, or rebuild from the secrets bundle and a snapshot | [Back up etcd](guides/etcd-backup.md) |
| Cilium | The only CNI, kept as a Helm release; the one thing Ichor installs besides itself | [CNI and Cilium](concepts/cni.md) |
| Access | Optional Ichor users (viewer, operator, admin) with secret-free kubeconfigs; admission that enforces the UI's checks; admin-only Cilium values; encrypted local secrets bundle | [Security model](concepts/security.md) |
| Hardening | Typed, off-by-default: SecureBoot, kernel lockdown, kubelet serving certificates | [Hardening](guides/hardening.md) |

A first install gets the Talos, Kubernetes and Cilium versions pinned in the
release (`ichor version`). Not in scope: booting machines (use an ISO or PXE),
storage, monitoring, GitOps tools and air-gapped installs. Changes:
[changelog](https://github.com/maeshinshin/ichor/blob/main/CHANGELOG.md).

## Where to go next

- [Getting started](guides/getting-started.md): the CLI to a one-node cluster and
  the first sign-in.
- [Install with Helm](guides/install-with-helm.md), [Adopt a running
  cluster](guides/adopt-a-cluster.md), [Reclaim a
  machine](guides/reclaim-a-machine.md).
- [Users and login](guides/users-and-login.md), [Settings](guides/settings.md),
  [Hardening](guides/hardening.md).
- [Upgrade Ichor with the installer](guides/upgrade-ichor.md).
- [Rotating keys](guides/rotate-keys.md), [Replace a control plane](guides/replace-a-control-plane.md).
- [Node actions](guides/node-actions.md), [Shutdown and power](guides/shutdown-and-power.md), [The web UI](guides/the-web-ui.md).
- [Troubleshooting](guides/troubleshooting.md), [Metrics and monitoring access](guides/metrics-and-monitoring-access.md).
- Storage: [Longhorn](guides/longhorn.md) or [Rook/Ceph](guides/rook-ceph.md).
- Concepts: [Certificate names](concepts/certificate-names.md), [Sign-in and
  sessions](concepts/sign-in-and-sessions.md).
- [Reference](reference/index.md): every command and every custom resource field.
