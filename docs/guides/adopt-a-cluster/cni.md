---
sidebar_order: 3
---

# The CNI

## Step 3: the CNI

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
does not migrate a CNI (see [Moving from flannel](moving-from-flannel.md)).

With `--cni-unmanaged` the cluster keeps its CNI exactly as it is. Ichor does
not install, upgrade or configure it, and the Cilium commands (`get`, `edit`, `apply` and `diff cni-values`, `upgrade cni`, `set kube-proxy`) and `cni`
operations are refused for it. Everything else still works: Talos and
Kubernetes upgrades, CA rotation, config rollout, etcd backup, node add and
remove. This cannot be changed later (`spec.cni.preset` is immutable); going
from unmanaged to managed means a new cluster. Ichor's own network policies
are applied only if Cilium is detected; otherwise `ichor-system` is not
isolated by Ichor.

## A cluster with no CNI

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

## Taking over Cilium

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
- Cilium must be healthy before adopt writes anything: the `cilium` DaemonSet
  Ready on every node it is scheduled to, the operator Deployment Ready, and
  KubePrism answering on `127.0.0.1:7445` on every node (Talos's own
  `KubePrismStatuses` health, read through each node's Talos API; a node that cannot
  be asked counts as not answering). Taking over restarts the agents, and the
  `ichor-cni` Job reaches the API through KubePrism meanwhile. The check runs with
  `--plan`, before the consent question, and a failure refuses with nothing written;
  repair Cilium or KubePrism and run adopt again.
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

> [!WARNING]
> There is no rollback of an adoption.
