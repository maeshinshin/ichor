# Reclaim a machine

In the UI this is "Reset and add"; the resource behind it is a `MachineReclaim`.
Which command fits your machine: [Adopt a running Talos cluster](adopt-a-cluster.md#what-you-have-in-front-of-you).

A node that already runs a configured Talos never reaches maintenance mode, so
the join flow cannot take it: discovery reports `AlreadyConfigured` and
registers nothing. Reset it first.

![Five steps in a row: a node runs a configured Talos, reported as AlreadyConfigured or Orphan; ichor reclaim node inspects it and shows a report; you type the machine's UUID; the reset wipes STATE and EPHEMERAL and reboots; the node is back in maintenance mode as a pending machine you approve. A refusal can stop it after the inspection.](../images/reclaim-a-machine-flow.light.drawio.svg)

*Inspect, confirm with the UUID, reset, then approve as usual.*

## What the reset does

- Wipes the first MiB of `STATE` and `EPHEMERAL`, then reboots. User disks are
  not touched but are listed, encrypted ones called out. An encrypted
  `EPHEMERAL` cannot be opened again; an encrypted user disk can only if its key
  can still be derived, and a key locked to `STATE` (`lockToState`) cannot.
- The machine returns in maintenance mode with the same UUID as a pending
  machine.
- The UUID you type is the consent; a wrong one sends nothing, and `--yes` does
  not replace it.

Both paths refuse:

- an approved Ichor machine (use `ichor delete node`, which wipes it). A machine
  of a managed cluster with no approved `TalosMachine` is an orphan: accepted,
  with a warning that [`ichor adopt --add-machine`](adopt-a-cluster.md#add-a-machine-that-is-already-in-the-cluster)
  would keep its configuration.
- a control plane whose removal leaves etcd without quorum, or the last
  control plane of a reachable cluster.

Each inspection call gives up after 20 seconds. A control plane whose etcd is
not running is reported as such, and `--force-ungraceful` resets it. A stale
pending `TalosMachine` of the same UUID from an earlier reclaim is kept:
discovery re-reads the node within about five minutes (the address must be in
`spec.discovery`) and records the new certificate and disks on it; approve after
that.

## An orphan of a cluster Ichor manages

A machine removed without a wipe still carries its cluster's configuration.
Wipe it with the credentials Ichor holds for that cluster:

```sh
ichor reclaim node 10.0.0.21 --cluster prod
```

The controller inspects and resets it, deletes its Kubernetes Node once it is
back, and the CLI registers it as pending. You need the `reclaim` verb, and the
cluster must be in the Ichor namespace. A machine with an approved
`TalosMachine` is refused: use Remove node.

Discovery reports such a machine as `Orphan` (a Talos configuration at the
address of a Kubernetes Node that no `TalosMachine` accounts for, matched by
system UUID) when its address is in the cluster's `spec.discovery`. The Add node
page lists it with Reset and add preset ("It was removed from this cluster";
leave the talosconfig empty), and `ichor describe cluster` shows it under
"Possible orphans". The report wipes nothing. A foreign `AlreadyConfigured`
device is reset only through the pasted-talosconfig flow.

:::tabs

::tab[With the CLI]

Uses your own talosconfig for this command only; it is never stored or printed.

```sh
ichor reclaim node 10.0.0.21 --talosconfig ./foreign-talosconfig --cluster prod
```

It prints the UUID, hostname, role, the cluster that loses the node, its disks
and, for a control plane, the etcd members, then asks for the UUID (`--confirm
UUID` for scripts).

```text
w1 · 10.0.0.21
  Role         worker · Talos v1.13.2
  UUID         5b2c0d1e-7f44-4c35-9a0e-2f6f1c4b8e11
  Cluster      old (id 3f9a), reachable from the node
  Reset        STATE and EPHEMERAL, then reboot · graceful
  Volume       data on /dev/sdb1 (10737418240 bytes), not wiped  ! ENCRYPTED: if its key is locked to STATE (lockToState), the reset wipes STATE, which holds the salt that key needs, so the data is unreadable afterwards

This wipes STATE and EPHEMERAL on w1 and removes it from cluster "old".
Type the machine's UUID to continue: 5b2c0d1e-7f44-4c35-9a0e-2f6f1c4b8e11
✓ Reset — accepted, graceful
✓ Maintenance mode — 10.0.0.21 answers with UUID 5b2c0d1e-7f44-4c35-9a0e-2f6f1c4b8e11
✓ Register — added to cluster prod's discovery addresses; it will appear as a pending node
Done in 2m41s

Next
  ichor get nodes
```

A refusal is a `✗ refused:` line. If the machine does not come back, the
`Maintenance mode` step fails with a hint that DHCP may have given it a new address.

| Flag | Effect |
|---|---|
| `--cluster NAME` | add the address to that `TalosCluster`'s `spec.discovery.addresses` afterwards |
| `--context C` | talosconfig context |
| `--force` | allow a control plane that breaks quorum, or the last one; also needs `--confirm-cluster NAME` (the foreign cluster's name) |
| `--force-ungraceful` | reset without cordon, drain and etcd leave, for a dead cluster. Without it a machine that never joined anything is refused, since a graceful reset would hang |
| `--wait DURATION` | wait for maintenance mode (default 10m). `0` skips the check that the machine came back and the discovery registration, so it will not appear as pending until you add it; for an orphan it returns once the request is confirmed |

::tab[With the UI ("Reset and add")]

The `reclaim` verb on `machinereclaims` decides who may use it; below
`ichor-admin` nobody has it until you grant it. There is no installation switch
(`reclaim.enabled` and `IchorInstallation.spec.reclaim` are deprecated and
ignored). The UI hides the button without the verb, the webhook checks it on
every create and confirmation, and the UI acts as the signed-in user.

1. To grant it, bind `ichor-machine-reclaimer` (installed with
   `rbac.helpers.enabled`) in Ichor's namespace:

   ```sh
   kubectl -n ichor-system create rolebinding reclaim-alice \
     --clusterrole=ichor-machine-reclaimer --user=ichor:alice
   ```

2. On the cluster's "Add node" page (or Machines), choose "Reset and add" beside
   an address that runs a Talos configuration. Enter the address and paste or
   upload its talosconfig; name the context if there are several.
3. Ichor inspects the machine using only that address and stops at "Awaiting
   confirmation", writing nothing. You see the foreign cluster, the orphaned and
   encrypted volumes and any refusals.
4. Type the UUID (the box is not pre-filled). The controller resets the node and
   the machine appears as pending.

The credential: a Secret in `ichor-system`, used for one reset at one address,
alive 15 minutes by default (at most 60) and deleted when the request is `Done`,
`Failed` or `Expired`. A sweeper deletes older ones. It is never written to
logs, Events, status, error text or the UI API access log, but anyone who can
read Secrets in `ichor-system` can read it meanwhile. The `MachineReclaim` stays
7 days as a record without it; `status.audit` keeps who requested and confirmed.

With no credential for the machine at all there is no API route; reinstall from
the ISO or wipe the disk another way.

:::

## Let Ichor find new machines

Ichor only looks where the cluster says. A machine booted from the Talos ISO or
over PXE waits in maintenance mode until its address, or a range containing it,
is in `spec.discovery`.

```console
$ ichor discovery add 192.168.1.51 --cluster demo
✓ added 192.168.1.51 to the discovery of demo

$ ichor discovery list --cluster demo
ADDRESS/CIDR      KIND     FOUND
192.168.1.51      address  fa70962a
192.168.1.0/24    range    fa70962a, 3c1d0e44
```

- `ichor discovery add|list|remove ADDRESS|CIDR`; name the cluster with
  `--cluster` unless the namespace has one. In the UI, type the address or range
  on the Add node page and press Add; removing a range asks first.
- A range is IPv4, `/24` or smaller, at most 4 per cluster, scanned on the Talos
  maintenance port. The scan starts at once; if nothing answers, the controller
  probes every 15 seconds for 3 minutes, then the 5-minute pass applies.
- Found machines are pending on the Add node page and in `ichor get nodes`.
  Removing an address stops scanning only; machines already found stay.
- An address that already runs a Talos configuration is not added as a machine.
  `add` ends with a `!` line naming [`ichor adopt --cluster NAME --add-machine ADDRESS`](adopt-a-cluster.md#add-a-machine-that-is-already-in-the-cluster)
  (this cluster's certificates) or `ichor reclaim node` (another cluster's).
