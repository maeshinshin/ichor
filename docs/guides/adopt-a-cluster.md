# Adopt a running Talos cluster

This guide covers two things Ichor does with Talos machines it did not set up:
bringing a whole running cluster under management (`ichor adopt`), and adding
one machine that already carries the cluster's own certificates to a cluster
Ichor manages (`ichor adopt --add-machine`). To reset a machine that another
cluster configured so it can join an Ichor cluster, see
[Reclaim a machine](reclaim-a-machine.md).

## What you have in front of you

| Your machine or cluster | Command | Needs a reset? |
|---|---|---|
| A running cluster Ichor has never seen | `ichor adopt` (below) | No |
| One more machine that already runs a configuration signed by a cluster Ichor manages (it joined with your own `talosctl apply-config`) and that you want to keep as it is | `ichor adopt --cluster NAME --add-machine ADDRESS` | No |
| A machine removed from a cluster Ichor manages (its `TalosMachine` was deleted, the node was not wiped, and it still carries the cluster's configuration) | `ichor reclaim node ADDRESS --cluster NAME` (no talosconfig) or the UI "Reset and add" with "It was removed from this cluster" | Yes: `STATE` and `EPHEMERAL` |
| A machine another Talos cluster (or a config of its own) configured, and you hold its talosconfig | `ichor reclaim node` or the UI "Reset and add" | Yes: `STATE` and `EPHEMERAL` |
| A machine that was never configured, or whose `STATE` was wiped (booted from the ISO, in maintenance mode), including one removed with `ichor delete node` (the removal sends the wipe; with the default power off, power it on first; with `--reboot` it returns by itself, and the removal waits up to five minutes for it to answer in maintenance mode) | The normal Add node flow | No |
| A configured machine and no credential for it | None through the API: reinstall from the ISO, or wipe it with a kernel argument | Yes |

How to tell them apart: `ichor adopt --add-machine` reads the machine's own configuration and
refuses one that does not name this cluster's ID and Talos API CA, and says to use
[`ichor reclaim node`](reclaim-a-machine.md) instead. For a machine that does carry this cluster's CAs it keeps the
configuration; if that configuration cannot be represented (typically a machine removed without a wipe, whose hostname and network link Ichor rendered from its deleted `TalosMachine`), it
blocks the machine and offers the wipe: `ichor reclaim node ADDRESS --cluster NAME`.
`ichor reclaim node` refuses a machine that is still an approved Ichor machine (use
`ichor delete node`), and accepts one of a managed cluster that has no approved `TalosMachine`
(an orphan). So the two commands never send you back and forth.

## How an adoption runs

![How an adoption runs, in five steps. Ichor reads the cluster and writes nothing in the plan. You consent, and the objects are written with each machine held, so it applies nothing. Each machine is released after a zero-drift check, control planes first. Then sign-in turns on. There is no rollback of an adoption.](../images/adopt-a-cluster-phases.light.drawio.svg)

*A machine is released only after a zero-drift check, so its first apply changes nothing. There is no rollback of an adoption.*

## The pages

Read them in order, or go to the one you need.

1. [Before you start](adopt-a-cluster/before-you-start.md): what the adopting machine needs, and what adopt never does.
2. [Plan and the secrets bundle](adopt-a-cluster/plan-and-secrets.md): the read-only plan, and where the cluster's CAs come from.
3. [The CNI](adopt-a-cluster/cni.md): Cilium, a cluster with no CNI, and taking over Cilium.
4. [Moving from flannel (or Calico)](adopt-a-cluster/moving-from-flannel.md): the options when the cluster runs another CNI.
5. [Run it](adopt-a-cluster/run.md): the command, what it prints, and the phases.
6. [Sign-in](adopt-a-cluster/sign-in.md): the first administrator and the authenticator.
7. [When Ichor was installed with Helm](adopt-a-cluster/with-helm.md): what adopt leaves alone.
8. [After adoption](adopt-a-cluster/after-adoption.md): the advice it prints, and what it does to running workloads.
9. [Add a machine that is already in the cluster](adopt-a-cluster/add-a-machine.md): `--add-machine`, for one more machine.

## Add a machine that is already in the cluster

A machine that already carries your cluster's configuration can join without a reset, with `ichor adopt --add-machine`. The steps and the checks are on [their own page](adopt-a-cluster/add-a-machine.md).
