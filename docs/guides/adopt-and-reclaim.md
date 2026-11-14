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

## Adopt a running cluster

### What you need

- Every node you adopt runs Talos v1.14 or later, and the cluster's version
  contract is v1.14 or later. A cluster below that is refused before anything
  is written, and the refusal names the way out: upgrade Talos with
  `ichor upgrade talos`, then adopt again. Ichor turns sign-in on by default,
  and only v1.14 can carry its issuer. A mixed cluster is refused the same way.
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
version down to v1.12; a contract below v1.14 is refused, as above), the CNI it
found and the kube-proxy setting. The cluster may run a newer Talos or
Kubernetes than this Ichor's release set; adopt takes it as it runs. A version the compatibility table does not know at
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
not install, upgrade or configure it, and the Cilium commands (`get`, `edit`, `apply` and `diff cni-values`, `upgrade cni`, `set kube-proxy`) and `cni`
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
  v1.14 or later, KubePrism enabled on port 7445 on every node (Talos's
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
! the Talos and Kubernetes client certificates of the previous administrators, and the CA keys held by whoever created this cluster, remain valid; consider `ichor rotate ca`

Next
  ichor get cluster prod -n default
  ichor get nodes -n default
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
valid. Consider `ichor rotate ca`. Nothing starts a rotation for you.

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
Talos v1.14 for the version contract); an unknown version is refused, an incompatible pair is a warning.

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
   Then run `ichor adopt`. Ichor does not run the removal, so this route is yours to carry out: try it on a copy first.

Cilium's own documented live migration from flannel installs Cilium next to the old
CNI with a new pod CIDR and `ipam.mode: cluster-pool`. It cannot be used here: it ends
on `cluster-pool`, which Ichor refuses to take over (changing IPAM renumbers pods) and
does not use (Ichor's Cilium runs `ipam.mode: kubernetes`).

## Add a machine that is already in the cluster

A machine that already runs a configuration signed by your cluster's own certificate
authorities, but that Ichor has no `TalosMachine` for, cannot be found by discovery (it
is not in maintenance mode) and must not be reset (a reset would wipe an etcd member or a
joined kubelet for no reason). This is the usual state of a node you added with your own
`talosctl apply-config` after the adoption, or of one whose `TalosMachine` you deleted by hand or removed without a wipe. A machine removed with `ichor delete node` (or the UI "Remove node") is sent the wipe and, once it answers in maintenance mode, shows up as a new pending machine instead. With `--reboot` the machine answers by itself, and the removal waits up to five minutes for that answer before it completes; if none comes, a Warning Event says the wipe is not confirmed. After the default power off the removal does not wait: power the machine on, and it shows up as a pending machine once it answers.
`ichor adopt --add-machine` registers it with the same safety as an adoption.

```sh
ichor create credentials prod                      # a talosconfig for the cluster, if you have none
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
   refused with nothing written, and the message names `ichor reclaim node`.
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
"already configured" (and the `!` line of `ichor discovery add` now names both
commands).
