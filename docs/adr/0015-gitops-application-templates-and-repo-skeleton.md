# 0015. GitOps: a hand-rolled Application type, and a policy-free repo skeleton

Status: Superseded by ADR 0101 · Date: 2026-10-25 · Design section: "GitOps and Argo CD" (and "Bootstrap")

Decided: Built GitOps `Application` as a hand-rolled type (`pkg/gitops`) behind one constructor, plus a policy-free repo skeleton

## Context

Phase 4 needed two things out of `pkg/gitops`: a way to build Argo CD
`Application` manifests that already carry the fixes the design notes call
out (server-side apply for large CRD groups, `ignoreDifferences` keyed by
`managedFieldsManagers`, sync-wave ordering), and a way to write the
repository skeleton `ichor gitops export` lays out (`bootstrap/`, `apps/`,
`clusters/<name>/`). This package gives `ichor init` and `ichor gitops export`
something to call.

### Should Argo CD's own Go types be a dependency?

Before writing a struct, check whether a dependency already provides it. Argo CD
publishes its `Application` CRD type at
`github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1`, but it cannot be
imported as a library:

```sh
go get github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1@latest
```

`go get` fails outright. The package imports `util/git` and `util/exec`, which
pull in `github.com/argoproj/argo-cd/gitops-engine` through a path that has
no `go.mod` at the revision argo-cd's own `go.mod` pins (`go: ... invalid
version: missing .../gitops-engine/go.mod at revision 97ad5b59a627`) — a
replace-directive-shaped dependency that only resolves when building argo-cd
itself, not when importing it as a library. Even granting that this could be
worked around (an explicit `replace`, or an older release), the same import
chain also pulls in `go-git`, an SSH agent client (`xanzy/ssh-agent`), a
Redis cache client, Google Cloud SDK packages, and more — none of which
`pkg/gitops` has any use for, since it never shells out to git or talks to a
cluster.

**Decision: hand-roll the subset of the schema this package needs.** It
is small (apiVersion/kind, project, source(s), destination, syncPolicy,
ignoreDifferences) and stable enough to maintain against the public docs
(against `argo-cd.readthedocs.io/en/stable/`, the v3.5.6 docs) without a parallel catalogue drifting the
way `pkg/typedconfig` worried about for machinery. `pkg/gitops/doc.go`
records the specific failure so a future upgrade attempt doesn't have to
re-discover it.

### controller-gen treats any type with embedded TypeMeta and ObjectMeta as a kind

Embedding `metav1.TypeMeta` and `metav1.ObjectMeta` anonymously in `Application`
is the obvious shape for a Kubernetes-ish type, but `make build` would then
silently generate `config/crd/bases/_applications.yaml` — a CRD named
`applications.` with an empty group and an empty version, picked up by
`dist/chart`'s own invariant test. The cause: controller-gen's CRD generator
(`sigs.k8s.io/controller-tools/pkg/crd/gen.go`, `FindKubeKinds`) treats
*any* type in *any* package under `paths="./..."` as a candidate Kubernetes
kind if it has both fields embedded, with no marker required to opt in —
there is no marker to opt *out* either, short of not shaping the type that
way. Since `Application` is never registered with a `runtime.Scheme`, sent
to an API server, or type-asserted as `runtime.Object`, it never needed
`TypeMeta` as a real type in the first place.

**Decision:** `Application` carries plain `APIVersion`/`Kind` string fields
instead of an embedded `metav1.TypeMeta`. `metav1.ObjectMeta` is still
embedded (that alone doesn't trigger the generator — both fields must be
present), which also means Go 1.27's embedded-literal elision applies:
`New` builds one with `Name`, `Namespace` and `Annotations` set directly on
the struct literal, no nested `metav1.ObjectMeta{...}` needed.

## Decision

### `Application`, `Options` and `New` (`pkg/gitops/application.go`)

`New(Options) (*Application, error)` is the one constructor. It bakes in
every pitfall the design notes name, so a caller cannot build a
correctly-shaped-but-broken Application by omission:

- **`ServerSideApply=true`** is added to `syncPolicy.syncOptions` whenever
  `Options.ServerSideApply` is set — for the large CRD groups case (e.g.
  Ichor's own CRDs, or CNI CRDs).
- **`ignoreDifferences` + `managedFieldsManagers`**: any non-empty
  `Options.IgnoreDifferences` is written through as-is, keyed by whatever
  field manager owns the field (the example from the design notes —
  `widget-operator` owning a CRD's `spec.versions` — is exactly
  `ResourceIgnoreDifference{..., ManagedFieldsManagers: []string{"widget-operator"}}`).
  (ADR 0055 narrows this: the pairing applies to entries that name fields, not kind-only ones.)
  `New` *always* pairs this with `RespectIgnoreDifferences=true` in
  `syncOptions`, because `ignoreDifferences` alone only suppresses the
  field from the *displayed* diff — without the sync option, Argo CD still
  reconsiders the field a sync target. This pairing is not left to the
  caller to remember.
- **Sync waves**: `Options.SyncWave` becomes the
  `argocd.argoproj.io/sync-wave` annotation. The exported constants
  `SyncWaveArgoCD < SyncWaveCNI < SyncWaveIchor < SyncWaveIchorCRs <
  SyncWaveStorage < SyncWaveOther` are the design's documented hand-over
  order ("Argo CD → CNI → Ichor (including CRDs) → Ichor CRs → storage
  after `TalosStorage` is healthy → everything else"), as typed values
  instead of a magic number a caller has to get right by reading the prose
  again.
- **Credential leak guard**: `New` parses every source's `RepoURL` and
  rejects one carrying embedded userinfo
  (`https://user:pass@host/repo.git`). The design is explicit that repo
  credentials are a Secret the CLI writes directly, never a value that
  travels through a file in Git — including inside a URL in a tracked
  Application manifest. This is enforced again, independently, at write
  time (next section), so an `Application` built without going through
  `New` is still caught.

`Options` does not force opinions this package has none on:
`ExtraSyncOptions` lets a caller add e.g. `CreateNamespace=true` without
`New` guessing whether a given component wants it.

### `Skeleton` and `Write` (`pkg/gitops/skeleton.go`)

`Skeleton.Write(dir)` lays out exactly the structure the design specifies:

```text
bootstrap/root-app.yaml      <- Skeleton.RootApp
apps/<name>/application.yaml <- one per Skeleton.Apps entry
clusters/<cluster>/<file>    <- one per Skeleton.CRs entry
```

**Which component directories exist under `apps/` is not this package's
decision.** The design says `apps/argocd/`, `apps/cni/` and `apps/ichor/`
are always present, `apps/storage/` and `apps/monitoring/` only when
selected — but *deciding* which components a given cluster has is `ichor
init`/`ichor gitops export`'s job, a separate decision.
`Skeleton.Apps` is a plain `map[string]*Application`; `Write` renders
whatever keys it's given, in sorted order, and no key is privileged.

**Determinism.** Every map on `Skeleton` (`Apps`, `CRs`) is walked through
`sortedKeys`, which sorts before iterating, so the written files never
depend on Go's deliberately-randomized map iteration order. YAML encoding
goes through `sigs.k8s.io/yaml`, which round-trips through
`encoding/json` (whose map-key ordering is already sorted) and then through
go-yaml's own map marshaling (also sorted) — so even values nested inside
`CRs` (e.g. a raw `map[string]any` CR) marshal to the same bytes regardless
of how they were built. `pkg/gitops/skeleton_test.go`'s
`TestSkeleton_Write_Deterministic` writes the same `Skeleton` value
repeatedly (Go's map randomization is probabilistic per process) and diffs
every file byte-for-byte.

**Credentials never reach a file.** Three independent layers, each pinned by a test:

1. **Nothing to put them in.** `Skeleton` and `Options` have no field typed
   to hold a credential or the secrets bundle — there is no parameter a
   caller could thread one through even by mistake.
2. **The URL guard, twice.** `New` checks every source's `RepoURL` before
   building an `Application`; `Write` checks again (`checkApplication`,
   reusing the same logic) on `RootApp` and every entry of `Apps`, in case
   one reached `Skeleton` some other way. Both paths are pinned by
   (`TestNew_RejectsCredentialsInRepoURL`,
   `TestSkeleton_Write_RefusesCredentialedRepoURL`).
3. **A `Secret`, whatever its source, is refused.** `WriteObject` — the
   single function every file in the skeleton goes through — type-switches
   on `*corev1.Secret` and errors before marshaling anything
   (`TestSkeleton_Write_RefusesSecret`). This is where the design's "repo
   credentials go in as a Secret from the CLI, never into Git" lands as
   code: the one object type that *is* a credential-bearing thing by
   definition cannot be written by this package, full stop.

### Boundary with `pkg/cni`

`pkg/cni` owns the CNI preset's own Application
content — which Helm chart, which values, which release name so hand-over
produces no diff. This package provides the mechanism it builds on top of:
`gitops.New` for the Application shape and its baked-in fixes, `SyncWaveCNI`
for where it sits in the hand-over order, and `Skeleton.Apps["cni"]` as
where its output lands in the repo tree. Nothing CNI-specific — not a preset
name, not a chart reference — lives in `pkg/gitops`.

**`pkg/cni.Preset.ArgoCDApplication` returns `*gitops.Application` directly**
(ADR 0019's `ichor init` work), built through `gitops.New`, rather than
`*unstructured.Unstructured` with its own hand-assembled map (ADR 0013 records
the `pkg/cni` side of this). `pkg/cni` depends on `pkg/gitops` as a result —
the direction this ADR's "nothing CNI-specific lives in `pkg/gitops`" sentence
above does not rule out; it is `pkg/gitops` depending on `pkg/cni`, or
CNI-specific content living in `pkg/gitops`, that would be backwards. The
practical effect is that every Application `pkg/cni` builds goes through this
package's own guards (the `RepoURL` credential check, the
`ServerSideApply`/`ignoreDifferences` pairing) instead of a caller having to
notice an `unstructured.Unstructured` carries none of them, and
`ichor init --gitops-dir` writes `pkg/cni/cilium`'s Application straight into
`Skeleton.Apps["cni"]` with no conversion step.

`ApplicationSourceHelm` carries a `Values` field (`spec.source.helm.values`,
inline Helm values as a YAML string — argo-cd.readthedocs.io's Helm page) for
what Cilium's unstructured map previously put at the same path. `ReleaseName`
and `ValueFiles` serve a `--gitops-dir`-authored `ApplicationSource`; `Values`
is for a preset that already rendered its own values document and has nothing
to reference by path.

Cilium generating its Hubble certificates through a CronJob rather than Helm
(so sync doesn't see a diff every time) is not this package's concern. That is
Cilium-preset content, i.e. `pkg/cni`'s manifests (ADR 0013), not anything
about how an `Application` or the repo skeleton is shaped.

## Consequences

- `pkg/gitops` has no dependency on `argoproj/argo-cd`. If a future Argo CD
  release fixes the `gitops-engine` resolution problem, re-evaluate — but
  the heavy transitive imports (`go-git`, cloud SDKs, Redis) would still be
  there, so the bar for switching is high.
- The hand-rolled `Application` type covers the fields this phase needs.
  Adding a field Argo CD supports but this package doesn't yet cover (e.g.
  `revisionHistoryLimit`, `info`) is a small, additive change against the
  public schema — not a redesign.
- Anyone embedding `metav1.TypeMeta` and `metav1.ObjectMeta` together,
  anonymously, anywhere under this repository's module (not just
  `pkg/gitops`) will silently grow a bogus CRD on the next `make build`
  unless they already know to check `config/crd/bases/` for
  surprises. There is no lint rule for this; it is only written down here
  and in `pkg/gitops/application.go`'s comment on `Application`.
- `ichor init --gitops-dir` (`ichor gitops export` is still separate work)
  decides *which* components go in `Skeleton.Apps`: today, just `"cni"`,
  from whatever `pkg/cni.Preset.ArgoCDApplication` returned for the
  cluster's chosen preset. It calls `gitops.New` (via `pkg/cni`) and
  `Skeleton.Write` rather than reimplementing either.

## Amendment: one root Application over apps/ and the cluster CRs

The skeleton has two properties against Argo CD v3.5.6:

- The root Application is `path: .` with `directory.recurse: true` and
  `directory.include: {apps/*/application.yaml,clusters/<name>/*.yaml}`
  (`ApplicationSourceDir.Include`), so the `apps/*/application.yaml` files (the
  CNI hand-over Application) are applied together with the cluster's CRs. A
  root with `path: clusters/<name>` never applies `apps/`. One Application, no
  extra file; Ichor's CRs and the apps stay separate by directory, and
  `bootstrap/root-app.yaml` and other clusters' directories are not synced. A
  second app-of-apps for `apps/` was rejected as one more file and one more
  sync to get wrong.
- `ichor init --gitops-dir` writes declared-form CRs, not the live typed
  objects (no `apiVersion`/`kind`, with `managedFields`, `uid`, ...), which sync
  zero resources. Init passes its CRs through `declareCRs`, the same function
  `ichor gitops export` uses, so both write identical declared-form files and
  the same names (`talos-machine-<name>.yaml`, `talos-config-patch-<name>.yaml`).
