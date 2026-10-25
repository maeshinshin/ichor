# 0028. `ichor gitops export`/`handoff`: an allow-list, two tracking checks, a one-field patch

Status: Accepted · Date: 2026-10-26 · Design section: GitOps and Argo CD

## Context

ADR 0024 built the CNI hand-over reconciler and its CR field
(`TalosClusterCNI.Handover`) but explicitly left `ichor gitops handoff cni`
to a later unit: "the CLI command that would flip a manual cluster to
adopted is explicitly another agent's work and is not built here." The
design document's "GitOps and Argo CD" section separately describes
`ichor gitops export` as writing the same repository skeleton `ichor init
--gitops-dir` writes, but for a cluster that already exists. This unit
builds both CLI subcommands.

Two things make `export` harder than "serialize every CR": a `TalosOperation`
CRD is being built by another agent concurrently and does not exist in this
package yet, so nothing can reference it directly; and the design requires
warning when a resource `export` is about to skip is already tracked by
Argo CD or Flux outside of Ichor, which needs two different detection
mechanisms (`argocd-cm`'s own tracking method, Flux's managed-fields
signal) rather than one.

## Decision: an allow-list, not a deny-list, for what `export` writes

`runGitopsExport` (`internal/cli/gitops_export.go`) only ever reads four
CR kinds for a cluster -- `TalosCluster`, `TalosMachine`, `TalosConfigPatch`,
`TalosStorage` -- and the CNI's own `*gitops.Application` computed the same
way `ichor init --argocd` computes it. There is no step that walks "every CR
referencing this cluster" and no step that excludes specific kinds by name.
A `TalosOperation` (or `TalosEtcdBackup`, or `IchorInstallation`) is excluded
simply because nothing in `export` ever lists it -- not because a check
says "skip TalosOperation," which is the form of exclusion that would need
updating the day that type lands. This also matches the design's own
framing of what belongs in Git: declared, approved state, not an imperative
operation's record.

Within `TalosMachine`, only approved machines (`Spec.IsApproved()`) are
written. An unapproved machine is a discovered candidate, not yet a decision
an operator made; exporting it would commit something to Git nobody asked
Git to track.

A `corev1.Secret` is never a candidate in the first place -- `export` never
lists Secrets -- and `pkg/gitops.WriteObject` refuses one outright as a
second, independent guard (ADR 0015).

## Decision: tracked-by-Argo-CD-or-Flux is a warning, checked only where `export` already skips something

`isTracked` (`internal/cli/gitops_tracking.go`) is called only for an
unapproved `TalosMachine` -- the one case `export` already has a reason to
skip something that might surprise an operator if it turns out to already
be managed by another GitOps tool. It never errors just because Argo CD or
Flux isn't installed (a missing `argocd-cm`, or no managed field at all,
both mean "not tracked," not "unknown") -- a cluster running neither is the
common case.

Two independent checks, because the two tools mark ownership differently:

- **Argo CD**: `argocd-cm`'s own `application.resourceTrackingMethod` and
  `application.instanceLabelKey` are read, never assumed -- the default has
  moved between Argo CD releases (ADR 0024 already made this point about
  render-time removal; the same applies here). If the ConfigMap exists but
  does not set a method, both the annotation
  (`argocd.argoproj.io/tracking-id`) and the label are checked, since a
  missed true positive costs a warning not shown, while an extra check
  costs nothing.
- **Flux**: it marks nothing on the resource itself -- ownership lives in
  the owning Kustomization's `.status.inventory` -- but every object Flux
  applies carries a `managedFields` entry naming the
  `flux-client-side-apply` field manager. Reading that one field on the
  object already in hand is far cheaper than listing every Kustomization in
  the namespace and searching each one's inventory for this object, and
  needs no extra API call at all.

## Decision: `handoff` is a one-field patch, not a second hand-over implementation

`runGitopsHandoff` (`internal/cli/gitops_handoff.go`) fetches the named
`TalosCluster` and, if `spec.cni.handover` is not already `"automatic"`,
patches it to `"automatic"`. That is the entire command. Reading
`internal/controller/cnihandover_controller.go` confirms this is sufficient:
`CNIHandoverReconciler.Reconcile` already branches on this exact field, and
only `automatic` mode polls the CNI's Argo CD `Application` and eventually
flips the `CNIHandover` condition. `handoff` never talks to Argo CD, never
reads a `TalosCluster`'s status, and never removes anything itself --
duplicating any of that would be rebuilding ADR 0024's reconciler a second
time in the CLI, which the controller/CLI split this project keeps
everywhere else (the controller is the only writer of Talos nodes' effective
state; the CLI changes the world by writing CRs) already rules out.

The command takes a `<component>` positional argument validated against a
one-entry slice (`handoffComponents = []string{"cni"}`) rather than a fixed
`cobra.ExactArgs` plus a hardcoded string check, so the day a second
hand-over reconciler exists, adding its name to that slice -- and its own
field-patch branch -- is the only change `gitops handoff` needs.

## Consequences

- `internal/cli/gitops_export.go`'s and `gitops_handoff.go`'s `RunE`
  handlers are factored into `runGitopsExport`/`runGitopsHandoff`, taking a
  `client.Client` directly, so tests exercise them against a fake client
  without going through cobra -- the same pattern `runAdopt`/`runInit`
  already use.
- `gitops export`'s allow-list means a cluster with `cni.preset: custom` gets
  no CNI `Application` in the skeleton: a custom CNI has no `pkg/cni.Preset`
  implementation to compute one from (ADR 0013), the same way `ichor init
  --argocd` already leaves it out for a custom preset.
- Not built here: PR mode (design phase 8), strict mode, break-glass, and
  `ichor ui` port-forwarding -- none of which this unit's two commands touch.
- `docs/adr/README.md`'s index briefly shows a gap at 0026/0027: those
  numbers are reserved for other agents' concurrent work in sibling
  worktrees and are expected to land before this branch merges, closing the
  sequence `index_test.go`'s `TestNumbersAreContiguous` checks for.
