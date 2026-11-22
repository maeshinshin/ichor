---
title: "Reference"
description: "The ichor command-line reference and the field reference of every Ichor custom resource, generated from the code."
sidebar_order: 60
---

# Reference

![How the reference is laid out. The command tree and the API types in the code are turned into pages by make docs-reference: reference/cli holds one page per command with every flag, reference/crds one page per custom resource with every field. The pages are never edited by hand.](../images/reference-layout.light.drawio.svg)

*The reference is generated from the code.*

Run `make docs-reference` after changing a command, a flag or an API type; a
test fails when the committed pages are stale.

## CLI

Commands are verb first (`ichor get nodes`, `ichor reboot node NAME`), grouped as
in `ichor --help`. Start at [ichor](cli/ichor.md).

| Group | Commands |
|---|---|
| Get started | [`init`](cli/ichor_init.md), [`adopt`](cli/ichor_adopt.md), [`ui`](cli/ichor_ui.md) |
| Inspect | [`get`](cli/ichor_get.md), [`describe`](cli/ichor_describe.md), [`diff`](cli/ichor_diff.md), [`preview`](cli/ichor_preview.md) |
| Change | [`apply`](cli/ichor_apply.md), [`create`](cli/ichor_create.md), [`delete`](cli/ichor_delete.md), [`edit`](cli/ichor_edit.md), [`set`](cli/ichor_set.md), [`rollout`](cli/ichor_rollout.md), [`discovery`](cli/ichor_discovery.md) |
| Node and cluster lifecycle | [`approve`](cli/ichor_approve.md), [`reject`](cli/ichor_reject.md), [`reclaim`](cli/ichor_reclaim.md), [`reboot`](cli/ichor_reboot.md), [`shutdown`](cli/ichor_shutdown.md), [`restart`](cli/ichor_restart.md) |
| Maintenance | [`upgrade`](cli/ichor_upgrade.md), [`bootstrap`](cli/ichor_bootstrap.md), [`rotate`](cli/ichor_rotate.md), [`backup`](cli/ichor_backup.md), [`snapshot`](cli/ichor_snapshot.md), [`defrag`](cli/ichor_defrag.md), [`forfeit-leadership`](cli/ichor_forfeit-leadership.md), [`restore`](cli/ichor_restore.md), [`pause`](cli/ichor_pause.md), [`resume`](cli/ichor_resume.md), [`export`](cli/ichor_export.md) |
| Troubleshooting | [`logs`](cli/ichor_logs.md), [`pcap`](cli/ichor_pcap.md), [`support-bundle`](cli/ichor_support-bundle.md) |
| Access | [`auth`](cli/ichor_auth.md), [`user`](cli/ichor_user.md), [`secrets`](cli/ichor_secrets.md) |
| Other | [`version`](cli/ichor_version.md) |

## Custom resources

API group `ichor.maesh.dev`, version `v1alpha1`. Each page lists every field
with its type, whether it is required and its description. Deprecated fields are
unused and kept so the schema never loses a field an installed build had.

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

How they fit together: [concepts](../concepts/clusters-and-machines.md) and the
[architecture document](../design/architecture.md).
