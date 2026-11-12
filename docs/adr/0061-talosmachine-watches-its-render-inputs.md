# 0061. A TalosMachine re-reconciles when a render input's spec changes

Status: Accepted · Date: 2026-10-26 · Design section: Nodes
Decided: `TalosMachineReconciler` watches `TalosStorage`, `TalosConfigPatch` and `TalosCluster` through one mapping, `machinesOfCluster` (every machine of the object's cluster), filtered to creates, deletes and spec changes (`predicate.GenerationChangedPredicate`); a Secret named by `registries[].authSecretRef` (controller namespace only) maps through an index on `TalosCluster` to the machines of the clusters that reference it, and a hash of the resolved credentials joins the desired-inputs hash (follow-up below)

## Context

`SetupWithManager` watched only `TalosMachine` and `TalosStorage`, and `cmd/main.go` sets no `SyncPeriod`, so an
edit to a `TalosConfigPatch` or to the render inputs of a `TalosCluster` (`kubernetesVersion`, `registries`,
`driftPolicy`, ...) reached an already converged node after up to 10 hours. ADR 0060's requeue does not help: it only
revisits machines whose last reconcile failed, and a converged machine returns an empty Result.

## Decision

- One mapping, `machinesOfCluster`, serves all three watches: a `TalosCluster` maps by its own name, a
  `TalosConfigPatch` and a `TalosStorage` by `spec.clusterRef`. It replaces `machinesForStorage`; there is one
  function declaring a `TalosMachineList` for this.
- Each watch uses `predicate.GenerationChangedPredicate`. It filters update events only, so create and delete still
  enqueue, and a status write (a `TalosCluster` going `Transitioning`, a `TalosStorage` condition) does not wake every
  machine of the cluster. These are CRDs with a status subresource, so `generation` moves exactly on spec writes.
- A patch created, or a registry mirror added, after the machine converged is applied promptly, well under the 30s
  requeue of ADR 0060: the watch, not the requeue, delivers it.

## Secrets are not watched (superseded: they are now)

Follow-up (a changed credential used to be a reboot-requiring path; since ADR 0009's registry addendum it is applied live, so the watch makes the machine apply it promptly): the machine controller watches Secrets in the controller namespace, mapped through a `TalosCluster` field index on `authSecretRef.name`; the Secret informer already existed (cache.go scopes it to that namespace), so no RBAC changes. `desiredInputsHash` includes `registryAuthFingerprint` (a hash of the resolved password/auth/identityToken, not the resourceVersion, so label edits do not count), and the TalosStorage status controller computes the same value. The text below is the original reasoning.

`spec.registries[].authSecretRef` (ADR 0027) is a render input too, but a Secret watch needs a decision this change
should not make quietly: the controller's Secret RBAC is namespace-scoped to `ichor-system`, and a watch is a cached
informer over every Secret there, plus a mapping from Secret to the clusters that reference it. The load and the
privilege are real and the case is rare.

The operational consequence: after changing a registry password, nothing happens until some `spec` of a render input
of that cluster changes (touching the `TalosCluster` spec, for instance) or the controller restarts or resyncs
(up to 10 hours). If this proves too slow, a follow-up can watch Secrets with a label or an `authSecretRef` index.

ADR 0060's requeue stays failure-only: a change reaching a converged machine is the watch's job.
