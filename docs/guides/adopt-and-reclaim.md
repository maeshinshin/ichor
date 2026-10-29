# Adopt a running Talos cluster and reclaim a machine

This guide covers two things Ichor does with Talos machines it did not set up:
bringing a whole running cluster under management (`ichor adopt`), and
resetting one machine that another cluster configured so it can join an Ichor
cluster (`ichor machine reclaim`, or the UI).

Status: built. The hardware test plan has not been run
yet, so treat both features as untested on real clusters until it has.

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

```
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
version down to v1.12), the CNI it found and the kube-proxy setting. Override
the contract with `--version-contract v1.13`, for example.

### Step 2: the secrets bundle

Give adopt the bundle in one of two ways (they are mutually exclusive, and one
is required):

- `--secrets-bundle FILE`: a file produced by `talosctl gen secrets`.
- `--recover-secrets`: adopt rebuilds the bundle from a control plane's machine
  config. Read-only on the node. `--recover-from ADDRESS` names that control
  plane (required with `--recover-secrets`). The bundle is stored under
  `~/.ichor/clusters/<cluster>/` with mode 0600. Back it up; adopt does not
  print it.

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
the CNI it found and tells you to run again with `--cni-unmanaged`.

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
  Ichor-owned keys are refused.
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
  came from a Talos inline manifest there is no record: Ichor's defaults are
  used, and the diff shows what is missing.
- After the rollout is Ready, the `ichor-cni` Job copies the foreign Helm release
  record Secrets into `ichor-cilium-prior-release` in `ichor-system`, reads
  them back, and only then deletes the originals. The plan tells you this
  before you consent. `helm rollback` of the old release is no longer possible
  afterwards, but nothing is uninstalled.
- There is no rollback of an adoption.

### Step 4: run it

Run the same command without `--plan`, plus the flags the plan told you to add:

```
ichor adopt --cluster prod --node 10.0.0.11 --recover-secrets \
  --recover-from 10.0.0.11 --accept-cilium-diff --accept-cilium-changes
```

Adopt prints the plan again and asks for consent (`--yes` skips the prompt).
Then it works in phases:

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

## Reclaim a machine that another cluster configured ("Reset and add", MachineReclaim)

A node that already runs a configured Talos never reaches maintenance mode, so
Ichor's join flow cannot take it. Discovery reports it as `AlreadyConfigured`
and registers nothing. To join it, reset it. The reset wipes only the `STATE`
and `EPHEMERAL` partitions and reboots. User disks are not touched, but they
are listed, and encrypted ones are called out, because their keys are gone and
the data is unreadable. The machine then comes back in maintenance mode with
the same UUID and registers as a pending machine, which you approve as usual.

Both paths refuse:

- a machine that is already an Ichor machine or in an Ichor cluster;
- a control plane whose removal would leave its cluster's etcd without quorum,
  or that is the last control plane of a reachable cluster.

Both require you to type the machine's UUID. The wrong UUID sends nothing.

### With the CLI

This uses your own talosconfig for the machine. It is used for this command
only, never stored or printed.

```
ichor machine reclaim 10.0.0.21 --talosconfig ./foreign-talosconfig --cluster prod
```

It prints the machine's UUID, hostname, role, the cluster that will lose it,
its disks and, for a control plane, the etcd members, then asks for the UUID.
For scripts, pass `--confirm UUID` (`--yes` does not replace it).

Flags:

- `--cluster NAME`: add the machine's address to that `TalosCluster`'s
  `spec.discovery.addresses` afterwards so Ichor registers it.
- `--context C`: the talosconfig context to use.
- `--force`: allow a control plane whose removal breaks etcd quorum, or the
  last one. Also needs `--confirm-cluster NAME`, the foreign cluster's name
  typed a second time.
- `--force-ungraceful`: reset without cordon, drain and etcd leave, for a
  cluster that is dead. Without it the reset is graceful.
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

   ```
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
