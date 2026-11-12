# 0028. `ichor gitops export`/`handoff`: an allow-list, two tracking checks, a one-field patch

Status: Superseded by ADR 0101 · Date: 2026-10-26 · Design section: GitOps and Argo CD

Decided: `ichor gitops export`'s allow-list of CR kinds and of the fields it writes for each (no status or server-assigned metadata), its Argo CD/Flux tracked-resource warning, and `ichor gitops handoff`'s one-field `TalosCluster` patch

## Context

ADR 0024 built the CNI hand-over reconciler and its CR field
(`TalosClusterCNI.Handover`); `ichor gitops handoff cni` is the CLI command that
flips a manual cluster to adopted. The design document's "GitOps and Argo CD"
section separately describes `ichor gitops export` as writing the same
repository skeleton `ichor init --gitops-dir` writes, but for a cluster that
already exists. This ADR covers both CLI subcommands.

Two things make `export` harder than "serialize every CR": the CR kinds that do
not belong in Git (a `TalosOperation` is a record of an imperative action) have
to stay out without `export` naming them; and the design requires warning when a
resource `export` is about to skip is already tracked by Argo CD or Flux outside
of Ichor, which needs two different detection mechanisms (`argocd-cm`'s own
tracking method, Flux's managed-fields signal) rather than one.

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
patches it to `"automatic"`. That is the entire command. This is sufficient:
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

## Decision: export writes the declared form of each CR, not the live object

Marshalling the objects read from the cluster as-is would put `status`,
`resourceVersion`, `uid`, `generation`, `creationTimestamp`, `managedFields` and
friends into Git (and, because a list result carries no `TypeMeta`, no
`apiVersion`/`kind` either). `resourceVersion` makes Argo CD's apply fail its
optimistic-concurrency check and `uid` makes the files unrestorable on another
cluster. So `export` writes the declared form of each CR.

`declaredForm` (`internal/cli/gitops_export.go`) is the single place every
exported CR passes through, after `runGitopsExport` has collected them,
whatever the kind. It is an allow-list: `apiVersion`, `kind` (from the
client's scheme), `metadata.{name,namespace,labels,annotations}` and `spec`.
Everything else is dropped, so a field a future API version adds stays out of
Git until someone decides it is declared state.

Dropped: `status`; `metadata.resourceVersion`, `uid`, `generation`,
`creationTimestamp`, `deletionTimestamp`, `deletionGracePeriodSeconds`,
`managedFields`, `finalizers`, `ownerReferences`, `selfLink`; and the
annotations `kubectl.kubernetes.io/last-applied-configuration` and
`argocd.argoproj.io/tracking-id` (they record how the object reached this
cluster; a restored cluster would claim an Application it is not in).

What about "the labels and annotations Ichor writes at runtime"? Ichor writes none on its own CRs: the controller never calls
`SetLabels`/`SetAnnotations` or assigns into `Labels`/`Annotations` on a
`Talos*` object (the design section is about labels on Kubernetes `Node`s and
the storage system's node objects, which export never lists). Every
`ichor.maesh.dev/` label or annotation on a CR is therefore the operator's
own and stays:

| Key | Kind | Who writes it | Exported |
|---|---|---|---|
| `ichor.maesh.dev/allow-delete` | TalosCluster, TalosMachine | operator | yes |
| `ichor.maesh.dev/reset-on-delete` | TalosMachine | operator | yes |
| `ichor.maesh.dev/upgrade-hold` | TalosMachine | operator | yes |
| `ichor.maesh.dev/upgrade-order` (label) | TalosMachine | operator | yes |
| `ichor.maesh.dev/confirm-kube-proxy-switch` | TalosCluster | operator | yes |
| `ichor.maesh.dev/machine-removal` (finalizer) | TalosMachine | controller | no (finalizers dropped) |
| `kubectl.kubernetes.io/last-applied-configuration` | any | kubectl | no |
| `argocd.argoproj.io/tracking-id` | any | Argo CD | no |
| any other label or annotation | any | operator or another tool | yes |

Argo CD's instance label (`app.kubernetes.io/instance`, key configurable in
`argocd-cm`) is kept: it cannot be told apart from an operator label without
reading `argocd-cm`, and with annotation tracking (the default) it is absent.

`gitops.WriteObject`'s refusal of a `corev1.Secret` no longer sees the typed
object for CRs, since they are now plain maps; the guard that matters is
unchanged, `export` never lists a Secret.

One `exportClusterKinds`, driven by a table of (file prefix, list type,
`clusterRef` accessor), serves `TalosConfigPatch`, `TalosStorage` and
`TalosEtcdBackup`. `exportMachines` stays separate: it has the approval check
and the tracked warning.

"Ichor warns if it sees them tracked" (architecture.md) is not implemented for
runtime labels or annotations: `gitopstracking.IsTracked` is called only for
unapproved `TalosMachine`s, and nothing looks for runtime labels or annotations
in tracked Node or storage objects.

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
- Amended: `TalosEtcdBackup` (ADR 0047) joined the allow-list, listed
  through `exportClusterKinds` like `TalosStorage`.
- Not part of this decision: PR mode (design phase 8), strict mode, override, and
  `ichor ui` port-forwarding.
