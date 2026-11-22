---
title: "Clusters and machines"
description: "How a node in Talos maintenance mode becomes a member of a TalosCluster: discovery, approval and provisioning, and how existing machines are adopted or reclaimed."
sidebar_group: "What Ichor manages"
sidebar_order: 10
---

# Clusters and machines

| Resource | Is |
|---|---|
| [`TalosCluster`](../reference/crds/taloscluster.md) | one cluster: Talos and Kubernetes versions, control-plane endpoint, CNI settings, drift policy. The secrets bundle (CA keys) is referenced and lives in a Secret |
| [`TalosMachine`](../reference/crds/talosmachine.md) | one machine: identity, role, disks, network, and what the controller has seen on it |
| [`TalosConfigPatch`](../reference/crds/talosconfigpatch.md) | a typed layer of machine configuration (Talos machinery types, no raw YAML) |

The controller renders each machine's effective configuration, applies it and
reports drift. Design: [architecture](../design/architecture.md), "Nodes".

| To | Use |
|---|---|
| See the rendered configuration, each value tagged with the layer that set it (secrets masked) | `ichor describe config` |
| List, show or delete patches | `ichor get patches`, `ichor describe patch NAME`, `ichor delete patch NAME` |
| See a patch's impact (reboot, install-time-only fields) before writing it | `ichor preview -f FILE` |
| Set the Kubernetes Node name, or labels on the Node | `TalosMachine` `spec.hostname` (an RFC 1123 name; default: derived by Talos), `spec.nodeLabels` (on the Node, not on the resource) |

![The life of a machine. A node in maintenance mode is discovered and becomes a pending TalosMachine; a person approves it, or rejects it (a rejected machine is not discovered again as pending, but a person can still approve it). The controller then configures it, and it becomes Ready. A Ready machine whose disk is wiped is provisioned again by itself; removing it drains the node, leaves etcd, always wipes it (STATE and EPHEMERAL, or more when asked) and deletes its Kubernetes Node, and a node that reboots after the wipe reappears as pending. A node that another cluster configured is reset by a reclaim, typed with its UUID, and then is discovered again.](../images/machine-lifecycle.light.drawio.svg)

*A machine moves forward by discovery, a person's approval and the controller's work; the person's steps are in blue.*

## Discovery

Ichor does not boot machines. Bring one to Talos maintenance mode (ISO, PXE,
anything) and register it by address or scan a subnet for the Talos API port
(`ichor discovery add`, or Add node in the UI). For each node found the
controller reads its hardware over the read-only, unauthenticated maintenance
connection and creates a `pending` `TalosMachine`.

- Identity is the SMBIOS UUID, serial number and permanent MAC addresses
  together, because the UUID alone can be empty or repeated. An empty or
  duplicated UUID gives a pending entry a person must resolve.
- A node that already runs a configured Talos wants a client certificate. It is
  reported as `AlreadyConfigured` and nothing is created; see
  [Adopt a running cluster](../guides/adopt-a-cluster.md) and
  [Reclaim a machine](../guides/reclaim-a-machine.md).

## Approval

A pending machine does nothing until approved, in the UI or with
`ichor approve node`. You decide:

- role (`controlplane` or `worker`) and labels;
- the install disk and the other disks' roles (`system`, `ephemeral`, `storage`,
  `userVolume`, `unused`);
- the primary interface: DHCP or static address, optional VLAN.

Stored as `spec.approvalState` (`pending`, `approved`, `rejected`) with who and
when. Admission refuses an approval that would leave more approved control
planes than `spec.controlPlane.count` plus one. The verb is `approve` on
`talosmachines/approval`, held by operator and admin ([Security
model](security.md)).

## Provisioning

The controller renders the configuration, sends it through the maintenance API
and follows the node through `status.stage`: `maintenance`, `installing`,
`rebooting`, `running`. A returning apply call only means the configuration was
stored, so it watches the stage.

- The maintenance API does not authenticate the node. The certificate
  fingerprint seen at discovery is pinned, and the first configuration (which
  carries the CA keys) is refused to a node presenting another. Accept a
  fingerprint you verified with the annotation
  `ichor.maesh.dev/trust-maintenance-fingerprint`. Keep machines on a separate
  provisioning network.
- A converged machine that later answers only in maintenance mode with its
  approved identity (disk wiped or replaced) is provisioned again with a Warning
  Event. `ichor.maesh.dev/hold-apply` stops that.

## Adopting and reclaiming

- **Adopt**: `ichor adopt` brings a running Talos cluster under management with
  zero drift; `ichor adopt --add-machine ADDRESS` adds a machine that already
  carries the managed cluster's CAs.
- **Reclaim**: resets a machine another cluster configured so it can be
  discovered and approved. Never automatic; needs the node's UUID typed as
  consent; refused if it would break its own cluster's etcd quorum. In the
  cluster it is a [`MachineReclaim`](../reference/crds/machinereclaim.md).

Next: [Operations](operations.md).
