---
sidebar_order: 4
---

# Moving from flannel (or Calico) to a Cilium Ichor manages

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
