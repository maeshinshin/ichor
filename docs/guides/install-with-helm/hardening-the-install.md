---
sidebar_order: 6
---

# Hardening the install

- Pod Security: the install manifest labels `ichor-system` with
  `pod-security.kubernetes.io/enforce: restricted` (and audit and warn). Every
  Ichor pod meets that profile; the Cilium Helm Job needs the host network and
  runs in `kube-system`. The chart does not own the namespace, so a Helm install
  labels it once: `kubectl label namespace ichor-system
  pod-security.kubernetes.io/enforce=restricted
  pod-security.kubernetes.io/audit=restricted
  pod-security.kubernetes.io/warn=restricted`.
- Priority: both pods run under the PriorityClass `ichor-critical` (value
  1000000), created by the manifest and by the chart (`priorityClass.create`).
  It ranks above ordinary workloads and below every `system-*` class, so the
  controller, which reconciles the Talos nodes, is evicted last of the user
  workloads. `system-cluster-critical` is not used: a user namespace may not
  use it by default.
- Replicas: the raw install manifest runs one replica of each pod and has no
  PodDisruptionBudget (a budget of `minAvailable: 1` on one replica would block
  every node drain). The chart runs two replicas and ships the budgets.
- Network: see [Network policies](network-policies.md): default-deny plus one
  allow per flow, with no `world` egress left.
- Releases are published only after the verify job passes, from a protected
  `release` GitHub environment. Maintainers: create the environment with
  required reviewers and a tag ruleset for `v*` in the repository settings.
