# 0046. Argo CD health checks ship as an opt-in ConfigMap shaped like argocd-cm keys

Status: Superseded by ADR 0101 · Date: 2026-10-26 · Design section: GitOps and Argo CD
Decided: Ship Lua health checks for TalosCluster and TalosMachine only, as a chart ConfigMap (`argocdHealth.enabled`, default false) whose keys are exactly `argocd-cm`'s `resource.customizations.health.<group>_<kind>` keys, tested by running the Lua under lua 5.1

## Context

Argo CD cannot judge the health of a kind it does not know, so the moment
`ichor gitops export` (ADR 0028) puts Ichor CRs in Git, every one shows as
healthy-by-default or stuck Progressing. The design requires the chart to ship
the checks; none existed.

## Decision

**Which kinds.** ADR 0028's allow-list is TalosCluster, TalosMachine,
TalosConfigPatch, TalosStorage. Only the first two get a script:

- `TalosOperation` is never exported, so it needs none.
- `TalosConfigPatch` has a `status` type but no controller writes it; a check
  would report on fields that are always empty.
- `TalosStorage` has no dedicated controller, so its status is never updated.
  Write its check when that controller lands (and its conditions exist to read).
  Without a script Argo CD's default (Healthy once it exists) applies.

**What each reads** (only what `internal/controller` writes):

- TalosMachine: `status.conditions[Ready]` and `status.observedGeneration`.
  Deleting, no Ready yet, or a stale generation is Progressing. Ready=True is
  Healthy. Ready=False with reason `ClusterLockHeld` or `ClusterTransitioning`
  is a wait (Progressing); any other reason (ApplyFailed, RebootRequired,
  NodeDrifted, ...) is Degraded.
- TalosCluster: no Ready condition exists. `Transitioning=True` (an even
  control-plane count) is Progressing; everything else is Healthy.
  `CNIHandover=False` is a normal state (Disabled, ManualPending) and is ignored.
  Per-node health lives on the TalosMachines.

**Delivery path.** Argo CD reads custom health from `argocd-cm` keys
`resource.customizations.health.<group>_<kind>` (Argo CD operator manual,
"Resource Health" > "Custom Health Checks"; the value is Lua reading global
`obj` and returning `status`/`message`). Ichor does not install or edit Argo CD
(the user owns `argocd-cm`), so the chart cannot write that ConfigMap. The
alternative, a PR to Argo CD's bundled `resource_customizations`, is upstream
work outside this repo. So the chart renders a separate ConfigMap
`ichor-argocd-health` with exactly those keys as its data; the user merges
them into their own `argocd-cm` (kustomize patch, Helm values of the Argo CD
chart, or `kubectl get -o jsonpath`). Same opt-in shape as `metricsCert.enabled`:
default false, renders nothing.

## Verification

`dist/chart/argocd_health_test.go` loads each `.lua` with `lua` (5.1, added to
`flake.nix` as `lua5_1`; Argo CD embeds gopher-lua, a 5.1 dialect) against a
table of status samples and expected verdicts, and checks the option renders
nothing when off and the two keys, byte-identical to the files, when on.
The scripts use only the base language (no standard libraries beyond what Argo
CD enables by default).

## Consequences

- The test needs `lua` on PATH (provided by `nix develop`).
- gopher-lua is close to but not identical with PUC Lua 5.1; the scripts stay
  to tables, `ipairs` and string concatenation to avoid the differences.
