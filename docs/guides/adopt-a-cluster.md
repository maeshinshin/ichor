# Adopt a running Talos cluster

Ichor can take over Talos machines it did not set up: a whole running cluster
(`ichor adopt`) or one machine that already carries a managed cluster's
certificates (`ichor adopt --add-machine`). To reset a machine another cluster
configured, see [Reclaim a machine](reclaim-a-machine.md).

![How an adoption runs, in five steps. Ichor reads the cluster and writes nothing in the plan. You consent, and the objects are written with each machine held, so it applies nothing. Each machine is released after a zero-drift check, control planes first. Then sign-in turns on. There is no rollback of an adoption.](../images/adopt-a-cluster-phases.light.drawio.svg)

*A machine is released only after a zero-drift check, so its first apply changes nothing. There is no rollback of an adoption.*

## Which command

| Your machine or cluster | Command | Reset? |
|---|---|---|
| A running cluster Ichor has never seen | `ichor adopt` (this guide) | No |
| One more machine that runs a configuration signed by a cluster Ichor manages (joined with your own `talosctl apply-config`), kept as it is | `ichor adopt --cluster NAME --add-machine ADDRESS` | No |
| A machine removed from a managed cluster (its `TalosMachine` deleted, node not wiped) | `ichor reclaim node ADDRESS --cluster NAME`, or the UI "Reset and add" with "It was removed from this cluster" | Yes: `STATE` and `EPHEMERAL` |
| A machine another Talos cluster configured, with its talosconfig in hand | `ichor reclaim node`, or the UI "Reset and add" | Yes: `STATE` and `EPHEMERAL` |
| A machine never configured, or with `STATE` wiped (ISO, maintenance mode), including one removed with `ichor delete node` | The normal Add node flow | No |
| A configured machine and no credential for it | None through the API: reinstall from the ISO, or wipe it with a kernel argument | Yes |

`ichor delete node` sends the wipe. With the default power off, power the
machine on first. With `--reboot` it returns by itself, and the removal waits up
to five minutes for it to answer in maintenance mode.

`ichor adopt --add-machine` refuses a machine whose configuration does not name
this cluster's ID and Talos API CA, and points to
[`ichor reclaim node`](reclaim-a-machine.md). If the configuration of a machine
that does carry the CAs cannot be represented (typically one removed without a
wipe), it blocks the machine and offers the wipe. `ichor reclaim node` refuses a
machine that is still an approved Ichor machine (use `ichor delete node`) and
accepts an orphan of a managed cluster.

## The pages

1. [Before you start](adopt-a-cluster/before-you-start.md): what the adopting machine needs, and what adopt never does.
2. [Plan and the secrets bundle](adopt-a-cluster/plan-and-secrets.md): the read-only plan, and where the cluster's CAs come from.
3. [The CNI](adopt-a-cluster/cni.md): Cilium, a cluster with no CNI, and taking over Cilium.
4. [Moving from flannel (or Calico)](adopt-a-cluster/moving-from-flannel.md): the options when the cluster runs another CNI.
5. [Run it](adopt-a-cluster/run.md): the command, what it prints, and the phases.
6. [Sign-in](adopt-a-cluster/sign-in.md): the first administrator and the authenticator.
7. [When Ichor was installed with Helm](adopt-a-cluster/with-helm.md): what adopt leaves alone.
8. [After adoption](adopt-a-cluster/after-adoption.md): the advice it prints, and what it does to running workloads.
9. [Add a machine that is already in the cluster](adopt-a-cluster/add-a-machine.md): `--add-machine`, for one more machine.
