---
title: "Reference"
description: "The ichor command-line reference and the field reference of every Ichor custom resource, generated from the code."
sidebar_order: 60
---

# Reference

These pages are generated from the code and are never edited by hand. Run
`make docs-reference` after changing a command, a flag or an API type; a test
fails when the committed pages are stale.

## CLI

The `ichor` command and its subcommands, with every flag. Start at
[ichor](cli/ichor.md). The top-level groups:

| Command | For |
|---|---|
| [`ichor init`](cli/ichor_init.md) | Create the first control plane and install Ichor |
| [`ichor adopt`](cli/ichor_adopt.md) | Bring an existing Talos cluster, or one more machine of it, under Ichor |
| [`ichor cluster`](cli/ichor_cluster.md) | Inspect clusters, discovery, CA and key rotation, credentials |
| [`ichor machine`](cli/ichor_machine.md) | List, approve, reject and reclaim machines |
| [`ichor node`](cli/ichor_node.md) | Node actions: reboot, shutdown, reset, remove, logs, support bundle |
| [`ichor operation`](cli/ichor_operation.md) | List, follow, pause, resume and delete operations |
| [`ichor upgrade`](cli/ichor_upgrade.md) | Upgrade Talos, Kubernetes or Ichor |
| [`ichor config`](cli/ichor_config.md) and [`ichor patch`](cli/ichor_patch.md) | Effective configuration and configuration patches |
| [`ichor cni`](cli/ichor_cni.md) | Cilium values, upgrades and the kube-proxy mode |
| [`ichor etcd`](cli/ichor_etcd.md) and [`ichor restore`](cli/ichor_restore.md) | Backups, snapshots and recovery |
| [`ichor user`](cli/ichor_user.md), [`ichor auth`](cli/ichor_auth.md) and [`ichor settings`](cli/ichor_settings.md) | Users, sign-in and Ichor's runtime settings |
| [`ichor ui`](cli/ichor_ui.md), [`ichor version`](cli/ichor_version.md), [`ichor secrets`](cli/ichor_secrets.md) | Open the UI, show versions, encrypt the local bundle |

## Custom resources

All resources are in the API group `ichor.maesh.dev`, version `v1alpha1`. Each
page lists every field with its type, whether it is required and its
description; fields marked deprecated are kept so that the schema never loses
a field an installed build had, and are unused.

| Resource | What it is |
|---|---|
| [TalosCluster](crds/taloscluster.md) | One cluster: versions, endpoint, CNI, drift policy |
| [TalosMachine](crds/talosmachine.md) | One machine: identity, role, approval, status |
| [TalosConfigPatch](crds/talosconfigpatch.md) | A typed layer of machine configuration |
| [TalosOperation](crds/talosoperation.md) | An upgrade, rotation or node action in progress |
| [TalosEtcdBackup](crds/talosetcdbackup.md) | Scheduled etcd snapshots and their storage |
| [TalosStorage](crds/talosstorage.md) | Disk preparation for a storage system |
| [MachineReclaim](crds/machinereclaim.md) | Resetting a machine that another cluster configured |
| [TalosCredentialRequest](crds/taloscredentialrequest.md) | A short-lived request for one certificate |
| [IchorInstallation](crds/ichorinstallation.md) | Ichor's own version and runtime settings |
| [IchorUser](crds/ichoruser.md) | An Ichor-owned user |

For how these fit together see the [concepts](../concepts/clusters-and-machines.md)
and the [architecture document](../design/architecture.md).
