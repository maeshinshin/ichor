# 0007. e2e runs Talos nodes as KubeVirt VMs

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

Two shapes were possible: run the standard `talosctl cluster create qemu`
inside a privileged pod, or boot Talos as KubeVirt virtual machines. The first
keeps the documented provisioner; the second means writing the VM definitions
ourselves, since talosctl has no KubeVirt provisioner.

The second was tried and it works, with a result better than the first would
have given:

- A VM booting the Image Factory `metal-amd64.iso` for Talos 1.14.3 reaches
  `MachineStatus` `STAGE: maintenance, READY: true`.
- The insecure maintenance API answers on port 50000 from any pod in the
  cluster. It is **not** reachable through `kubectl port-forward`: with
  `bridge` binding the pod IP belongs to the guest, so the virt-launcher pod's
  own network namespace has no listener for port-forward to dial, and the
  attempt is refused. (Port-forward does work under `masquerade`, which is how
  this was first measured before the binding changed — a detail that cost a
  debugging cycle downstream.) Tests therefore reach the Talos API from a
  one-shot pod inside the cluster, which is also how the controller will reach
  nodes in production.
- `get systeminformation` returns an SMBIOS UUID and serial number — exactly
  the primary key `TalosMachine.spec.identity` is built on — with
  `manufacturer: KubeVirt`.
- `get disks` shows the ISO as `sr0` and a writable 11 GB `vda`, so there is a
  real install target, and `get links` shows a real NIC plus bond and dummy
  devices.

That is Ichor's entry point reproduced exactly: a node that is up, unconfigured,
and readable over an unauthenticated connection.

## Decision

e2e runs Talos nodes as KubeVirt virtual machines on `talos-cluster01`, in the
`ichor-e2e` namespace, instead of `talosctl cluster create qemu` on a
developer's workstation. KubeVirt v1.9.1 and CDI v1.66.1 are installed there
and managed by Argo CD from the cluster's own GitOps repository, so the test
platform is described in Git like everything else in that cluster.

CI keeps the `talosctl cluster create qemu` path: GitHub's `ubuntu-24.04`
runners provide `/dev/kvm` and passwordless sudo, and Talos's own
documentation treats them as sufficient. So there are two harnesses, which is
the cost of this decision, and the e2e suite takes the cluster it is given
through `KUBECONFIG` and a set of node endpoints rather than creating one
itself.

Three facts worth keeping, each of which cost a debugging cycle:

- **CDI must be told `volumeMode: Filesystem`.** `ceph-block`'s StorageProfile
  offers `Block` first for `ReadWriteOnce`, CDI takes it, and the importer —
  which does not run as root — dies with `blockdev: cannot open
  /dev/cdi-block-volume: Permission denied`. Setting the mode explicitly on the
  DataVolume fixes it.
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
- Ichor owns the VM manifests that talosctl's provisioner would otherwise have
  provided, and does not get `--presets maintenance` or the provisioner's
  networking for free.
- Multi-node clusters work on the pod network with KubeVirt's **`bridge`**
  binding, not `masquerade`. `masquerade` NATs the VM behind its pod IP and
  forwards only the ports declared on the interface, which would mean
  enumerating every port Talos nodes use between themselves — apid, trustd,
  apiserver, etcd, kubelet — plus whatever the inner cluster's CNI
  encapsulates with. `bridge` gives the VM the pod IP itself, so there is no
  NAT and no port list: measured from an ordinary pod, with no ports declared,
  50000 is reachable while 50001, 22 and 6443 are refused — which is the VM's
  real state in maintenance mode, where only apid is listening, not a filter.
  VM-to-VM traffic therefore reduces to pod-to-pod traffic, which Cilium
  already provides, and **Multus is not needed**.
- The e2e suite must not assume it created the cluster, which is the right
  shape anyway: it is also how `adopt` will be tested later.
- **Nor may it assume it created the nodes.** Two harnesses means the suite
  cannot contain the provisioning of either: a KubeVirt VM is meaningless on a
  CI runner that has no KubeVirt, and `talosctl cluster create qemu --presets
  maintenance` is meaningless here. So the suite consumes a cluster through
  `KUBECONFIG` *and* a set of maintenance-mode node endpoints through the
  environment, failing loudly when either is absent, and each harness's setup
  target supplies them its own way. One set of specs runs on both.
