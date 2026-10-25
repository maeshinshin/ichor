# 0013. CNI presets: the interface, custom's contract, Hubble's certificates, and where the guards live

Status: Accepted · Date: 2026-10-25 · Design section: CNI

## Context

The design names six responsibilities a CNI preset carries and says a new
CNI is a new implementation of that interface and nothing else: Talos-side
patches, manifest rendering for the bootstrap, an Argo CD Application
template, whether it can replace kube-proxy, a consistency check against the
live configuration, and the workloads that signal a successful hand-over.
Three things needed deciding to build it: the interface's exact shape, what
`custom` makes the user declare, and where two "refuse this" rules actually
live, since the obvious place for one of them does not work.

## Decision: the interface

`pkg/cni.Preset` has exactly the six methods the design names, one per
responsibility, taking `KubeProxyMode` ("enabled" or "replaced") where the
mode changes the answer:

```go
type Preset interface {
	TalosPatches(kubeProxy KubeProxyMode) ([]ichorv1alpha1.TalosConfigPatchDocument, error)
	BootstrapManifest(kubeProxy KubeProxyMode) ([]byte, error)
	ArgoCDApplication(kubeProxy KubeProxyMode) (*unstructured.Unstructured, error)
	SupportsKubeProxyReplacement() bool
	CheckConsistency(ctx context.Context, live LiveConfig, kubeProxy KubeProxyMode) error
	HandoverWorkloads() []WorkloadRef
}
```

No extra methods, no constructor argument beyond what each preset needs
internally. `TalosPatches` returns `TalosConfigPatchDocument` directly --
the same type a cluster-layer `TalosConfigPatch` already carries -- so a
future caller applies it exactly the way `pkg/render` already applies any
other patch, instead of inventing a second patch shape. `ArgoCDApplication`
returns `*unstructured.Unstructured` rather than a typed Argo CD
`Application`: pulling in `argoproj/argo-cd`'s Go module for one struct's
field names is a dependency for a few lines (CONTRIBUTING.md's chart
warning is a reminder of how easily a vendored shape drifts from what's
actually installed); `unstructured.Unstructured`, already a transitive
dependency, says exactly as much and no more.

`flannel.Preset` is the zero-value proof that "Ichor does nothing" is
literal: every method either returns `nil, nil` or, where the design implies
a hard "no" (kube-proxy replacement), an error. There is nothing to
configure, so there is no constructor argument and no state.

`cilium.Preset` renders Helm values, not Helm's expanded chart templates.
Actually running Helm -- fetching `cilium/cilium` and expanding its
templates -- needs a network call and is bootstrap wiring for the unit that
calls this package (`ichor init`, explicitly out of this change's scope).
What `BootstrapManifest` and `ArgoCDApplication` both render today is the
resolved values document the chart would be installed with
(`pkg/cni/cilium/values.go`), plus the chart's coordinates
(`helm.cilium.io`, chart `cilium`, version `1.18.0` -- pinned to the version
the sourced Talos values below were written against). That is honest about
what this phase delivers and is exactly the "declare values and chart
coordinates, and be able to render" boundary the unit was scoped to.

**Sources, not memory**, since both are Ichor dependencies this change
touches directly:

- Talos's recommended Cilium values -- `ipam.mode: kubernetes`,
  `kubeProxyReplacement`, `k8sServiceHost: localhost` /
  `k8sServicePort: 7445` (KubePrism) for the kube-proxy-free mode, the
  `securityContext.capabilities` lists for `ciliumAgent` and
  `cleanCiliumState`, and `cgroup.autoMount.enabled: false` /
  `cgroup.hostRoot: /sys/fs/cgroup` -- are from
  `docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium`, the Talos
  v1.14 docs referencing Cilium 1.18.0, retrieved 2026-10-25.
- `hubble.tls.auto.method: cronJob` (see below) is from
  `docs.cilium.io/en/stable/helm-reference/`, retrieved 2026-10-25 (current
  stable reference, ~1.20.4).
- `ServerSideApply=true` as the large-CRD-set sync option, and the
  `source`/`destination`/`syncPolicy.syncOptions` Application shape, are
  from `argo-cd.readthedocs.io`'s sync-options and
  `operator-manual/application.yaml` pages, retrieved 2026-10-25.
- The Cilium agent/operator workload names (`cilium` DaemonSet,
  `cilium-operator` Deployment, both in `kube-system`) returned by
  `HandoverWorkloads` are Cilium's own standard install layout, confirmed
  against `docs.cilium.io`'s current Helm install docs, retrieved
  2026-10-25.

No Sidero Omni material was read for any of this.

## Decision: what `custom` makes the user declare

`TalosClusterCNICustom` holds exactly the two things the design says a
`custom` preset cannot infer on its own: `documents`
(`[]TalosConfigPatchDocument`, the Talos-side settings the CNI needs, e.g.
disabling kube-proxy) and `source` (`repoURL`, one of `chart` or `path`, and
`targetRevision` -- the same four names Argo CD's own `ApplicationSource`
uses). There is no `pkg/cni` `Preset` implementation for `custom`: the
design's interface is for CNIs Ichor knows how to render something for.
`custom` is the opposite case -- the user's chart/manifests and the user's
declared patches pass straight through to the reconciler and the Argo CD
Application it builds, with nothing for a `Preset` to compute. Building a
no-op `custom.Preset` that exists only to satisfy an interface no caller
needs yet would be the exact kind of extension point ponytail's ladder says
not to add.

## Decision: Hubble's certificates

Cilium's Helm chart defaults `hubble.tls.auto.method` to `helm`, which
regenerates the mTLS certificate as a Helm hook on every install/upgrade --
and so on every Argo CD sync, which runs `helm template`/`helm upgrade`
repeatedly, means a permanent diff. Setting `hubble.tls.auto.method:
cronJob` is enough: it is a plain Helm value, already inside
`pkg/cni/cilium/values.go`'s `Values.Hubble`, carried into both
`BootstrapManifest`'s rendered values and `ArgoCDApplication`'s
`spec.source.helm.values`. No separate CronJob manifest needed to be
hand-authored; the chart renders its own CronJob once this value is set.

## Decision: where the two "refuse this" rules live

**Preset is immutable.** `TalosClusterCNI`'s own
`+kubebuilder:validation:XValidation:rule="self.preset == oldSelf.preset"`
rejects the change. This is a CRD-embedded CEL rule, like every other
invariant already in `api/v1alpha1` (ADR 0003's allow-list check, the
patch/target discrimination rules) -- not a webhook, both because
`internal/webhook/` was out of this change's scope and because a rule that
only ever compares two fields on the same object needs nothing a webhook
would add. It was generated and confirmed against envtest: changing
`preset` on an existing `TalosCluster` is rejected; every other update
(including one that changes something else entirely) is accepted.

**Switching `kubeProxy` needs the
`ichor.maesh.dev/confirm-kube-proxy-switch` annotation.** This one could
not be a CRD-embedded rule, which the `preset` rule above is. CRD
`x-kubernetes-validations` only exposes `metadata.name` and
`metadata.generateName` on `self`/`oldSelf` -- confirmed the hard way: a
rule written as `self.metadata.annotations` failed CRD creation outright
with `undefined field 'annotations'` against envtest's real apiserver, not
a lint warning. There is no CRD-schema-only way to gate on an annotation.
The remaining options were a controller-runtime webhook
(`internal/webhook/`, explicitly out of this change's scope -- another unit
owns it) or a `ValidatingAdmissionPolicy`: a built-in, declarative,
CEL-evaluated admission rule (stable since Kubernetes v1.30; this project's
pinned k8s.io/api is v0.36, i.e. envtest 1.36) that evaluates against the
full admission request -- `object`/`oldObject`, metadata included -- rather
than the CRD's own schema. It is still CEL, still no Go admission code, and
it does not touch `internal/webhook/`.

`config/admission/kubeproxy-switch-policy.yaml` carries the
`ValidatingAdmissionPolicy` and its `ValidatingAdmissionPolicyBinding`,
matching `UPDATE`s to `talosclusters.ichor.maesh.dev` and denying any
update that changes `spec.cni.kubeProxy` unless the annotation is present.
It is wired into `config/default/kustomization.yaml`, so a kustomize-based
deploy installs it; `api/v1alpha1/suite_test.go` loads the exact same file
(not a copy) before the CNI specs run, so the test and a real deploy enforce
the same rule. **It is not yet mirrored into `dist/chart`.** Doing that
properly needs a values toggle and `_helpers.tpl` namespace templating the
same way the existing hand-edited chart files do, which is packaging work
for the Helm chart, not CNI-preset work -- flagged here rather than rushed,
since a wrong half of it (installed via kustomize, silently absent from a
Helm install) is worse than an acknowledged gap. Follow-up: add it to
`dist/chart/templates/`, covered by a drift check the way
`dist/chart/chart_test.go` already covers the CRDs.

The annotation unblocks the API-level guard only. Running the switch in the
one safe order -- new side up on every node, verify, then old side down --
is the reconciler's job, which is out of this change's scope (the
acceptance bar here is "declare the hand-over workloads and the
consistency check," not "execute the switch").

## Consequences

- Adding a CNI (`calico`, later) means writing one `pkg/cni/<name>`
  package implementing the six methods, plus whatever `TalosCluster`
  CRD validation its own quirks need -- no interface change.
- `pkg/cni/cilium`'s values are pinned to a Cilium/Talos-doc version pair
  (1.18.0) that will drift from upstream recommendations over time; bumping
  it is a values.go change plus re-reading the same two source pages, not a
  structural one.
- The `ValidatingAdmissionPolicy`'s absence from `dist/chart` is a known,
  tracked gap (above), not a silent one: a cluster installed through the
  Helm chart today does not enforce the kube-proxy-switch guard; one
  installed through `make deploy`/`kustomize build config/default` does.
