# 0044. Tracking detection moves to `pkg/gitopstracking`; `pkg/prmode.Decide` turns it into a write-vs-PR gate

Status: Accepted · Date: 2026-10-26 · Design section: GitOps and Argo CD ("PR mode")
Decided: moved `isTracked` from `internal/cli` to a new `pkg/gitopstracking` package unchanged in behavior except for a corrected Flux signal (`kustomize-controller`/`helm-controller`, not `flux-client-side-apply`), and added a pure `pkg/prmode.Decide(trackedBy, hasRepoConnection) Mode` so "tracked → don't write, open a PR instead" has one place to live rather than zero

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
job was to make the rule reachable before a second copy could appear, not
to un-drift two already-diverging ones.

## Question 1: is the cheap Flux check (a field-manager lookup) still enough when it gates a write, not just a warning?

`isTracked`'s own doc comment justified checking one field manager name,
`flux-client-side-apply`, over listing every Kustomization and searching
its `.status.inventory`, on the grounds that this function only ever fed a
warning: an ambiguous case could default to "check anyway" for free, and a
miss cost nothing a human skimming export's warnings wouldn't still catch
by other means. Used as a write gate instead, a miss means Ichor writes
directly to a CR Flux currently owns -- the exact fight PR mode exists to
prevent. The question the task asked was whether this promotion in stakes
also promotes the cost-benefit case for scanning inventory instead.

**It does not, but only after fixing a real bug the promotion exposed.**
Checked against fluxcd.io's current docs (kustomize-controller's
"Kustomization" page and the project FAQ, both fetched 2026-10-26, Flux
v2.7-2.9 generation) rather than trusted from the existing comment:

- Flux has used server-side apply by default since well before this
  generation. Under SSA, the field manager a Flux controller registers in
  `managedFields` when it applies a resource is the controller's own name:
  `kustomize-controller` for a `Kustomization`'s applies, `helm-controller`
  for a `HelmRelease`'s (the kustomize-controller docs page shows this
  directly in an example event: "kustomize-controller Service/default/podinfo
  created"; a web search corroborated `helm-controller` independently,
  itself consistent with upstream Helm's own field-manager convention).
- `flux-client-side-apply` -- the one string the old code checked -- is
  documented on fluxcd.io's own FAQ as the **opposite** signal: "In order
  for Flux to preserve fields added with `kubectl`, ... you have to specify
  a field manager named `flux-client-side-apply`." It is the reserved name
  an *operator's own* manual edit must use so Flux's drift detection
  tolerates it, not a name Flux's controllers register when applying a
  resource themselves. No source found supports it ever being Flux's own
  manager name under either apply strategy for the versions this project
  can run against.
- Net effect: the pre-existing code's Flux branch, used as a decision
  gate, would false-negative on essentially every current Flux-managed
  resource (it checks a string that is not Flux's apply signal) while also
  being able to false-positive on an unrelated object a human happened to
  `kubectl apply --field-manager=flux-client-side-apply` once. **This is a
  bug the warning-only version already shipped with, caught only because
  this task asked the stakes question seriously.**

With the field manager corrected to `kustomize-controller`/`helm-controller`,
is the lookup -- one read on the object already in hand -- now sufficient
for a gate, or does gating still demand the inventory scan? The only
failure mode found that a lookup can miss and a scan would not: a resource
Flux applied in the past, whose `managedFields` entry survives after Flux
stopped reconciling it (removed from the Kustomization's source, prune
disabled) -- `.status.inventory` would no longer list it, while
`managedFields` still would. That is a **false positive**: Ichor treats an
actually-abandoned resource as still tracked and routes it to a PR it
didn't strictly need. It fails toward caution, not toward a direct write
colliding with a live Flux reconcile -- the dangerous direction this gate
exists to prevent. The dangerous direction -- a resource Flux *currently*
owns carrying neither field-manager string -- was not found to occur under
normal operation (the controller name is not configurable; `--override-manager`
only lets a *different* manager pre-empt Flux's own fields, it does not
rename Flux's own identity). The one window where it could happen (another
writer displaces every field Flux owns between two reconciles) is not
closed by scanning the inventory either: `.status.inventory` is only as
fresh as Flux's last successful reconcile, the same staleness bound the
field-manager entry has. Scanning gains nothing against the actual risk and
costs an `unstructured`-based list-every-Kustomization-and-search-its-inventory
path (plus, eventually, the same again for `HelmRelease`) for a case the
lookup already covers as well as the scan would.

**Decided: the field-manager lookup, corrected, is enough. No inventory
scan was built.**

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
decision, only this unit's confirmation that `Decide` implements that
sentence exactly rather than guessing at a nearby behavior (warn-but-write,
or refuse with an error): `ModeReadOnly`, the same outcome `Decide` also
returns for a Flux-tracked CR (see below), carries no write and no PR.

## Decision

`pkg/gitopstracking.IsTracked` (`tracking.go`) is `internal/cli`'s old
`isTracked`, moved rather than copied, with the Flux fix from Question 1.
`internal/cli/gitops_tracking.go` keeps `isTracked` and its six
package-local constants as one-line aliases/delegation to the new
package -- exactly ADR 0033's `bundleSecretKey` pattern -- so
`gitops_tracking_test.go` and `gitops_export_test.go` needed no edits at
all: both reference the lowercase symbols by name, not by literal value,
so the Flux fix underneath is invisible to them while still being exercised
by them (`TestIsTracked_FluxManagedFieldsEntry` sets `Manager:
fluxFieldManager`, which now aliases `kustomize-controller`).

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
"wiring ... the Flux equivalent are both left to the caller/a later unit"),
so a Flux-tracked CR has no `SubmitPR` path to route to yet, and
`ModeReadOnly` is what is left once `ModeDirectWrite` and `ModeOpenPR` are
ruled out -- not a new special case, the fallthrough of the two the design
already named.

## Consequences

- `internal/cli`'s behavior and tests are unchanged byte-for-byte in
  `gitops_tracking_test.go`/`gitops_export_test.go`; `goconst`'s
  package-scoped counting meant two new string literals
  (`"kustomize-controller"`, `"FieldsV1"`) needed pulling into named
  constants inside `pkg/gitopstracking` itself once its own test file
  repeated them three times -- a `pkg/gitopstracking`-local concern, not a
  cross-package one.
- Not built here, same as ADR 0038/0037 left them: strict mode, break-glass,
  the UI GitOps page, computing an impact preview, and wiring "CR → owning
  Application" for the Flux case. `pkg/prmode.Decide` and
  `pkg/gitopstracking.IsTracked` are both now importable from
  `internal/uiapi`, but nothing in `internal/uiapi` calls either yet --
  that caller is a later unit's job.
- `pkg/gitopstracking` has no Argo CD or Flux Go module dependency, same as
  `pkg/gitops` before it: `IsTracked`'s Flux branch stayed a `managedFields`
  string comparison, its Argo CD branch stayed a `corev1.ConfigMap` read,
  neither needing `unstructured` or a new dependency.
