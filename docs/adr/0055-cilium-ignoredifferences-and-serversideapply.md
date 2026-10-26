# 0055. Cilium stays Synced under Argo CD with ignoreDifferences on CiliumIdentity and ServerSideApply

Status: Accepted · Date: 2026-10-26 · Design section: CNI, GitOps and Argo CD

Decided: The generated Cilium Application carries `ignoreDifferences: [{group: cilium.io, kind: CiliumIdentity}]` and `ServerSideApply=true`, without `RespectIgnoreDifferences=true`; Hubble certificates stay on `hubble.tls.auto.method=cronJob`; Kokumi cannot carry either setting today, so that case is an open question

## Context

The design's last open question was which Cilium and Calico values avoid a
permanent OutOfSync under Argo CD. The user chose `ignoreDifferences` and named
a reference: their own homelab Argo CD, read-only on 2026-10-26.

What was observed there (the user's cluster, not a cluster Ichor handed over):
Application `cilium`, Cilium chart `1.20.0`, status Synced with `selfHeal:
true`, `syncOptions: [ServerSideApply=true]`, and `ignoreDifferences` with the
single entry `{group: cilium.io, kind: CiliumIdentity}`. `argocd-cm` held no
Cilium-specific customization. Its values were `ipam.mode: kubernetes`,
`kubeProxyReplacement: true`, `securityContext.capabilities`, `cgroup`,
`k8sServiceHost: localhost`, `k8sServicePort: 7445`, plus homelab-only choices
(Gateway API, L2 announcements, Hubble UI service type and metrics) and no
`hubble.tls.auto.method`.

## Decision

**Argo CD on its own** (Ichor writes the Application, `pkg/cni/cilium`): the
Application gets the one `ignoreDifferences` entry and keeps
`ServerSideApply=true` and `CreateNamespace=true`. Release name (`cilium`),
namespace (`kube-system`) and values are unchanged from the bootstrap
manifest, so ADR 0024's no-diff hand-over still holds; a test now compares the
two byte for byte.

**`RespectIgnoreDifferences=true` is not added.** Argo CD's sync-options
documentation (argo-cd.readthedocs.io, stable, read 2026-10-26; the page names
no version) says `ignoreDifferences` is by default used only to compute sync
status, and that the option makes a sync pre-patch the *desired* state so
ignored fields are not applied over live ones; it is only effective when the
resource already exists. `CiliumIdentity` objects are created by the Cilium
operator and appear nowhere in the chart (no template under
`install/kubernetes/cilium/templates` at tag v1.20.0 mentions the kind), so
there is no desired manifest to pre-patch. The reference has no such option and
is Synced. `gitops.New` therefore changed: it adds the option only when an
entry names fields (`jsonPointers`, `jqPathExpressions` or
`managedFieldsManagers`), which is the case ADR 0015 introduced the pairing
for. A kind-only entry gets no option. Still one constructor.

**Hubble certificates stay `cronJob`.** Read in the Cilium v1.20.0 chart:
`hubble.tls.auto.method` defaults to `helm`. The `tls-helm` templates call
`genCA`/`genSignedCert` on every render, try to reuse existing material through
`lookup` (which `helm template`, what Argo CD runs, cannot do because it has no
cluster access), and label each Secret `cilium.io/helm-template-non-idempotent:
"true"`. So under Argo CD a re-render yields new certificates. The reference
runs the default and is Synced; the likely reason is that Argo CD's repo-server
caches rendered manifests per revision, so a re-render only happens on a new
revision or hard refresh. That reason was not tested. `cronJob` renders no
random material (the Job and CronJob generate certificates in the cluster), so
the diff cannot arise and the behaviour does not depend on cache timing. It is
also the same value in the bootstrap manifest and the Application. Kept.

**Values compared with the reference.** All five Talos essentials are already
present, with identical content: `ipam.mode: kubernetes`;
`kubeProxyReplacement`; `securityContext.capabilities` (`ciliumAgent`,
`cleanCiliumState`); `cgroup.autoMount.enabled: false` and `hostRoot:
/sys/fs/cgroup`; `k8sServiceHost: localhost` and `k8sServicePort: 7445`
(these two only when kube-proxy is replaced, as in Talos's guide). Nothing was
added. Not added: `gatewayAPI`, `l2announcements`, `bgpControlPlane`, Hubble
relay/UI/metrics, which are homelab choices.

**Chart version.** The reference runs 1.20.0; Ichor pins `1.18.0`
(`ChartVersion`). The observation is of a newer chart than the one Ichor
installs. The pin is not bumped here: ADR 0013 ties the bump to a review of the
values against the new chart. Whether the entry behaves the same on 1.18.0 is
checked in the e2e run.

**Caveat.** Neither the Argo CD docs for `ignoreDifferences` (diffing guide,
read 2026-10-26) nor the observation explain mechanically how a group/kind-only
entry affects the result; every documented example names a field, and the page
does not say what a kind-only entry does. The decision rests on the observed
Synced state, not on a derived mechanism. Ichor's own confirmation is the e2e
on a cluster Ichor handed over; until then this is a copy of what worked
elsewhere.

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

There is no reference. Calico is phase 11. Its values are decided when it is
built, the same way: observe a working Argo CD-managed Calico.

## Consequences

- `gitops.New` no longer adds `RespectIgnoreDifferences=true` for kind-only
  entries (ADR 0015 amended).
- The design's Open questions shrinks to Calico and the Kokumi case.
- Chart 1.18.0 versus the 1.20.0 observation is an explicit gap for e2e.
