# 0055. Cilium stays Synced under Argo CD with ignoreDifferences on CiliumIdentity and ServerSideApply

Status: Accepted (amended 2026-10-26); superseded in part by ADR 0099 · Date: 2026-10-26 · Design section: CNI, GitOps and Argo CD

Decided: The generated Cilium Application carries `ignoreDifferences: [{group: cilium.io, kind: CiliumIdentity}]` and `ServerSideApply=true`, without `RespectIgnoreDifferences=true`; Hubble certificates stay on `hubble.tls.auto.method=cronJob`; Kokumi cannot carry either setting today, so that case is an open question; the generated Cilium and root Applications use automated sync with prune and selfHeal (addendum 2026-10-26)

## Context

The design's last open question was which Cilium and Calico values avoid a
permanent OutOfSync under Argo CD. The answer for Cilium is `ignoreDifferences`
with the single entry `{group: cilium.io, kind: CiliumIdentity}` plus
`syncOptions: [ServerSideApply=true]` on the generated Application, over a
deliberately narrow set of Cilium values: the ones the Talos Cilium guide
requires (`ipam.mode`, `kubeProxyReplacement`, `securityContext.capabilities`,
`cgroup`, `k8sServiceHost`/`k8sServicePort`) and ADR 0013's choices, and nothing
more. Gateway API, L2 announcements, BGP and Hubble relay/UI are not managed by
Ichor; a user who wants them adds them in their own values.

## Decision

**Argo CD on its own** (Ichor writes the Application, `pkg/cni/cilium`): the
Application gets the one `ignoreDifferences` entry and keeps
`ServerSideApply=true` and `CreateNamespace=true`. Release name (`cilium`),
namespace (`kube-system`) and values are unchanged from the bootstrap
manifest, so ADR 0024's no-diff hand-over still holds: the two are byte for
byte equal.

**`RespectIgnoreDifferences=true` is not added.** Argo CD's sync-options
documentation (argo-cd.readthedocs.io, stable; the page names no version) says `ignoreDifferences` is by default used only to compute sync
status, and that the option makes a sync pre-patch the *desired* state so
ignored fields are not applied over live ones; it is only effective when the
resource already exists. `CiliumIdentity` objects are created by the Cilium
operator and appear nowhere in the chart (no template under
`install/kubernetes/cilium/templates` at tag v1.20.0 mentions the kind), so
there is no desired manifest to pre-patch. `gitops.New` therefore adds the option only when an
entry names fields (`jsonPointers`, `jqPathExpressions` or
`managedFieldsManagers`), which is the case ADR 0015 introduced the pairing
for. A kind-only entry gets no option. Still one constructor.

**Hubble certificates stay `cronJob`.** Read in the Cilium v1.20.0 chart:
`hubble.tls.auto.method` defaults to `helm`. The `tls-helm` templates call
`genCA`/`genSignedCert` on every render, try to reuse existing material through
`lookup` (which `helm template`, what Argo CD runs, cannot do because it has no
cluster access), and label each Secret `cilium.io/helm-template-non-idempotent:
"true"`. So under Argo CD a re-render yields new certificates, unless Argo CD's
repo-server cache hides the re-render until a new revision or hard refresh.
`cronJob` renders no random material (the Job and CronJob generate certificates in the cluster), so
the diff cannot arise and the behaviour does not depend on cache timing. It is
also the same value in the bootstrap manifest and the Application.

**Values Ichor manages.** All five Talos essentials are present:
`ipam.mode: kubernetes`; `kubeProxyReplacement`; `securityContext.capabilities`
(`ciliumAgent`, `cleanCiliumState`); `cgroup.autoMount.enabled: false` and
`hostRoot: /sys/fs/cgroup`; `k8sServiceHost: localhost` and
`k8sServicePort: 7445` (these two only when kube-proxy is replaced, as in
Talos's guide). Nothing else is added: not `gatewayAPI`, `l2announcements`,
`bgpControlPlane` or Hubble relay/UI/metrics, which are features of a user's own
cluster rather than of the install Ichor owns.

**Chart version.** Ichor pins `1.18.0` (`ChartVersion`). The chart-template
facts above were read at v1.20.0. The pin is not bumped here: ADR 0013 ties a
bump to a review of the values against the new chart.

**Caveat.** The Argo CD docs for `ignoreDifferences` (diffing guide) do not
explain mechanically how a group/kind-only entry affects the result; every
documented example names a field, and the page does not say what a kind-only
entry does. `CiliumIdentity` objects are created by the operator, are
cluster-scoped and appear nowhere in the chart, so Argo CD is expected to stay
Synced with or without the entry. It is kept as a harmless precaution against
operator-owned objects entering the diff, not because a derived mechanism
requires it.

**Project.** The Application names the `default` AppProject. Argo CD's
`core-install.yaml` does not create one, and the Application reports
`InvalidSpecError` until the user does.

**Tunnel protocol.** Some networks drop VXLAN (UDP 8472) between nodes,
including the outer cluster Ichor's end-to-end harness runs on. With Cilium's
default tunnel, pod-to-pod traffic between nodes then fails: cluster DNS times
out and Argo CD's repo-server resolves nothing. `tunnelProtocol: geneve` fixes
it, which is why the tunnel protocol is selectable (ADR 0098); the same value
goes in both the bootstrap install and the Application.

## Argo CD alone versus Argo CD with Kokumi

| | Argo CD alone | Argo CD with Kokumi (ADR 0048, design only) |
|---|---|---|
| Who writes the Application | Ichor (`pkg/cni/cilium`) | Kokumi's Serving controller |
| `ServerSideApply=true` | Ichor sets it | Kokumi already sets it (hard-coded) |
| `ignoreDifferences` | Ichor sets it on the Application | No field exists to pass it |
| `RespectIgnoreDifferences` | not needed | not needed, same reason |
| Where the ignore can live instead | n/a | `argocd-cm` `resource.customizations.ignoreDifferences.cilium.io_CiliumIdentity`, shipped as a user-merged ConfigMap like ADR 0046 |
| Status | implemented | open question |

Source for the Kokumi column: `kokumi-dev/kokumi` at commit 4126144 (2026-10-21):
`internal/argocd/application.go` builds the Application as a literal with
`syncPolicy.automated {prune, selfHeal: true}` and `syncOptions:
["ServerSideApply=true"]` and no `ignoreDifferences`; no `api/v1alpha1` type
carries Application fields; `applyApplication` in
`internal/controller/serving_controller.go` compares the whole `spec` and
updates the Application when it differs, so an edit to the Application is
reverted. kokumi.dev's overview page says nothing on the subject. This is a
reading of the source of an experimental `v1alpha1` project, not a statement of
its contract. The `CiliumIdentity` problem exists with Kokumi too: Argo CD
compares the artifact's rendered manifests with live regardless of who made the
Application.

What is not decided: whether the `argocd-cm` route works. Its documented form
(`resource.customizations.ignoreDifferences.<group>_<kind>`) takes
`jsonPointers`, `jqPathExpressions` or `managedFieldsManagers`, and a
kind-only entry is not documented there, so the equivalent of the Application
entry is unknown. Ichor does not write `argocd-cm` (ADR 0046).

## Calico

Calico is phase 11. Its values are decided when it is built.

## Consequences

- `gitops.New` no longer adds `RespectIgnoreDifferences=true` for kind-only
  entries (ADR 0015 amended).
- The design's Open questions shrinks to Calico and the Kokumi case.

## Addendum (2026-10-26): generated Applications sync automatically

With Argo CD in use, automated sync is fine; without Argo CD this does not
apply. An Application without `syncPolicy.automated` stays OutOfSync until
someone syncs it, so the CNI hand-over (ADR 0024) never completes. Before its
first sync the Application differs from live in exactly one way: Argo CD's
`argocd.argoproj.io/tracking-id` annotation on every resource. The hand-over is
a no-diff in content but is not Synced until a sync writes that annotation; the
first sync changes no Cilium workload.

**Distinction.** "Ichor never touches an Application's sync policy" means Ichor
never changes the sync policy of an Application that already exists. An
Application Ichor generates (only on the Argo CD / hand-over path) chooses its
sync policy once, at creation, in `pkg/gitops.New` (ADR 0015).

**Every generated Application** (there are no others; `gitops export` reuses
these two builders, flannel's `ArgoCDApplication` returns nil):

| Application | Built by | Policy |
|---|---|---|
| `cilium` (CNI) | `pkg/cni/cilium` | `automated {prune: true, selfHeal: true}`, `ServerSideApply=true`, `CreateNamespace=true`, `PruneLast=true` |
| `root` (app of apps) | `internal/cli.buildRootApplication` | `automated {prune: true, selfHeal: true}` (already so; unchanged) |

**Prune, per Application** (Argo CD v3.5 docs, sync-options and auto_sync):

- CNI: `prune: true`. Prune removes only resources Argo CD tracks that the
  pinned chart no longer renders, i.e. after a deliberate chart or value change.
  `prune: false` would not stop that change; it would leave the app OutOfSync
  ("the app will be out of sync if Argo CD expects a resource to be pruned"),
  which blocks the Synced + Healthy signal the hand-over waits for. The risk
  that a dropped resource takes the network down is reduced by `PruneLast=true`
  (pruning runs as a last implicit wave, after the new resources are healthy).
  A user can protect any single resource with the
  `argocd.argoproj.io/sync-options: Prune=false` annotation, which overrides
  the policy.
- root: `prune: true`. It deletes a child Application only when its manifest is
  removed from Git, which is the intent. Ichor sets no
  `resources-finalizer` on children, so deleting a child Application does not
  cascade-delete the workloads it deployed.

Without `--argocd` (or with `handover: disabled`) no Application is generated and
nothing changes.
