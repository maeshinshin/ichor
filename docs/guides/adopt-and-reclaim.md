# Adopt a running Talos cluster and reclaim a machine

This guide covers three things Ichor does with Talos machines it did not set up:
bringing a whole running cluster under management (`ichor adopt`), adding one
machine that already carries the cluster's own certificates to a cluster Ichor
manages (`ichor adopt --add-machine`), and resetting one machine that another
cluster configured so it can join an Ichor cluster (`ichor machine reclaim`, or
the UI).

Status: built and hardware-verified on Talos v1.14 (2026-10-30). Not yet run on bare metal.
The `--add-machine` and `--cilium-values` (takeover) options and the reclaim and refusal text are built and tested against fakes; their hardware cases have not been run.

## What you have in front of you

| Your machine or cluster | Command | Needs a reset? |
|---|---|---|
| A running cluster Ichor has never seen | `ichor adopt` (below) | No |
| One more machine that already runs a configuration signed by a cluster Ichor manages (it joined with your own `talosctl apply-config`) and that you want to keep as it is | `ichor adopt --cluster NAME --add-machine ADDRESS` | No |
| A machine removed from a cluster Ichor manages (its `TalosMachine` was deleted, the node was not wiped, and it still carries the cluster's configuration) | `ichor machine reclaim ADDRESS --cluster NAME` (no talosconfig) or the UI "Reset and add" with "It was removed from this cluster" | Yes: `STATE` and `EPHEMERAL` |
| A machine another Talos cluster (or a config of its own) configured, and you hold its talosconfig | `ichor machine reclaim` or the UI "Reset and add" | Yes: `STATE` and `EPHEMERAL` |
| A machine that was never configured, or whose `STATE` was wiped (booted from the ISO, in maintenance mode), including one removed with `ichor node remove` (it is wiped; power it on if it was powered off) | The normal Add node flow | No |
| A configured machine and no credential for it | None through the API: reinstall from the ISO, or wipe it with a kernel argument | Yes |

How to tell them apart: `ichor adopt --add-machine` reads the machine's own configuration and
refuses one that does not name this cluster's ID and Talos API CA, and says to use
`ichor machine reclaim` instead. For a machine that does carry this cluster's CAs it keeps the
configuration; if that configuration cannot be represented (typically a machine removed without a wipe, whose hostname and network link Ichor rendered from its deleted `TalosMachine`), it
blocks the machine and offers the wipe: `ichor machine reclaim ADDRESS --cluster NAME`.
`ichor machine reclaim` refuses a machine that is still an approved Ichor machine (use
`ichor node remove`), and accepts one of a managed cluster that has no approved `TalosMachine`
(an orphan). So the two commands never send you back and forth.

## Adopt a running cluster

### What you need

- Every node you adopt runs Talos v1.12 or later. A cluster with any node
  below that is refused with "upgrade Talos first", and nothing is written.
  A mixed cluster is refused the same way.
- Your own `talosconfig` for the cluster (`--talosconfig`, `$TALOSCONFIG` or
  `~/.talos/config`) and a kubeconfig for its Kubernetes API (`--kubeconfig`,
  `$KUBECONFIG` or `~/.kube/config`). Adopt reads nodes with the talosconfig
  and never sends it to the controller.
- The cluster's secrets bundle, either as a file or recovered from a control
  plane (below).

Adopt never applies anything to a node itself. The controller does, in the
last phase, and only after a zero-drift check.

### Step 1: look at the plan

`--plan` reads the cluster and prints what adopt would do. It writes nothing,
not even a Secret.

```sh
ichor adopt --cluster prod --node 10.0.0.11 --recover-secrets \
  --recover-from 10.0.0.11 --plan
```

`--cluster` is the name the `TalosCluster` will have. `--node` is one control
plane; adopt finds the other machines from the cluster's membership when the
`TalosCluster` does not exist yet. If the file holds several clusters, pick one
with `--context`.

The plan shows, per machine, one of three kinds of difference between the
live config and what Ichor would render:

- Representable (sysctl and kernel-module documents, subnets, install disk).
  Adopt stores these as `TalosConfigPatch` objects.
- Ichor-owned (the Cilium inline manifest entry, KubePrism, kube-proxy when
  replaced, removal of Talos's flannel). Ichor will change these on the first
  render. They are listed and need your consent. They do not block.
- Unrepresentable (anything else in the config, such as another inline
  manifest). That machine is blocked, with the paths and a hint, and is left
  out. If every machine is blocked nothing is written.

It also prints the Talos version contract it chose (probed from the running
version down to v1.12), the CNI it found and the kube-proxy setting. The cluster
may run an older or a newer Talos or Kubernetes than this Ichor's release set;
adopt takes it as it runs. A version the compatibility table does not know at
all is refused, since Ichor could not operate the cluster. An incompatible pair
(a Kubernetes its Talos does not support, or one the installed Cilium does not
run) is reported as a warning and adopted; the upgrade that would make it
compatible is refused until it is. `--version-contract` is probed
from the running Talos unless you name one.

### Step 2: the secrets bundle

Give adopt the bundle in one of two ways (they are mutually exclusive, and one
is required):

- `--secrets-bundle FILE`: a file produced by `talosctl gen secrets`, or an
  encrypted one Ichor wrote earlier (adopt asks for its passphrase, see below).
  A plaintext file works but draws a warning: encrypt it with
  `ichor secrets encrypt --file FILE`.
- `--recover-secrets`: adopt rebuilds the bundle from a control plane's machine
  config. Read-only on the node. `--recover-from ADDRESS` names that control
  plane (required with `--recover-secrets`). The bundle is stored under
  `~/.ichor/clusters/<cluster>/` with mode 0600, encrypted like the one
  `ichor init` writes (a generated passphrase printed once on a terminal,
  `--bundle-passphrase-file`, or `ICHOR_BUNDLE_PASSPHRASE`;
  `--bundle-plaintext` opts out). Back up the file and its passphrase; adopt
  does not print the bundle.

A wrong or partial bundle cannot get through. It shows up as secret-field
drift, and no patch is produced for that machine.

### Step 3: the CNI

Adopt inspects the CNI before it writes anything.

| Found | What adopt does |
|---|---|
| Cilium Ichor bootstrapped | Checks the release exists. |
| Cilium from Helm, the Cilium CLI, a Talos inline manifest or an applied `helm template` | Takes it over as Ichor's own Helm release (below), or leaves it alone with `--cni-unmanaged`. |
| Talos's flannel, Calico or another CNI | Refused unless you pass `--cni-unmanaged`. |
| No CNI at all (`cni.name: none`, no CNI workload, every node NotReady with "cni plugin not initialized") | Installs Ichor's pinned Cilium first, then Ichor, then adopts as managed Cilium. `--cni-unmanaged` is refused. |
| `cni.name: none` with a CNI you applied by hand | Adopted unmanaged (`talosCNI: none`), the CNI left as it is. |
| Two CNIs at once | Refused, even with `--cni-unmanaged`. |

The mode is never chosen for you. The refusal for a non-Cilium cluster names
the CNI it found, tells you to run again with `--cni-unmanaged`, and says that Ichor
does not migrate a CNI (see "Moving from flannel" below).

With `--cni-unmanaged` the cluster keeps its CNI exactly as it is. Ichor does
not install, upgrade or configure it, and `ichor cni ...` commands and `cni`
operations are refused for it. Everything else still works: Talos and
Kubernetes upgrades, CA rotation, config rollout, etcd backup, node add and
remove. This cannot be changed later (`spec.cni.preset` is immutable); going
from unmanaged to managed means a new cluster. Ichor's own network policies
are applied only if Cilium is detected; otherwise `ichor-system` is not
isolated by Ichor.

A cluster with no CNI:

- Its nodes are NotReady, so no pod, Ichor's included, can run. Adopt installs
  the Cilium pinned in your `ichor` binary as Ichor's own Helm release (release
  `cilium` in `kube-system`, records in `ichor-system`) before it installs
  Ichor, and waits up to 10 minutes for the nodes to be Ready.
- Preconditions, checked read-only before the plan asks for consent: Talos
  v1.12 or later, KubePrism enabled on port 7445 on every node (Talos's
  default), and a pod CIDR on every node.
- Nothing is detected from an existing Cilium. `--tunnel vxlan|geneve` picks
  the tunnel (immutable afterwards). Kube-proxy is read from Talos: enabled
  gives `kubeProxyReplacement=false`, disabled gives `true`.
  `--cilium-values FILE` merges your Cilium Helm values over Ichor's defaults;
  Ichor-owned keys are refused, and writing values needs `update` on
  `talosclusters/cilium-values` (`ichor-admin` has it).
- The plan says what will be installed and needs your consent. There is no
  `ichor-cni` Job and no inline manifest to remove, so hand-over completes at
  once. If adopt stops half-way, run it again.

Taking over Cilium:

- The live Cilium minor must equal the minor of the chart in your Ichor build,
  or be one lower. Otherwise adopt refuses and tells you to upgrade it with the
  tool that installed it.
- `ipam.mode` must be `kubernetes`. A `cluster-pool` Cilium is refused.
- Adopt renders the pinned chart and compares each object with the live one.
  A difference is printed per object and path, and needs
  `--accept-cilium-diff`.
- A setting Ichor owns (for example `kubeProxyReplacement`) that Ichor would
  change on the running data plane needs `--accept-cilium-changes`. The IPAM
  mode and the tunnel protocol are never accepted.
- Your Helm values are read from the newest release record. For a Cilium that
  came from a Talos inline manifest (the usual Talos-guide install, made with
  `helm template`) there is no record, so Ichor cannot read your values. Give
  them with `--cilium-values FILE` (the file you passed to `helm template`): they
  are checked like `spec.cni.cilium.values` (Ichor-owned keys are refused), the
  diff gate renders the chart with them, and the new `TalosCluster` keeps them.
  Without the file Ichor's defaults are used and the diff lists every value you
  set; accepting it with `--accept-cilium-diff` then replaces them with the
  defaults. With a release record the flag is refused: the record's values are
  used. The flag only works for a cluster adopt creates, not one that exists.
- After the rollout is Ready, the `ichor-cni` Job copies the foreign Helm release
  record Secrets into `ichor-cilium-prior-release` in `ichor-system`, reads
  them back, and only then deletes the originals. The plan tells you this
  before you consent. `helm rollback` of the old release is no longer possible
  afterwards, but nothing is uninstalled.
- There is no rollback of an adoption.

### Step 4: run it

Run the same command without `--plan`, plus the flags the plan told you to add:

```sh
ichor adopt --cluster prod --node 10.0.0.11 --recover-secrets \
  --recover-from 10.0.0.11 --accept-cilium-diff --accept-cilium-changes
```

Adopt shows its steps like `ichor init` does: one line per step, the findings of
the plan as lines of their own (`!` for what needs your attention: a skipped
machine, Ichor-owned settings, a Cilium difference, the Helm record that will be
deleted), and a `Done in` and `Next` at the end. It asks for consent before it
writes anything (`--yes` skips the prompt). The patches themselves are not
printed; add `-o yaml` (or `json`) to print them on stdout, in which case adopt
keeps writing its `adopt: ...` lines on stderr as before. A run on a cluster with
no CNI that Ichor has never seen looks like this:

```text
Ichor adopt · prod
namespace ichor-system · discovering from 10.0.0.11
✓ Read cluster — Talos v1.14.5 · Kubernetes v1.36.6 · 2 read · no TalosCluster yet
  no CNI found: Cilium will be installed by adopt (Cilium 1.20.4, tunnel the default, kube-proxy enabled) as Ichor's own Helm release, then Ichor is installed, then the cluster is adopted with the managed cilium preset
✓ Inspect CNI — CNI: none (no CNI installed, every node NotReady)
  machine "uuid-10.0.0.10": already matches a fresh render, nothing to do
  machine "uuid-10.0.0.11": already matches a fresh render, nothing to do
✓ Plan — no patch needed
✓ Secrets bundle — stored under ~/.ichor/clusters/prod/
✓ Install Cilium — Cilium is installed and the nodes are Ready
✓ Create resources — 5 created, 0 unchanged
✓ Cilium hand-over — Helm release owned by Ichor
✓ Release holds — 2 released
Done in 4m12s
! the Talos and Kubernetes client certificates of the previous administrators, and the CA keys held by whoever created this cluster, remain valid; consider `ichor cluster rotate-ca`

Next
  ichor cluster status prod -n default
  ichor machine list -n default
```

The consent prompt and the reminder to back up a recovered secrets bundle stay
as they were (the reminder is a `!` line). Then it works in phases:

1. Writes the bundle Secret, the `TalosCluster`, the `TalosMachine`s (held),
   the patches and, if Ichor is not running yet, Ichor's install manifest. On a
   cluster with no CNI Cilium is installed before this, as above.
   A held machine has the annotation `ichor.maesh.dev/hold-apply`; the
   controller reports its drift (condition `AdoptionHeld`) and applies
   nothing.
2. Takes over Cilium and waits for the `CNIHandover` condition. Skipped with
   `--cni-unmanaged`.
3. Releases the hold machine by machine, control planes first, each only after
   a live zero-drift check. The controller applies the first render.

Only approved machines are read. A pending one is skipped with a message;
approve it and run adopt again.

If adopt stops half-way, run it again. The writes are idempotent. A run where
every machine already matches a fresh render says so and exits.

### When Ichor was installed with Helm

If a Helm release of the chart `ichor` is deployed (or the controller
Deployment carries the chart's labels), adopt skips its install step, creates
no `IchorInstallation`, and the plan says "Ichor is installed by Helm; not
touching it".

- Ichor's version must equal the CLI's exactly. On a mismatch adopt refuses and
  prints the `helm upgrade` command.
- Adopt does not apply Ichor's Cilium network policies on a Helm install. If
  they apply to your cluster it tells you to set
  `networkPolicy.cilium.enabled=true` and run `helm upgrade`.
- `--require-ichor` makes adopt fail when Ichor is not installed, instead of
  installing it.

See [Install Ichor with Helm](install-with-helm.md).

### After adoption

Adopt prints advice, and the UI shows it on the cluster page until you dismiss
it: the Talos and Kubernetes client certificates of the previous
administrators, and the CA keys held by whoever created the cluster, remain
valid. Consider `ichor cluster rotate-ca`. Nothing starts a rotation for you.

### What adopt does to your running workloads

Adopt reads Kubernetes and the nodes, and writes only Ichor's own objects (the
`TalosCluster`, `TalosMachine`s, patches, the bundle Secret, and Ichor itself in
`ichor-system`). It does not touch your namespaces, Deployments, Services or
storage, and it installs no GitOps tool, storage or monitoring. What
can still move your pods:

- The Cilium takeover (managed mode only): the Cilium pods are restarted on every
  node when the rendered chart differs from what runs. The plan announces it and
  the diff gate shows what changes. `--cni-unmanaged` skips it.
- The first apply on each machine, after the hold is released, only happens when
  the live configuration already equals what Ichor renders (zero drift), so it
  changes nothing. Any later change is yours, through a patch or an operation.
- Later upgrades drain nodes as usual and respect PodDisruptionBudgets.

The cluster may run another Talos or Kubernetes version than your `ichor`
binary's release set. Adopt takes the running versions as they are (the floor is
Talos v1.12); an unknown version is refused, an incompatible pair is a warning.

### Moving from flannel (or Calico) to a Cilium Ichor manages

Ichor does not migrate a CNI, and `spec.cni.preset` cannot be changed once a
cluster exists, so an unmanaged adoption stays unmanaged. The options:

1. **Leave the CNI.** Adopt with `--cni-unmanaged`. Everything else Ichor does
   keeps working. This is the default recommendation.
2. **A new cluster.** Build one with `ichor init` and move the workloads.
3. **A planned outage.** Ichor adopts a cluster that has no CNI (above) by installing
   its own Cilium. You can get to that state yourself: set
   `cluster.network.cni.name: none` in the machine configuration, remove the old CNI
   workload, and reboot the nodes, so that every node is NotReady with no CNI
   running. Pods have no network from the first of those steps until Cilium is up.
   Then run `ichor adopt`. Ichor does not run the removal, and this route has not
   been run on hardware yet; try it on a copy first.

Cilium's own documented live migration from flannel installs Cilium next to the old
CNI with a new pod CIDR and `ipam.mode: cluster-pool`. It cannot be used here: it ends
on `cluster-pool`, which Ichor refuses to take over (changing IPAM renumbers pods) and
does not use (Ichor's Cilium runs `ipam.mode: kubernetes`).

## Add a machine that is already in the cluster

A machine that already runs a configuration signed by your cluster's own certificate
authorities, but that Ichor has no `TalosMachine` for, cannot be found by discovery (it
is not in maintenance mode) and must not be reset (a reset would wipe an etcd member or a
joined kubelet for no reason). This is the usual state of a node you added with your own
`talosctl apply-config` after the adoption, or of one whose `TalosMachine` you deleted by hand or removed without a wipe. A machine removed with `ichor node remove` (or the UI "Remove node") is wiped and shows up as a new pending machine instead.
`ichor adopt --add-machine` registers it with the same safety as an adoption.

```sh
ichor cluster credentials prod                      # a talosconfig for the cluster, if you have none
ichor adopt --cluster prod --add-machine 10.0.0.21 --talosconfig ./talosconfig.ichor --plan
ichor adopt --cluster prod --add-machine 10.0.0.21 --talosconfig ./talosconfig.ichor
```

`--add-machine` is repeatable. It needs the `TalosCluster` to exist, in the namespace given
by `-n` (default `ichor-system`, where `ichor init` creates it, like every other command). The secrets bundle
is read from the cluster's own Secret (your kubeconfig must be allowed to read it), unless
you give `--secrets-bundle` or `--recover-secrets`; while a CA rotation is under way it
refuses. What it does, in order:

1. Reads each machine's identity and configuration with your talosconfig and checks that
   the configuration names this cluster's ID and Talos API CA. A machine that does not is
   refused with nothing written, and the message names `ichor machine reclaim`.
2. Skips a machine whose UUID is already a `TalosMachine`, and refuses a control plane
   that would exceed `spec.controlPlane.count` (raise the count first: 1, 3 or 5).
3. Plans it like an adoption: what the typed documents can hold (sysctl, kernel modules,
   install disk, image) is captured as one machine patch, Ichor-owned settings are
   listed, and anything else blocks that machine with the paths and a hint. The role and
   the install disk come from the machine's own configuration.
4. After consent, creates the `TalosMachine` approved and held, then releases the hold
   only after a live zero-drift check, waiting for Ready. The controller applies the first
   render; the CLI applies nothing. No CA rotation advice is printed.

`--plan` writes nothing. The flags of an adoption that make no sense here (`--node`,
`--cni-unmanaged`, `--tunnel`, `--cilium-values`, `--accept-cilium-*`, `--require-ichor`,
`--version-contract`) are refused.

Not covered: the UI has no button for this yet; discovery still reports such an address as
"already configured" (and the `!` line of `ichor cluster discovery add` now names both
commands).

## Let Ichor find new machines: one address or a whole range

Ichor only looks where the cluster says. A machine booted from the
Talos ISO or over PXE waits in maintenance mode until its address, or a range
that contains it, is listed in the cluster's `spec.discovery`.

- UI: on the cluster's "Add node" page type `192.168.0.50` or
  `192.168.0.0/24` into the one input and press Add. Each listed address and
  range has a remove button; removing a range asks first.
- CLI: `ichor cluster discovery add 192.168.0.0/24` (or an address),
  `ichor cluster discovery list` and `ichor cluster discovery remove ADDRESS|CIDR`.
  Name the cluster with `--cluster` unless the namespace has only one.

`add` and `remove` print one line, `list` a table of what is scanned and what
each address or range has found so far (the API keeps no time of the last probe):

```console
$ ichor cluster discovery add 192.168.1.51 --cluster demo
✓ added 192.168.1.51 to the discovery of demo

$ ichor cluster discovery list --cluster demo
ADDRESS/CIDR      KIND     FOUND
192.168.1.51      address  fa70962a
192.168.1.0/24    range    fa70962a, 3c1d0e44
```

An address that already runs a Talos configuration is not added as a machine;
`add` then ends with a `!` line that says so and names the two commands that apply:
`ichor adopt --cluster NAME --add-machine ADDRESS` if it carries this cluster's certificates,
`ichor machine reclaim` if another cluster configured it.

A range must be IPv4 and `/24` or smaller (at most 256 addresses), and a cluster
holds at most 4 ranges. It is scanned address by address on the Talos
maintenance port, and the scan starts as soon as you add it, not at the next
5 minute pass. If nothing answers yet (the machine is still booting, or the
network rule for the new address is still being installed), the controller
probes it again every 15 seconds for the first 3 minutes, so a machine that
comes up within that time is found within about 15 to 30 seconds of answering;
after that the normal 5 minute pass applies. Machines found appear as pending on the Add node page (and in
`ichor machine list`) and wait for approval. Removing an address or range stops
the scanning only; machines already found stay.

## Reclaim a machine that another cluster configured ("Reset and add", MachineReclaim)

A node that already runs a configured Talos never reaches maintenance mode, so
Ichor's join flow cannot take it. Discovery reports it as `AlreadyConfigured`
and registers nothing. To join it, reset it. The reset wipes only the `STATE`
and `EPHEMERAL` partitions and reboots. User disks are not touched, but they
are listed, and encrypted ones are called out, because their keys are gone and
the data is unreadable. The machine then comes back in maintenance mode with
the same UUID and registers as a pending machine, which you approve as usual.

Both paths refuse:

- a machine that is already an approved Ichor machine (take it out with
  `ichor node remove`, which wipes it). A machine that belongs to a cluster Ichor manages but
  has no approved `TalosMachine` is an orphan and is accepted, with a warning that
  `ichor adopt --add-machine` would keep its configuration instead. A stale pending `TalosMachine` for the same UUID, left
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

### An orphan of a cluster Ichor manages

A machine removed without a wipe still carries its cluster's configuration, and you
usually have no talosconfig for it. Wipe it with the credentials Ichor already holds for
that cluster:

```sh
ichor machine reclaim 10.0.0.21 --cluster prod
```

The controller inspects the machine, the CLI shows the same report, you type the UUID, and the
controller sends the same reset (`STATE` and `EPHEMERAL`, then a reboot into maintenance mode),
deletes the machine's Kubernetes Node once it is back, and the CLI registers it as a pending
machine. A control plane that is still in etcd is subject to the same quorum refusals. The
cluster must be in the Ichor namespace, and you need the `reclaim` verb. In the UI, Reset and add
shows "It was removed from this cluster" on the Add node page; tick it and leave the talosconfig
empty. A machine that still has an approved `TalosMachine` is refused: use Remove node.

### With the CLI

This uses your own talosconfig for the machine. It is used for this command
only, never stored or printed.

```sh
ichor machine reclaim 10.0.0.21 --talosconfig ./foreign-talosconfig --cluster prod
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
✓ Register — added to cluster prod's discovery addresses; it will appear as a pending machine
Done in 2m41s

Next
  ichor machine list -n default
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

### With the UI ("Reset and add")

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
