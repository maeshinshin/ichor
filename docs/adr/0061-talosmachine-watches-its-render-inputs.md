# 0061. A TalosMachine re-reconciles when a render input's spec changes

Status: Accepted · Date: 2026-10-26 · Design section: Nodes
Decided: `TalosMachineReconciler` watches `TalosStorage`, `TalosConfigPatch` and `TalosCluster` through one mapping, `machinesOfCluster` (every machine of the object's cluster), filtered to creates, deletes and spec changes (`predicate.GenerationChangedPredicate`); Secrets, including `registries[].authSecretRef`, are not watched

## Context

`SetupWithManager` watched only `TalosMachine` and `TalosStorage`, and `cmd/main.go` sets no `SyncPeriod`, so an
edit to a `TalosConfigPatch` or to the render inputs of a `TalosCluster` (`kubernetesVersion`, `registries`,
`driftPolicy`, ...) reached an already converged node after up to 10 hours. ADR 0060's requeue does not help: it only
revisits machines whose last reconcile failed, and a converged machine returns an empty Result.

## Decision

- One mapping, `machinesOfCluster`, serves all three watches: a `TalosCluster` maps by its own name, a
  `TalosConfigPatch` and a `TalosStorage` by `spec.clusterRef`. It replaces `machinesForStorage`; there is one
  function declaring a `TalosMachineList` for this, in the same `countplanes_guard_test.go` allow-list slot.
- Each watch uses `predicate.GenerationChangedPredicate`. It filters update events only, so create and delete still
  enqueue, and a status write (a `TalosCluster` going `Transitioning`, a `TalosStorage` condition) does not wake every
  machine of the cluster. These are CRDs with a status subresource, so `generation` moves exactly on spec writes.
- `envtest` runs the real manager and checks that a patch created, or a registry mirror added, after the machine
  converged is applied within 15s, under the 30s requeue of ADR 0060, so the watch is what delivers it.

## Secrets are not watched

`spec.registries[].authSecretRef` (ADR 0027) is a render input too, but a Secret watch needs a decision this change
should not make quietly: the controller's Secret RBAC is namespace-scoped to `ichor-system`, and a watch is a cached
informer over every Secret there, plus a mapping from Secret to the clusters that reference it. The load and the
privilege are real and the case is rare.

The operational consequence: after changing a registry password, nothing happens until some `spec` of a render input
of that cluster changes (touching the `TalosCluster` spec, for instance) or the controller restarts or resyncs
(up to 10 hours). If this proves too slow, a follow-up can watch Secrets with a label or an `authSecretRef` index.

ADR 0060's requeue stays failure-only: a change reaching a converged machine is the watch's job.
