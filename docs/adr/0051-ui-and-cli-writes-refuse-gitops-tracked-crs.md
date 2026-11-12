# 0051. UI and CLI writes refuse a CR that Argo CD or Flux tracks

Status: Superseded by ADR 0101 · Date: 2026-10-26 · Design section: GitOps and Argo CD
Decided: Every write to an existing CR in `internal/uiapi` (so the UI and the CLI, backend or fallback) goes through one `Server.requireUntracked`, which runs `gitopstracking.IsTracked` and `prmode.Decide(trackedBy, false)` and refuses anything but `ModeDirectWrite` with `FailedPrecondition`; a failed check refuses too; `Create` is not gated; the RBAC is two cluster-scoped rules, one narrowed by `resourceNames`

## Context

`pkg/gitopstracking` and `pkg/prmode` (ADR 0037, 0038, 0044) were complete but nothing imported them, so the
backend wrote CRs that Argo CD or Flux owns. Architecture: a tracked CR without a repository connection is
read-only with an export button. No repository connection exists in the CRDs yet, so for now every tracked CR is
read-only; opening the PR for `ModeOpenPR` is not built.

## Decisions

**1. One gate.** `Server.requireUntracked(ctx, obj)` in `internal/uiapi/server.go`, called right after the
object is read in `ApproveMachine`, `RejectMachine`, `UpdatePatch` and `DeletePatch` (the only
`Update`/`Delete` sites). It passes `hasRepoConnection=false`; any mode other than `ModeDirectWrite`
(including a `ModeOpenPR` that cannot occur yet) is refused. The CLI fallback builds the same `Server`, so it is
covered without extra code.

**2. Refusal.** `connect.CodeFailedPrecondition`: `"<ns/name>" is tracked by Argo CD|Flux, so it is read-only
here: run `ichor gitops export` to put it in Git and change it with a pull request`.

**3. A failed check refuses.** An `IsTracked` error returns `CodeUnavailable` and writes nothing: "cannot tell"
is never "may write" (ADR 0044).

**4. `Create` is not gated.** A CR that does not exist has no Argo CD annotation or label, no Flux field
manager and no inventory entry, so `IsTracked` can only answer "not tracked". Whether the new CR lands in a path
or namespace a tool will later own is not knowable from the cluster; that belongs to PR mode, which
decides where a new file goes. Creating the CR directly is the same as `kubectl apply` today.

**5. RBAC.** Two rules added to the UI ClusterRole (`rbac_markers.go`, `config/rbac/ichor-ui/`, the chart):
`get` on `configmaps` with `resourceNames: [argocd-cm]`, and `list` on
`kustomizations.kustomize.toolkit.fluxcd.io`. They are cluster-scoped because Argo CD's namespace is not fixed
and a Role/RoleBinding into a namespace that does not exist fails the chart install (ADR 0050 made the same
call for Longhorn). `resourceNames` keeps the ConfigMap read to one name, so there is no cluster-wide ConfigMap
or Secret read. The namespace comes from `Server.ArgoCDNamespace` (`ICHOR_ARGOCD_NAMESPACE` in `ichor-ui`),
default `argocd`.

## Consequences

- The namespace is configurable end to end: the chart sets `ichorUI.argocdNamespace` (default `argocd`) as
  `ICHOR_ARGOCD_NAMESPACE` on the `ichor-ui` Deployment, and the CLI's `--argocd-namespace` (default `argocd`,
  the same flag `ichor gitops export` has) sets the namespace the `--local` fallback reads. Before this, a
  non-default Argo CD namespace silently let a tracked CR be written directly.
- Flux has no PR path (ADR 0037), so a Flux-tracked CR stays read-only even once a repository connection exists.
