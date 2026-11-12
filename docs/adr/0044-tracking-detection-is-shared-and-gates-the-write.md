# 0044. Tracking detection moves to `pkg/gitopstracking`; `pkg/prmode.Decide` turns it into a write-vs-PR gate

Status: Superseded by ADR 0101 · Date: 2026-10-26 · Design section: GitOps and Argo CD ("PR mode")
Decided: moved `isTracked` from `internal/cli` to a new `pkg/gitopstracking` package with Flux detection corrected to an inventory scan (the field manager is only a first signal; `flux-client-side-apply` was never Flux's), and added a pure `pkg/prmode.Decide(trackedBy, hasRepoConnection) Mode` so "tracked → don't write, open a PR instead" has one place to live rather than zero

## Context

architecture.md's "GitOps and Argo CD" section requires: "If a CR is tracked
by Argo CD and a repository connection exists, the UI and CLI don't write
it ... open a PR ... Without a repository connection the CR is read-only
with an export button." Nothing made that call. `isTracked`
(`internal/cli/gitops_tracking.go`) already detected Argo CD/Flux tracking
correctly enough for its one existing use -- a warning in `gitops export`
-- but was private to `internal/cli`, so neither `pkg/prmode` (ADR 0038,
which opens the PR) nor `internal/uiapi` could call it, and
`pkg/prmode.SubmitPR` had no tracking check of its own: called, it always
opens a PR, with nothing upstream deciding that it should.

This is the same shape of problem ADR 0033 solved for `strongIdentity` and
`loadBundle`: a rule two packages need was a copy away from drifting, except
here there was no second copy yet -- the second caller didn't exist. The
aim is to make the rule reachable before a second copy can appear, not
to un-drift two already-diverging ones.

## Question 1: is a field-manager lookup enough for Flux when it gates a write?

`isTracked`'s old doc comment justified checking one field manager name,
`flux-client-side-apply`, over listing Kustomizations, because the function
only fed a warning. Used as a write gate, a miss means Ichor writes directly
to a CR Flux owns, the fight PR mode exists to prevent. Two facts
from primary sources (fluxcd.io docs and kustomize-controller source) bear
on that.

**`flux-client-side-apply` is not a Flux signal.** fluxcd.io's FAQ and
Kustomization page document it as the field manager name a *user* must give
their own `kubectl` edits for Flux to preserve them. It is the opposite of
"Flux applied this"; the old code and architecture.md had it wrong.

**`kustomize-controller` is a guess too.** kustomize-controller builds its
resource manager with `ssa.Owner{Field: r.ControllerName, ...}`, and the
controller name is configurable, so `kustomize-controller` is only the
default. A deployment that changes `--field-manager` never shows that name,
and Ichor would write directly to Flux-managed CRs. That is the reason
architecture.md has Ichor read `argocd-cm` instead of guessing an Argo CD
marker, and it applies equally here.

Flux also puts no label or annotation on what it applies; ownership is the
Kustomization's `.status.inventory`. So the authoritative check is the scan.

**Decided: scan inventories, keep the field manager as a first signal only.**
`IsTracked` order: managed fields naming `kustomize-controller` or
`helm-controller` (a hit is enough; a stale hit errs toward a PR, the safe
direction), then Argo CD via `argocd-cm`, then the Flux inventory scan, which
runs whenever the first two find nothing. A miss on the first signal never
means "not tracked".

Inventory shape, from the kustomize-controller `api/v1` `ResourceRef` doc
comment: `.status.inventory.entries[].id` is
`<namespace>_<name>_<group>_<kind>`, with the version in a separate `v` field.
fluxcd.io's prose says `..._<kind>_<version>`, but its own example ids
(`default_podinfo__Service`, `default_podinfo_apps_Deployment`, with `v`
alone carrying the version) match the source, so the source wins. The code
reads Kustomizations as `unstructured`; Ichor takes no Flux Go dependency.

Flux not installed (`NoKindMatch` or `NotFound` on the list) means "not
tracked", not an error and not "unknown". Any other list error (for example
forbidden) is returned as an error: for a gate, "cannot tell" must not become
"go ahead and write".

**Warning and decision stay one function.** The scan is one List, reached only
when nothing cheaper matched. `gitops export` already turns an `IsTracked`
error into a printed warning and continues, so a failed scan degrades it
gracefully, and a second, weaker function would be a second rule that can
drift (the problem ADR 0033 fixed).

Not covered: `HelmRelease` ownership. Only the first-signal manager name sees
it; whether a HelmRelease exposes an inventory to scan was not verified.
Deferred.

## Question 2: where does the shared rule live?

`pkg/gitopstracking`, a new package -- not `pkg/gitops` (the existing
write-side package), because `pkg/gitops/doc.go` states its own invariant
in so many words: "It makes no Argo CD, Git or Kubernetes API calls:
everything here is pure construction and YAML encoding" -- a deliberate
choice (ADR behind it argued against even the *dependency shape* of a live
client). `IsTracked` does a live `client.Get` by design; adding it to
`pkg/gitops` would either quietly break that package's own documented
invariant or require rewriting the comment that explains why the
dependency-light package exists at all, for a function that has nothing to
do with building Application/skeleton manifests. A new package costs one
file more than reusing `pkg/gitops`, and leaves both packages' own stated
scope true.

## Question 3: what happens with no repository connection?

architecture.md already answers this -- "Without a repository connection
the CR is read-only with an export button" -- so this is not a new design
decision: `Decide` implements that
sentence exactly rather than guessing at a nearby behavior (warn-but-write,
or refuse with an error): `ModeReadOnly`, the same outcome `Decide` also
returns for a Flux-tracked CR (see below), carries no write and no PR.

## Decision

`pkg/gitopstracking.IsTracked` (`tracking.go`) is `internal/cli`'s old
`isTracked`, moved rather than copied, with the Flux fix from Question 1.
`internal/cli/gitops_tracking.go` keeps `isTracked` and its six
package-local constants as one-line aliases/delegation to the new
package -- exactly ADR 0033's `bundleSecretKey` pattern -- so
code and tests that reference the lowercase symbols by name are
unaffected (`fluxFieldManager` aliases `kustomize-controller`).

`pkg/prmode.Decide` (`decide.go`) is new:

```go
type Mode int

const (
	ModeDirectWrite Mode = iota
	ModeOpenPR
	ModeReadOnly
)

func Decide(trackedBy string, hasRepoConnection bool) Mode
```

It takes `IsTracked`'s own `by` return value directly (`""`, `"Argo CD"`,
or `"Flux"`) and a caller-supplied connection flag, and does no I/O of its
own -- the caller already ran `IsTracked` (which does) and already knows
its own repository-connection state, so `pkg/prmode` gains this function
with zero new imports, consistent with every other file in the package.
`ModeOpenPR` is returned only for `"Argo CD"` with a connection, matching
architecture.md's sentence literally; every other case -- not tracked is
the one exception, mapped to `ModeDirectWrite` -- is `ModeReadOnly`,
including `"Flux"` regardless of connection state: ADR 0037 already scoped
PR mode to Argo CD (`ClassifySource` takes an Argo CD Application directly;
"wiring ... the Flux equivalent are both left to the caller"),
so a Flux-tracked CR has no `SubmitPR` path to route to yet, and
`ModeReadOnly` is what is left once `ModeDirectWrite` and `ModeOpenPR` are
ruled out -- not a new special case, the fallthrough of the two the design
already named.

## Consequences

- `internal/cli`'s behavior is unchanged.
- Not built here, same as ADR 0038/0037 left them: strict mode, override,
  the UI GitOps page, computing an impact preview, and wiring "CR → owning
  Application" for the Flux case. `pkg/prmode.Decide` and
  `pkg/gitopstracking.IsTracked` are both now importable from
  `internal/uiapi`, but nothing in `internal/uiapi` calls either yet --
  that caller is added separately (ADR 0051).
- `pkg/gitopstracking` has no Argo CD or Flux Go module dependency: the Flux
  inventory scan uses `unstructured`, the Argo CD branch a `corev1.ConfigMap`
  read.

- ADR 0051 wires the first caller: `internal/uiapi` now runs `IsTracked` and `Decide` before every write to an existing CR.
