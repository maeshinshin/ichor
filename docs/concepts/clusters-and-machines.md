---
title: "Clusters and machines"
description: "How a node in Talos maintenance mode becomes a member of a TalosCluster: discovery, approval and provisioning, and how existing machines are adopted or reclaimed."
sidebar_group: "What Ichor manages"
sidebar_order: 10
---

# Clusters and machines

Two resources describe what Ichor manages.

- A [`TalosCluster`](../reference/crds/taloscluster.md) is one cluster: the Talos and Kubernetes versions it runs, its
  control-plane endpoint, its CNI settings and its drift policy. The secrets
  bundle (the CA keys) is referenced from it and lives in a Secret, never in the
  resource.
- A [`TalosMachine`](../reference/crds/talosmachine.md) is one machine: its identity, its role, its
  disks and network, and what the controller has seen on it.

Machine configuration is a layer of typed
[`TalosConfigPatch`](../reference/crds/talosconfigpatch.md) documents built on
the Talos machinery types. There is no raw YAML escape hatch. The controller
renders the effective configuration for each machine, applies it, and reports
drift between what it rendered and what the node runs.

For the full design see [the architecture document](../design/architecture.md),
section "Nodes".

![The life of a machine. A node in maintenance mode is discovered and becomes a pending TalosMachine; a person approves it, or rejects it (a rejected machine is not discovered again as pending, but a person can still approve it). The controller then configures it, and it becomes Ready. A Ready machine whose disk is wiped is provisioned again by itself; removing it drains the node and leaves etcd, and if the node is also reset (when asked) it reappears as pending. A node that another cluster configured is reset by a reclaim, typed with its UUID, and then is discovered again.](../images/machine-lifecycle.light.drawio.svg)

*A machine moves forward by discovery, a person's approval and the controller's work; the person's steps are in blue.*

## Discovery

Ichor does not boot machines. You bring a machine to Talos maintenance mode
(from an ISO, over PXE, however you like) and Ichor finds it. Discovery is
either a node you register by address, or a subnet scan for the Talos API port
(`ichor discovery add`, or the Add node page in the UI). Talos offers
nothing a controller could listen for, so scanning is the only way.

For each node found, the controller reads its hardware over the
unauthenticated maintenance connection, which is read-only, and creates a
`TalosMachine` in the `pending` state. The identity is a tuple of the SMBIOS
UUID, the serial number and the permanent MAC addresses, because the UUID alone
can be empty or repeated.

Ichor does not register a machine on a weak identity. An empty UUID, or one that
already belongs to a live machine, produces a pending entry that a person has
to resolve, with the address, MACs and serial shown.

A node that already runs a configured Talos cannot be discovered this way,
because its API wants a client certificate. Discovery reports it as
`AlreadyConfigured` and creates nothing; see
[Adopt a running cluster](../guides/adopt-and-reclaim.md) and
[Reclaim a machine](../guides/reclaim-a-machine.md).

## Approval

A pending machine does nothing until someone approves it, in the UI or with
`ichor approve node`. Approval is where you decide:

- the role, `controlplane` or `worker`, and labels;
- the install disk and the roles of the other disks (`system`, `ephemeral`,
  `storage`, `userVolume`, `unused`);
- the primary network interface, with DHCP or a static address and an optional
  VLAN.

The decision is stored on the machine as `spec.approvalState` (`pending`,
`approved` or `rejected`) together with who made it and when. Admission also
checks the cluster: it refuses an approval that would leave more approved
control planes than `spec.controlPlane.count` plus one.

Approval is gated by its own verb, `approve` on `talosmachines/approval`, which
the operator and admin roles hold. See [Security model](security.md).

## Provisioning

Once approved, the controller renders the configuration and sends it to the
node through the Talos maintenance API, then follows the node through install,
reboot and join. `status.stage` moves from `maintenance` through `installing`
and `rebooting` to `running`; the apply call returning only means the
configuration was stored, so the controller watches the stage rather than
trusting the call.

The maintenance API does not authenticate the node at the other end. Ichor
therefore pins what it can: the fingerprint of the maintenance certificate
seen at discovery is recorded, and the controller refuses to send the first
configuration, which carries the CA keys, to a node that presents another one.
You accept a changed fingerprint you have verified with the annotation
`ichor.maesh.dev/trust-maintenance-fingerprint`. Keep machines on a separate
provisioning network. See [Security model](security.md).

An approved machine that converged and later answers only in maintenance mode
with its approved identity, because its disk was wiped or replaced, is
provisioned again by the controller, with a Warning Event. The annotation
`ichor.maesh.dev/hold-apply` stops that.

## Adopting and reclaiming

- **Adopt** brings a running Talos cluster under Ichor's management with zero
  drift (`ichor adopt`), and `ichor adopt --add-machine ADDRESS` adds a machine
  that already carries the managed cluster's CAs.
- **Reclaim** resets a machine that another cluster configured, so it can be
  discovered and approved. It is never automatic, it needs the node's UUID typed
  as consent, and it refuses a machine whose removal would break its own
  cluster's etcd quorum.
  In the cluster it is a [`MachineReclaim`](../reference/crds/machinereclaim.md).

Next: [Operations](operations.md) for upgrades and node actions, and
[`ichor get nodes`](../reference/cli/ichor_get_nodes.md) and the commands it links to.
