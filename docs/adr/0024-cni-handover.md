# 0024. CNI hand-over: no registry, render-time removal, a separate controller

Status: Accepted (amended 2026-10-26); superseded in part by ADR 0099 · Date: 2026-10-25 · Design section: GitOps and Argo CD

Decided: No CNI hand-over registry (one real member); removal happens at render time off a status condition; a separate controller; the generated Application syncs automatically (prune, selfHeal), amended 2026-10-26

## Context

ADR 0019 built `ichor init` through step 5 and deliberately excluded step 6
(hand-over): "it is a continuous reconciliation loop against Argo CD's own
status (watch, poll, retry) ... It belongs to the controller." This ADR is that controller's design.

The design document's "GitOps and Argo CD" section originally described
hand-over as "one mechanism with a component registry, not three special
cases," implying three components (CNI, Ichor, Argo CD) each go through
inject-into-`cluster.inlineManifests` → adopted by Argo CD → removed. That is not
what `ichor init` (ADR 0019) does:

- **Ichor** is applied directly through the API server (`applyIchorManifest`),
  never inlined. It never needs hand-over.
- **Argo CD** is never installed by Ichor at all.
- **The CNI** is the only one that genuinely has to be inlined (a node isn't
  Ready and nothing can be scheduled before a CNI exists), and the only one
  Argo CD later adopts.

So the registry would have exactly one member.

## Decision: no registry

The rule applies directly here: don't build an abstraction over a
single implementation. `pkg/cni.Preset` already declares everything a CNI
hand-over needs (`BootstrapManifest`, `ArgoCDApplication`,
`HandoverWorkloads`) as part of the one interface every CNI implements. A
"component registry" would be a second interface wrapping the first for no
caller that exists. The hand-over reconciler calls `preset.ArgoCDApplication`
directly to get the Name/Namespace it needs to look up; there is nothing to
register.

## Decision: removal happens at render time, not by rewriting a `TalosConfigPatch`

Why removal has to actually happen, not just "Argo CD adopting it": Talos's
`ManifestApplyController` (per Talos v1.14.3's source) only ever
server-side-applies `cluster.inlineManifests` and re-applies them on every
Kubernetes upgrade -- it has no delete path. If the CNI's inlined entry were
left in place, an upgrade would silently roll the CNI back to whatever Ichor
originally inlined, overwriting anything Argo CD has since changed. The same
controller refuses to apply over an object carrying another controller's
inventory annotation, which sounds like protection -- but Argo CD's own
tracking marker (configured by `argocd-cm`'s `application.resourceTrackingMethod`,
per the design doc) is not that annotation, so
adoption alone does not stop the re-apply.

Given that, removing the entry has to be a real, visible state change, not an
implicit side effect of Argo CD's status. It is implemented in
`pkg/render.BuildConfig`: once `TalosCluster.status` carries the
`CNIHandover` condition as `True`, every patch's
`ClusterConfig.ClusterInlineManifests` has the entry named
`cni.InlineManifestName` filtered out before merging
(`dropHandedOverInlineManifest`/`withoutInlineManifest`,
`pkg/render/render.go`). This follows the design's existing precedent
(control-plane taint folding already happens in render, not as a stored
patch) and principle 5 (the controller renders from ingredients; it does not
rewrite them): no `TalosConfigPatch` object is ever mutated -- a patch
carrying the entry is deep-copied only for the filtered copy that gets
merged, and every other patch passes through as the original pointer
(`pkg/render/render_test.go`'s `TestCNIHandoverDropsInlineManifest` pins
this, including pointer identity).

## Decision: automatic / manual / disabled, as a CR field only

`TalosClusterCNI.Handover` (`api/v1alpha1/taloscluster_types.go`) is one of
`automatic` (default), `manual`, or `disabled`, matching the design's "Hand-over
can be automatic, manual (`ichor gitops handoff cni`), or disabled when Argo CD
isn't used." The reconciler branches on the field: `disabled` and `manual` both set the `CNIHandover` condition `False` (with
a reason naming which) and never requeue. The `ichor gitops handoff cni` CLI
command that would flip a manual cluster to adopted is separate work.

## Decision: a separate controller, not a bump to `TalosClusterReconciler`

`CNIHandoverReconciler` (`internal/controller/cnihandover_controller.go`) is
its own controller watching `TalosCluster`, not folded into
`TalosClusterReconciler`. `TalosClusterReconciler`'s own doc comment already
warns `DiscoverAll` can block a single `TalosCluster`'s reconcile for up to
~68s at `spec.discovery`'s caps. Raising that reconciler's
`MaxConcurrentReconciles` would not have helped: controller-runtime's
workqueue deduplicates and serializes by object key *within one controller*,
so a single `TalosCluster` only ever has one in-flight reconcile through one
controller's queue regardless of concurrency settings -- a slow discovery
pass would still block a hand-over check sharing the same queue. Two
controllers watching the same Kind each get an independent work queue, so a
slow `DiscoverAll` reconcile in one never delays the other.

## Decision: Argo CD absent is "not here yet," never a hang or a hard error

`automatic` mode reads the CNI's Argo CD `Application` as
`unstructured.Unstructured` (the standard controller-runtime pattern for a
foreign CRD this project neither owns nor vendors Go types for) at the exact
Name/Namespace `preset.ArgoCDApplication` itself returns -- which is also how
"adoption produces no diff" is satisfied: the same template that was injected
is the only thing ever looked up, so there's no second, independently-guessed
shape to drift from.

Both `apierrors.IsNotFound` (Argo CD exists but hasn't adopted the Application
yet) and `apimeta.IsNoMatchError` (Argo CD's CRDs aren't installed at all --
the realistic "this cluster doesn't run Argo CD" case) are treated
identically: the `CNIHandover` condition is set `False` with reason
`WaitingForArgoCD`, and the reconcile returns a bounded `RequeueAfter`
(`handoverRequeueInterval`, 30s), never a blocking wait or an error. A cluster
that genuinely never runs Argo CD should set `spec.cni.handover: disabled` to
stop this polling; left at the `automatic` default it polls forever and says
so plainly, which satisfies "does not break" (nothing blocks, nothing retries
in a tight loop) without guessing that the operator meant `disabled`.

> Update (ADR 0055): the handed-over Application now also carries `ignoreDifferences` on `CiliumIdentity`; release name, namespace and values are unchanged and a test compares them with the bootstrap values.

## Consequences

- `pkg/cni.InlineManifestName` is an exported constant duplicating
  `internal/cli`'s own unexported `cniKey`: both `pkg/render` and
  `internal/controller` need the exact string Ichor inlined the manifest
  under, and neither can import the CLI package. `pkg/cni` is safe for both to import (its own
  subpackages `cilium`/`flannel` import it, not the reverse).
- `CNIHandoverReconciler.cniPresetFor` duplicates `internal/cli`'s own preset
  switch (`"cilium"`/`"flannel"`, `"custom"` deliberately absent, same as ADR
  0013) for the same reason: a controller cannot import the CLI package.
- Not part of this decision: the `ichor gitops handoff cni` CLI command.

> Update (ADR 0055 addendum, 2026-10-26): the generated Cilium Application now carries `syncPolicy.automated {prune: true, selfHeal: true}` and `PruneLast=true`. Without it Argo CD never synced the Application, so it never became Synced + Healthy and the hand-over never completed. Ichor still never changes the sync policy of an existing Application; it only chooses one when it creates its own.

> Update (2026-10-28): `cluster.inlineManifests`, `cluster.extraManifests` and `cluster.extraManifestHeaders` are `safe` in `pkg/fieldmeta`. After the hand-over Ichor stops rendering the CNI entry. With the default (reboot-required) class the single control plane would sit `Ready=False/RebootRequired` for that pending removal (the machine controller never reboots), a later CNI operation would pause on "unrelated pending configuration change at cluster.inlineManifests", and only `ichor rollout config` would clear it, by rebooting the node (~3 min API outage). Talos v1.14.3 needs no reboot: `controllers/k8s/control_plane.go:510-552` rebuilds `ExtraManifestsConfig` live from the active MachineConfig on control planes only (`controlplaneMapFunc`, `:44-66`); `extra_manifest.go:131-141` destroys the no-longer-listed `k8s.Manifest` resource; `manifest_apply.go:230-393` only server-side-applies (skips inventoried objects, `:335-338`) and has no delete path, so removing an entry deletes nothing in the cluster. The machine controller therefore applies the removal live right after the hand-over and nothing reboot-required is left behind.

## Amendment (2026-10-28): the hand-over status is a render input, so its removal is a change, not drift

The machine controller classes a node/render mismatch as drift when `desiredInputsHash` (the
generations of the cluster, machine, patches, storage, plus the registry credentials) equals the
hash recorded at the last convergence. The hand-over writes `status.conditions[CNIHandover]`, which
moves no generation, so the hash stays put while the render drops the CNI's
`cluster.inlineManifests` entry. The removal would read as drift and, under the default `warnOnly`, never be
applied (`NodeDrifted`). `desiredInputsHash` now also folds in whether `CNIHandover` is True
and `status.cni.kubeProxy` (ADR 0066), the two status values the render reads. The removal is then
an intended change and is applied live under any `driftPolicy`, since `cluster.inlineManifests` is
safe and needs no reboot. Hashes of clusters that have neither are unchanged.
