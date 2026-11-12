# 0034. Config editing UI: the layer-grouped effective-config view, and why the rest waits on proto

Status: Accepted · Date: 2026-10-26 · Design section: CLI and UI, Patch layers

Decided: built the one piece of phase 6's "config editing by layer ... effective-config view annotated by origin and secrets masked" that the current API surface actually supports — grouping `GetEffectiveConfig`'s values by `origin.layer` in the design's own bottom-up order — and did not build layer *editing*, impact preview, or schema-driven forms, because none of them have a backend to call yet.

## Context

Roadmap phase 6 is "config editing UI." Architecture.md's `## CLI and UI`
section names four things: "config editing by layer," "an effective-config
view annotated by origin and secrets masked," "impact preview before
applying," and "schema-driven forms." What `proto/ichor/v1alpha1/` exposes decides how much of it can be built:
since ADR 0016 it has only `ClusterService`, `MachineService` and
`NodeService`, none of them with a write method for `TalosConfigPatch`:

- `NodeService.GetEffectiveConfig` is read-only and already returns
  exactly `EffectiveConfigValue{path, value, secret, origin{layer,
  source}}` per leaf (ADR 0018) — the "effective-config view annotated by
  origin and secrets masked" clause is already fully backed.
- There is no `PatchService` (or any method on an existing service) to
  list, create, update or delete a `TalosConfigPatch`. "Config editing by
  layer" means writing one; there is nothing to call.
- `pkg/fieldmeta.Assess(paths []string, pinned *VersionContract) Impact`
  is the whole implementation of "impact preview" — but it takes *the set
  of changed paths* as input. Nothing changed exists without an edit to
  diff against, and no RPC passes a proposed change anywhere, so this
  function cannot be invoked from the browser's side of any existing
  call. It is also Go-only, not reachable from the UI directly even if an
  edit existed to feed it.
- No RPC returns a CRD's schema. `TalosConfigPatch`'s full shape (the
  typed-config mirror types ADR 0003 generates) is baked into
  `config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml`'s committed
  OpenAPI schema, but nothing serves that schema to a browser that only
  talks to the backend, not the Kubernetes API server (architecture.md,
  "Trust").

## Decision: build the effective-config view's layer grouping, nothing that needs a write path

`ui/src/pages/NodeDetail.tsx`'s `EffectiveConfigSection` already called
`GetEffectiveConfig` and rendered one flat table (built with the phase 5 UI). It did not satisfy "the effective-config view in
the UI shows which layer each value came from" (architecture.md, "Patch
layers" — the one concrete UI requirement that section states): origin
was a text column to read per row, not a grouping. `groupByLayer` buckets
`EffectiveConfigValue[]` by `origin.layer` and renders one table per
layer, in `ConfigLayer`'s own numeric order — which is already the
design's bottom-up order (system, cluster, role, selector, machine), so
no second ordering table was needed. A short, explicit line above the
tables says this view is read-only and that editing isn't available from
this screen yet, rather than leaving a reader to wonder why there's no
edit button.

**Not built, and not stubbed**: a patch-editing form, an impact-preview
panel, or a schema-driven field list. A form with no RPC to
submit to is not a smaller version of the feature, it is a non-functional
one that looks finished. Building it would also have meant two mistakes:
hand-listing `TalosConfigPatch`'s
fields (when mirrorgen exists specifically so no parallel catalogue of
Talos's field names has to exist, ADR 0003) and hand-porting
`pkg/fieldmeta`'s reboot/install-time verdicts into JS (a second, harder
to keep in sync, copy of a table whose own package doc explains at length
why each entry needed real verification against Talos's source rather
than being guessed).

## Gap report: what proto needs for the rest of phase 6

Not added to proto here. Each gap is recorded with enough shape
that implementing it is mechanical:

1. **A write path for `TalosConfigPatch`.** Smallest useful shape: a
   `PatchService` with `ListPatches(cluster ObjectRef) → []Patch`,
   `GetPatch`, `CreatePatch`, `UpdatePatch`, `DeletePatch`, mirroring
   `MachineService`'s own shape (ADR 0016) rather than inventing a new
   one. `Patch` needs at least `ref`, `clusterRef`, `layer`, `priority`,
   `target`, `documents[]` — the same fields `TalosConfigPatchSpec`
   already has; the wire message should mirror the CRD spec, not reduce
   it.
2. **Impact preview needs a method that takes a proposed edit and
   returns `pkg/fieldmeta.Assess`'s result.** Either a dry-run field on
   `CreatePatch`/`UpdatePatch` (compute the diff server-side against the
   currently-applied config, call `Assess`, return the `Impact` alongside
   or instead of writing), or a standalone `PreviewImpact(patch) →
   {rebootRequired bool, installTimeOnly []string, unsupported []string}`
   — `fieldmeta.Impact`'s three fields, unchanged. Either way, the UI
   needs to show `installTimeOnly` and `rebootRequired` as genuinely
   different outcomes, not two severities of the same warning: a reboot does nothing
   for an install-time-only field, so presenting them identically would
   invite an operator to reboot for no reason.
3. **Schema-driven forms do not need a new RPC** — `config/crd/bases/
   ichor.maesh.dev_talosconfigpatches.yaml`'s OpenAPI schema is already
   committed and already generated from the same mirror types a form
   would need to render (ADR 0003), and the browser already never talks
   to the Kubernetes API server directly, so serving that one YAML file
   (or a JSON-converted copy) as a static asset — no live cluster state
   in it — doesn't cross the "browser talks only to the backend" line
   either way. This is recorded here rather than built now because a form
   with nothing to submit to (point 1) is not worth building yet, not
   because the schema itself is hard to reach.

## Consequences

- `ui/src/pages/NodeDetail.tsx`'s effective-config view groups by layer;
  nothing else changes in `ui/src/lib/`, `proto/` or `internal/uiapi/`.
- No proto change.
- The three gaps above are not new discoveries — ADR 0016 and ADR 0018
  already named "config editing, a different, later unit" and "disk
  roles and network link configuration join this request message,
  additively, once they land on the CRD" as deferred. This ADR is the
  first to need them and find no workaround, so it records the concrete
  shapes rather than leaving the gap as a one-line mention.
- `TalosOperation`'s progress view, the GitOps page, PR mode and OIDC
  are separate decisions, not covered here.
