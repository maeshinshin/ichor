---
sidebar_order: 8
---

# After adoption

Adopt prints advice, and the UI shows it on the cluster page until you dismiss
it: the Talos and Kubernetes client certificates of the previous
administrators, and the CA keys held by whoever created the cluster, remain
valid. Consider `ichor rotate ca`. Nothing starts a rotation for you.

## What adopt does to your running workloads

Adopt reads Kubernetes and the nodes, and writes only Ichor's own objects (the
`TalosCluster`, `TalosMachine`s, patches, the bundle Secret, and Ichor itself in
`ichor-system`, with its first administrator). It does not touch your namespaces, Deployments, Services or
storage, and it installs no GitOps tool, storage or monitoring. What
can still move your pods:

- The Cilium takeover (managed mode only): the Cilium pods are restarted on every
  node when the rendered chart differs from what runs. The plan announces it and
  the diff gate shows what changes. `--cni-unmanaged` skips it.
- Sign-in marks every control plane `APIServerRestartRequired`; kube-apiserver
  restarts only when you run `ichor rollout config` (see [Sign-in](sign-in.md)).
- The first apply on each machine, after the hold is released, only happens when
  the live configuration already equals what Ichor renders (zero drift), so it
  changes nothing. Any later change is yours, through a patch or an operation.
- Later upgrades drain nodes as usual and respect PodDisruptionBudgets.

The cluster may run another Talos or Kubernetes version than your `ichor`
binary's release set. Adopt takes the running versions as they are (the floor is
Talos v1.14 for the version contract); an unknown version is refused, an incompatible pair is a warning.
