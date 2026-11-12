# 0013. CNI presets: the interface, custom's contract, Hubble's certificates, and where the guards live

Status: Accepted; superseded in part by ADR 0099 · Date: 2026-10-25 · Design section: CNI

Decided: Settled the `cni.Preset` interface, `custom`'s required fields, Hubble's certificate handling, and where the two immutability guards live

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
	ArgoCDApplication(kubeProxy KubeProxyMode) (*gitops.Application, error)
	SupportsKubeProxyReplacement() bool
	CheckConsistency(ctx context.Context, live LiveConfig, kubeProxy KubeProxyMode) error
	HandoverWorkloads() []WorkloadRef
}
```

No extra methods, no constructor argument beyond what each preset needs
internally. `TalosPatches` returns `TalosConfigPatchDocument` directly --
the same type a cluster-layer `TalosConfigPatch` already carries -- so a
future caller applies it exactly the way `pkg/render` already applies any
other patch, instead of inventing a second patch shape.

**`ArgoCDApplication` returns `*gitops.Application`** (ADR 0019's `ichor init`
work), not `*unstructured.Unstructured`. Pulling in `argoproj/argo-cd`'s Go
module for one struct's field names is a dependency for a few lines, and
`unstructured.Unstructured` is already a transitive one, but a caller has to
*use* the result: `ichor init --gitops-dir` writes every Argo CD Application it
builds through `pkg/gitops.Skeleton`, and `pkg/gitops` is where the actual
protections against writing a credential into Git live -- the `RepoURL`
userinfo check (twice: once in `gitops.New`, once again in
`Skeleton.Write` for an `Application` that reached it some other way) and
the refusal to write a `*corev1.Secret`. An `*unstructured.Unstructured`
carries none of that; a caller handed one has to re-derive whatever
`pkg/gitops` already guarantees, or (worse) skip it. `pkg/cni` therefore
depends on `pkg/gitops`, which is the right direction: the package that
builds an Argo CD Application for a specific CNI depends on the package that
models what an Argo CD Application is, not the other way around.
`pkg/gitops` carries `ApplicationSourceHelm.Values` (`spec.source.helm.values`
per argo-cd.readthedocs.io's Helm page) for this: Cilium's rendered values
document goes there.

`flannel.Preset` is the zero-value proof that "Ichor does nothing" is
literal: every method either returns `nil, nil` or, where the design implies
a hard "no" (kube-proxy replacement), an error. There is nothing to
configure, so there is no constructor argument and no state.

`cilium.Preset` renders Helm values, not Helm's expanded chart templates.
Actually running Helm -- fetching `cilium/cilium` and expanding its
templates -- needs a network call and is bootstrap wiring for `ichor init`,
which calls this package.
What `BootstrapManifest` and `ArgoCDApplication` both render today is the
resolved values document the chart would be installed with
(`pkg/cni/cilium/values.go`), plus the chart's coordinates
(`helm.cilium.io`, chart `cilium`, version `1.18.0` -- pinned to the version
the sourced Talos values below were written against). That is the "declare values and chart coordinates, and be able to render"
boundary of this phase.

**Sources** for the values and names, all of them Ichor dependencies:

- Talos's recommended Cilium values -- `ipam.mode: kubernetes`,
  `kubeProxyReplacement`, `k8sServiceHost: localhost` /
  `k8sServicePort: 7445` (KubePrism) for the kube-proxy-free mode, the
  `securityContext.capabilities` lists for `ciliumAgent` and
  `cleanCiliumState`, and `cgroup.autoMount.enabled: false` /
  `cgroup.hostRoot: /sys/fs/cgroup` -- are from
  `docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium`, the Talos
  v1.14 docs referencing Cilium 1.18.0.
- `hubble.tls.auto.method: cronJob` (see below) is from
  `docs.cilium.io/en/stable/helm-reference/` (the stable reference, ~1.20.4).
- `ServerSideApply=true` as the large-CRD-set sync option, and the
  `source`/`destination`/`syncPolicy.syncOptions` Application shape, are
  from `argo-cd.readthedocs.io`'s sync-options and
  `operator-manual/application.yaml` pages.
- The Cilium agent/operator workload names (`cilium` DaemonSet,
  `cilium-operator` Deployment, both in `kube-system`) returned by
  `HandoverWorkloads` are Cilium's own standard install layout, per `docs.cilium.io`'s Helm install
  docs.

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
needs yet would be the exact kind of extension point that is not worth adding
before a caller needs it.

## Decision: Hubble's certificates

> Update (ADR 0055): re-checked against the Cilium v1.20.0 chart; `cronJob` kept, and the Application also ignores `CiliumIdentity`.

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
patch/target discrimination rules) -- not a webhook, because a rule that
only ever compares two fields on the same object needs nothing a webhook
would add. Changing `preset` on an existing `TalosCluster` is rejected; every
other update (including one that changes something else entirely) is accepted.

**Switching `kubeProxy` needs the
`ichor.maesh.dev/confirm-kube-proxy-switch` annotation.** This one could
not be a CRD-embedded rule, which the `preset` rule above is. CRD
`x-kubernetes-validations` only exposes `metadata.name` and
`metadata.generateName` on `self`/`oldSelf`: a rule written as
`self.metadata.annotations` fails CRD creation outright with
`undefined field 'annotations'`. There is no CRD-schema-only way to gate on an
annotation. The remaining options are a controller-runtime webhook
(`internal/webhook/`) or a `ValidatingAdmissionPolicy`: a built-in, declarative,
CEL-evaluated admission rule (stable since Kubernetes v1.30; this project's
pinned k8s.io/api is v0.36, i.e. envtest 1.36) that evaluates against the
full admission request -- `object`/`oldObject`, metadata included -- rather
than the CRD's own schema. It is still CEL, with no Go admission code.

`config/admission/kubeproxy-switch-policy.yaml` carries the
`ValidatingAdmissionPolicy` and its `ValidatingAdmissionPolicyBinding`,
matching `UPDATE`s to `talosclusters.ichor.maesh.dev` and denying any
update that changes `spec.cni.kubeProxy` unless the annotation is present.
It is wired into `config/default/kustomization.yaml`, so a kustomize-based
deploy installs it, and mirrored -- identical below the
`apiVersion`/`kind`/`metadata.name` lines -- into
`dist/chart/templates/admission/kubeproxy-switch-policy.yaml`, gated by a
new `admission.enabled` value (default `true`, independent of
`webhook.enabled`: this guard is enforced by the apiserver itself, not the
controller-runtime webhook server, so it still holds while the controller
is down). `dist/chart/chart_test.go`'s `checkKubeProxySwitchPolicy` guards
both halves the same way the existing checks guard the webhook
configuration: it fails if either the `ValidatingAdmissionPolicy` or its
`ValidatingAdmissionPolicyBinding` is missing from the rendered chart, and
fails if the Binding's `spec.policyName` doesn't name the Policy this chart
ships -- a Policy with no Binding, or a Binding naming the wrong Policy,
guards nothing. `api/v1alpha1/suite_test.go` also loads
`config/admission/kubeproxy-switch-policy.yaml` directly (not a copy) before
the CNI specs run, so the kustomize path is covered too.

Because a cluster below Kubernetes 1.30 would otherwise install this chart
successfully with the guard silently absent, `dist/chart/Chart.yaml` now
declares `kubeVersion: ">=1.30.0-0"` -- `ValidatingAdmissionPolicy` is
stable, enabled by default, from exactly that version
(kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/),
so an older cluster fails the install outright
instead of silently shipping without the guard.

**The annotation unblocks the API-level guard; it does not run the safe
switch.** `ichor.maesh.dev/confirm-kube-proxy-switch` only lets the
`kubeProxy` update through admission -- nothing in this decision brings the
new side up on every node, verifies it, and then turns the old side off, which
is the one safe order the design requires once a switch is allowed. That is
the reconciler's job: this decision declares the hand-over workloads and the
consistency check, not the switch. Until that exists, adding the annotation and flipping `kubeProxy` only clears the
admission guard; actually carrying out the switch safely is a manual,
unassisted operation. Do not read the annotation's existence as a
statement that switching is safe to automate yet.

## Consequences

- Adding a CNI (`calico`, later) means writing one `pkg/cni/<name>`
  package implementing the six methods, plus whatever `TalosCluster`
  CRD validation its own quirks need -- no interface change.
- `pkg/cni/cilium`'s values are pinned to a Cilium/Talos-doc version pair
  (1.18.0) that will drift from upstream recommendations over time; bumping
  it is a values.go change plus re-reading the same two source pages, not a
  structural one.
- The kube-proxy-switch guard now ships identically through both install
  paths -- `make deploy`/`kustomize build config/default` and the Helm
  chart -- and `dist/chart/chart_test.go` fails loudly if the two drift
  again, the same way it already does for the CRDs.
- A chart install against a pre-1.30 Kubernetes now fails outright
  (`kubeVersion`), rather than silently shipping without the guard.
- The annotation is a necessary, not sufficient, condition for a safe
  `kubeProxy` switch: it only clears admission. Nothing executes the
  design's required new-side-up/verify/old-side-down sequence yet, so a
  switch today is a manual operation even once the annotation is set.

- Superseded in part by ADR 0066: the new-side-up, verify, old-side-down sequence is now executed by a `TalosOperation` of kind `cni`.
