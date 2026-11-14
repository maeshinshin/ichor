# Reclaim a machine

In the UI this is "Reset and add"; the resource behind it is a `MachineReclaim`.
Not sure it is the right command for your machine? The table in
[Adopt a running Talos cluster](adopt-and-reclaim.md#what-you-have-in-front-of-you)
says which command each case takes.

A node that already runs a configured Talos never reaches maintenance mode, so
Ichor's join flow cannot take it. Discovery reports it as `AlreadyConfigured`
and registers nothing. To join it, reset it. The reset wipes only the `STATE`
and `EPHEMERAL` partitions and reboots. User disks are not touched, but they
are listed, and encrypted ones are called out. A key locked to `STATE` (`lockToState`)
needs a salt that the reset deletes, so its data is unreadable afterwards; the keys
themselves are not gone. The `STATE` and `EPHEMERAL` partitions are unreadable after
the reset because the reset wipes them, not because of any key. The machine then comes back in maintenance mode with
the same UUID and registers as a pending machine, which you approve as usual.

Both paths refuse:

- a machine that is already an approved Ichor machine (take it out with
  `ichor delete node`, which wipes it). A machine that belongs to a cluster Ichor manages but
  has no approved `TalosMachine` is an orphan and is accepted, with a warning that
  [`ichor adopt --add-machine`](adopt-and-reclaim.md#add-a-machine-that-is-already-in-the-cluster) would keep its configuration instead. A stale pending `TalosMachine` for the same UUID, left
  by an earlier reclaim, does not count (it pinned the old maintenance certificate; the
  cluster's discovery re-reads the node on its next pass, within about five minutes, and
  records the new certificate and disks on that same machine, so approve it after that.
  This needs the node's address in the cluster's `spec.discovery`);
- a control plane whose removal would leave its cluster's etcd without quorum,
  or that is the last control plane of a reachable cluster.

Every call the CLI makes to the machine while inspecting it gives up after 20 seconds. A
control plane whose etcd is not running (a foreign cluster that never bootstrapped, or a dead
one) is reported as "not running or not answering", and `--force-ungraceful` resets it: there is
no quorum to protect and no graceful leave to wait for.

Both require you to type the machine's UUID. The wrong UUID sends nothing.

## An orphan of a cluster Ichor manages

A machine removed without a wipe still carries its cluster's configuration, and you
usually have no talosconfig for it. Wipe it with the credentials Ichor already holds for
that cluster:

```sh
ichor reclaim node 10.0.0.21 --cluster prod
```

The controller inspects the machine, the CLI shows the same report, you type the UUID, and the
controller sends the same reset (`STATE` and `EPHEMERAL`, then a reboot into maintenance mode),
deletes the machine's Kubernetes Node once it is back, and the CLI registers it as a pending
machine. A control plane that is still in etcd is subject to the same quorum refusals. The
cluster must be in the Ichor namespace, and you need the `reclaim` verb. In the UI, Reset and add
shows "It was removed from this cluster" on the Add node page; tick it and leave the talosconfig
empty. A machine that still has an approved `TalosMachine` is refused: use Remove node.

Discovery finds such a machine for you when its address is in the cluster's `spec.discovery`: a node
that runs a Talos configuration at the address of a Kubernetes Node that no `TalosMachine` of the
namespace accounts for (matched by the Node's system UUID) is reported as `Orphan`, not
`AlreadyConfigured`. The Add node page lists it with Reset and add already set to the cluster's own
credentials, and `ichor describe cluster` shows it under "Possible orphans". Discovery cannot prove the
machine is this cluster's (the probe is unauthenticated); the wipe can, because only this cluster's
certificate authority can dial it. Nothing is wiped by the report, and a machine reported as
`AlreadyConfigured` (a foreign device) is only reset by the pasted-talosconfig flow with its typed UUID. The
lists on the page come from the latest report per address and drop an address that a pending or
approved machine of the cluster has since taken, so a machine that became pending again no longer
shows a Reset and add link. On the Nodes page, Reset and add also offers "It was removed from this
cluster", and the inspection step shows the same warning as the CLI (the machine belongs to this
cluster; `ichor adopt --add-machine` would keep its configuration instead).

## With the CLI

This uses your own talosconfig for the machine. It is used for this command
only, never stored or printed.

```sh
ichor reclaim node 10.0.0.21 --talosconfig ./foreign-talosconfig --cluster prod
```

It prints the machine's UUID, hostname, role, the cluster that will lose it,
its disks and, for a control plane, the etcd members, then asks for the UUID.
For scripts, pass `--confirm UUID` (`--yes` does not replace it).

```text
w1 · 10.0.0.21
  Role         worker · Talos v1.13.2
  UUID         5b2c0d1e-7f44-4c35-9a0e-2f6f1c4b8e11
  Cluster      old (id 3f9a), reachable from the node
  Reset        STATE and EPHEMERAL, then reboot · graceful
  Volume       data on /dev/sdb1 (10737418240 bytes), not wiped  ! ENCRYPTED: its key may depend on STATE, so the data may be unreadable afterwards

This wipes STATE and EPHEMERAL on w1 and removes it from cluster "old".
Type the machine's UUID to continue: 5b2c0d1e-7f44-4c35-9a0e-2f6f1c4b8e11
✓ Reset — accepted, graceful
✓ Maintenance mode — 10.0.0.21 answers with UUID 5b2c0d1e-7f44-4c35-9a0e-2f6f1c4b8e11
✓ Register — added to cluster prod's discovery addresses; it will appear as a pending node
Done in 2m41s

Next
  ichor get nodes -n default
```

A refusal (the machine is already an Ichor machine, or a control plane whose
removal breaks etcd quorum) is a `✗ refused:` line under the block. If the
machine does not come back, the `Maintenance mode` step fails with the hint that
it may have taken a new address from DHCP.

Flags:

- `--cluster NAME`: add the machine's address to that `TalosCluster`'s
  `spec.discovery.addresses` afterwards so Ichor registers it.
- `--context C`: the talosconfig context to use.
- `--force`: allow a control plane whose removal breaks etcd quorum, or the
  last one. Also needs `--confirm-cluster NAME`, the foreign cluster's name
  typed a second time.
- `--force-ungraceful`: reset without cordon, drain and etcd leave, for a
  cluster that is dead. Without it the reset is graceful. A machine that was
  installed with a configuration of its own and never joined anything has no
  reachable cluster, so a graceful reset would hang: Ichor refuses it and tells
  you to pass this flag.
- `--wait DURATION`: how long to wait for maintenance mode (default 10m,
  0 to not wait).

If the node's DHCP address changed after the reboot, the command says so and
suggests a scan instead of guessing.

## With the UI ("Reset and add")

"Reset and add" (the `MachineReclaim` kind) resets the STATE and EPHEMERAL
partitions of a machine that another Talos cluster configured and adds it to
your cluster. It puts a talosconfig for the other cluster into a Secret in your
Ichor cluster for a few minutes. There is no installation switch: the `reclaim`
verb on `machinereclaims` decides who may use it, and nobody below `ichor-admin`
has it until you grant it. (The chart value `reclaim.enabled` and
`IchorInstallation.spec.reclaim` are deprecated and ignored.)

1. Who may use it. `ichor:admin` holds the verb. To grant it to anyone else,
   bind the ClusterRole `ichor-machine-reclaimer` (installed with
   `rbac.helpers.enabled`) in the namespace Ichor runs in (default
   `ichor-system`):

   ```sh
   kubectl -n ichor-system create rolebinding reclaim-alice \
     --clusterrole=ichor-machine-reclaimer --user=ichor:alice
   ```

   The role grants `create`, `get`, `list`, `update` and the verb `reclaim` on
   `machinereclaims`. The UI checks the verb and does not offer the button to
   anyone without it; the webhook checks it again on every create and on every
   confirmation. With Ichor's users off, the UI acts as its ServiceAccount, so
   bind the role to `ichor-system:ichor-ichor-ui` the same way.
2. In the UI, open the cluster's "Add node" page. When discovery reaches an
   address that already runs a Talos configuration, the page offers "Reset and
   add" next to the message; the Machines page has the same button. Enter the
   machine's address and paste or upload its talosconfig. If the file has several
   contexts, name the one to use.
3. Ichor inspects the machine, using only that address (other endpoints in the
   file are ignored) and stops at "Awaiting confirmation". Nothing has been
   written to the node. You see the foreign cluster's name, the orphaned and
   encrypted volumes and the refusals that apply.
4. Type the UUID. Pasting works; the box is not pre-filled. Only an exact match
   goes through. The controller then resets the node, waits for maintenance
   mode and the machine appears as pending in the target cluster.

Whoever holds the other cluster's admin talosconfig can already reset that
machine with `talosctl`; the typed UUID, the quorum and last-control-plane
refusals, the short-lived single-use Secret and the audit Events are what Ichor
adds.

The credential:

- It is used for one reset and for that one address.
- It lives for 15 minutes by default (at most 60) and is deleted when the
  request is `Done`, `Failed` or `Expired`. A sweeper deletes any such Secret
  older than the maximum, even if its request is gone.
- It is not written to logs, Events, status, error text or the UI API's access
  log. Anyone who can read Secrets in `ichor-system` can still read it while it
  exists, so grant that sparingly.
- The `MachineReclaim` object stays for 7 days as a record with no credential
  in it. `status.audit` keeps who requested and who confirmed it.

If you cannot give the UI path to anyone, use the CLI. If you have no
credential for the machine at all there is no API route; reinstall from the ISO
or wipe the disk by other means.

## Let Ichor find new machines: one address or a whole range

Ichor only looks where the cluster says. A machine booted from the
Talos ISO or over PXE waits in maintenance mode until its address, or a range
that contains it, is listed in the cluster's `spec.discovery`.

- UI: on the cluster's "Add node" page type `192.168.0.50` or
  `192.168.0.0/24` into the one input and press Add. Each listed address and
  range has a remove button; removing a range asks first.
- CLI: `ichor discovery add 192.168.0.0/24` (or an address),
  `ichor discovery list` and `ichor discovery remove ADDRESS|CIDR`.
  Name the cluster with `--cluster` unless the namespace has only one.

`add` and `remove` print one line, `list` a table of what is scanned and what
each address or range has found so far (the API keeps no time of the last probe):

```console
$ ichor discovery add 192.168.1.51 --cluster demo
✓ added 192.168.1.51 to the discovery of demo

$ ichor discovery list --cluster demo
ADDRESS/CIDR      KIND     FOUND
192.168.1.51      address  fa70962a
192.168.1.0/24    range    fa70962a, 3c1d0e44
```

An address that already runs a Talos configuration is not added as a machine;
`add` then ends with a `!` line that says so and names the two commands that apply:
[`ichor adopt --cluster NAME --add-machine ADDRESS`](adopt-and-reclaim.md#add-a-machine-that-is-already-in-the-cluster) if it carries this cluster's certificates,
`ichor reclaim node` if another cluster configured it.

A range must be IPv4 and `/24` or smaller (at most 256 addresses), and a cluster
holds at most 4 ranges. It is scanned address by address on the Talos
maintenance port, and the scan starts as soon as you add it, not at the next
5 minute pass. If nothing answers yet (the machine is still booting, or the
network rule for the new address is still being installed), the controller
probes it again every 15 seconds for the first 3 minutes, so a machine that
comes up within that time is found within about 15 to 30 seconds of answering;
after that the normal 5 minute pass applies. Machines found appear as pending on the Add node page (and in
`ichor get nodes`) and wait for approval. Removing an address or range stops
the scanning only; machines already found stay.
