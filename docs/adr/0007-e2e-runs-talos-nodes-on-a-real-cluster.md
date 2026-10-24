# 0007. e2e runs Talos nodes on a real cluster

Status: Accepted · Date: 2026-10-25 · Design section: 7 (Testing)

## Context

The design says e2e creates throwaway clusters with `talosctl cluster create
qemu`. That provisioner runs QEMU on the host that invokes it and needs
`CAP_SYS_ADMIN` and `CAP_NET_ADMIN` — root. The development workstation has
`/dev/kvm` world-readable and VT-x enabled, but `sudo` requires a password that
an agent session cannot supply, and the machine has 4 vCPUs and 15 GB of RAM,
which caps it at a single-node cluster.

A second host was available: a three-node bare-metal Talos cluster
(`talos-cluster01`, Talos 1.13.1 / Kubernetes 1.36.3) whose first node has 16
cores and 62 GB. A privileged pod there sees real `/dev/kvm` and `/dev/net/tun`
on AMD-V hardware — no nesting involved.

The second was tried and it works, with a result better than the first would
have given:

- A node booting the Image Factory `metal-amd64.iso` for Talos 1.14.3 reaches
  `MachineStatus` `STAGE: maintenance, READY: true`.
- The insecure maintenance API answers on port 50000 from any pod in the
  cluster. (Port-forward does work under `masquerade`, which is how
  this was first measured before the binding changed — a detail that cost a
  debugging cycle downstream.) Tests therefore reach the Talos API from a
  one-shot pod inside the cluster, which is also how the controller will reach
  nodes in production.
- `get disks` shows the ISO as `sr0` and a writable 11 GB `vda`, so there is a
  real install target, and `get links` shows a real NIC plus bond and dummy
  devices.

That is Ichor's entry point reproduced exactly: a node that is up, unconfigured,
and readable over an unauthenticated connection.

## Decision

CI keeps the `talosctl cluster create qemu` path: GitHub's `ubuntu-24.04`
runners provide `/dev/kvm` and passwordless sudo, and Talos's own
documentation treats them as sufficient. So there are two harnesses, which is
the cost of this decision, and the e2e suite takes the cluster it is given
through `KUBECONFIG` and a set of node endpoints rather than creating one
itself.

Facts worth keeping, each of which cost a debugging cycle:

- **`talosctl` 1.14.3 has no global `--insecure`.** It is `-i/--insecure` on
  the individual commands, and flags must follow the subcommand.
- **`--cert-fingerprint` pins the server certificate even in maintenance
  mode.** The design notes that insecure apply does not authenticate the node
  and assumes a separate provisioning network. Fingerprint pinning is a second
  defence worth offering once a node's fingerprint is known, and the discovery
  flow should record it.

## Consequences

- e2e gets real disks, a real install target and real maintenance mode, on 16
  cores and 62 GB — enough for the `standard` topology that the workstation
  could never host.
- Ichor owns the node manifests that talosctl's provisioner would otherwise have
  provided, and does not get `--presets maintenance` or the provisioner's
  networking for free.
- `masquerade` NATs the node behind its pod IP and
  forwards only the ports declared on the interface, which would mean
  enumerating every port Talos nodes use between themselves — apid, trustd,
  apiserver, etcd, kubelet — plus whatever the inner cluster's CNI
  encapsulates with. `bridge` gives the node the pod IP itself, so there is no
  NAT and no port list: measured from an ordinary pod, with no ports declared,
  50000 is reachable while 50001, 22 and 6443 are refused — which is the node's
  real state in maintenance mode, where only apid is listening, not a filter.
  node-to-node traffic therefore reduces to pod-to-pod traffic, which Cilium
  already provides, and **Multus is not needed**.
- The e2e suite must not assume it created the cluster, which is the right
  shape anyway: it is also how `adopt` will be tested later.
- **Nor may it assume it created the nodes.** So the suite consumes a cluster through
  `KUBECONFIG` *and* a set of maintenance-mode node endpoints through the
  environment, failing loudly when either is absent, and each harness's setup
  target supplies them its own way. One set of specs runs on both.
