# 0039. Config editing UI: PatchService screens, opaque-JSON editing, no generic schema-driven renderer

Status: Accepted · Date: 2026-10-26 · Design section: CLI and UI, Patch layers

Decided: built the `PatchService` CRUD screens (list, create, edit, delete) and an explicit impact-preview action on `ui/src/pages/PatchList.tsx`/`PatchDetail.tsx`, editing each `PatchDocument` and `LabelSelector` as a JSON blob in a `Textarea` rather than through a generic schema-driven form renderer, and relying on the apiserver's own CRD validation (surfaced via `describeError`) instead of a second, hand-written validation layer.

## Context

ADR 0034 found three of phase 6's four pieces blocked on missing API
surface and reported the shapes needed; ADR 0035 built exactly those
shapes (`PatchService`'s five CRUD methods plus `PreviewImpact`). This
unit is the screens on top of that backend -- the last piece of phase 6,
and the one the task that produced this ADR asked to be answered as
design questions before writing any code.

## Decision

### Question 1: no generic schema-driven form renderer

The CRD's own committed OpenAPI schema (`config/crd/bases/
ichor.maesh.dev_talosconfigpatches.yaml`) already exists specifically so a
form could read it instead of a form carrying its own copy of Talos's
field names (ADR 0003's whole reason for generating mirror types in the
first place). But `internal/uiapi`'s write path already enforces every
structural rule the CRD states -- layer/target consistency, exactly-one-of
on `target`, exactly-one-of on a document's kind field -- through strict
field validation against the real API server (ADR 0035), and returns the
CRD's own CEL message, already human-readable, as `CodeInvalidArgument`
when a write fails. A generic renderer walking that OpenAPI schema to
build input widgets would duplicate exactly those rules in TypeScript,
with no mechanism to keep the two in sync as Talos's own types change
version to version -- the same "parallel catalogue" problem ADR 0003
exists to avoid, moved one layer up the stack instead of eliminated.

**No generic schema-driven form renderer was built.** The screens edit
each `PatchDocument.document` as raw JSON in a `Textarea`, submit, and show
whatever the apiserver rejects via `describeError` (already shared by
every other screen, ADR 0021) above the Save button. This satisfies
"schema-driven forms" the way ADR 0034's gap report already anticipated:
the schema is the CRD's, read once at write time by the one validator that
actually has to stay correct against Talos's own versions, not re-read and
re-implemented by a form.

### Question 2: editing is a `Textarea` per document, plus one for a label selector

`PatchDocument.document` is opaque JSON on the wire (ADR 0035, to avoid
modeling every Talos field a third time); a `Textarea` is the direct,
one-line-of-reasoning match for an opaque string field, so
`PatchDetail.tsx` shows one per document with a `Select` beside it for
`kind`.

`PatchTarget.labelSelector` is not opaque on the wire -- `LabelSelector` is
a structured message (`matchLabels` map, `matchExpressions` list) -- but
building a dedicated add/remove-row editor for it would be the one bespoke
structured-editing UI in this unit, for the target type used least often
(role and a single machine reference both need only a `Select`/`Input`).
It is edited the same way as a document: one JSON blob in a `Textarea`,
parsed client-side (`ui/src/lib/patchTarget.ts`'s `parseLabelSelector`)
only because a `Textarea`'s string has to become a structured field before
it can be sent at all -- that parse is syntax only (`JSON.parse`, then a
shape check), never a rule the CRD already states. A malformed selector
therefore fails before any request is sent, with its own "must be valid
JSON" message, distinct from a server-side rejection.

No editor library was added for either case, per the task's own
constraint; `Textarea` (already in `components/ui/`) is enough for both.

### Question 3: impact preview is an explicit action, not live

`PatchDetail.tsx`'s "Preview impact" button calls `PreviewImpact`
on click, not on every keystroke. Three reasons, in order of weight:
`PreviewImpact` is a round trip against a real `TalosCluster` read and a
`pkg/fieldmeta.Assess` call, not a pure local computation -- firing it on
every character typed into a document's `Textarea` (where most
intermediate states are invalid JSON anyway) would mean either silently
swallowing errors on every keystroke or showing a flickering error for
normal typing. "Impact preview **before applying**" (architecture.md,
"CLI and UI") reads as a deliberate check before a decision, the same
posture ADR 0021's approve dialog already takes for a different
near-irreversible action, not a live inline hint. The preview result is
shown with an explicit note that it does not update automatically, so
re-running it after a further edit is a visible, deliberate step rather
than something a reader has to infer from a stale result.

### Screens

Four additions, following ADR 0021's own screen conventions exactly
(data fetching is `useEffect`/`useState` per page calling the generated
client directly; errors go through `describeError`/`ErrorAlert`; a
required `Select` with no default disables Save until filled, the same
pattern the approve dialog's role picker already established):

- `/patches` -- `PatchList`: every patch the backend can see (`ListPatches`
  with no cluster filter, mirroring `MachineList`'s "list everything
  unconditionally" convention), with cluster name, layer, priority, a
  one-line target summary and document kinds per row, a "New patch"
  button, and a delete confirmation dialog per row (patch.proto's own doc
  comment: deleting a patch is not like rejecting a machine -- there is no
  "it would come back" concern, so delete actually deletes, and the
  dialog says so).
- `/patches/new` and `/patches/:namespace/:name` -- `PatchDetail`: one
  component for both create and edit (`PatchService`'s own "full replace,
  no partial-field merge" shape, ADR 0035, makes a single form for both
  the natural fit). Cluster is a required `Select`; `name` is editable only
  on create (`UpdatePatch` never renames); `namespace` is set from the
  chosen cluster's own namespace, not asked for separately, matching
  `ClusterRef`'s `corev1.LocalObjectReference` convention (same namespace
  only). Layer is a required `Select` restricted to `cluster`/`role`/
  `selector`/`machine` (`CONFIG_LAYER_SYSTEM` is generated by Ichor itself,
  architecture.md's "Patch layers" -- never offered). A target section
  (role / label selector / machine reference) appears only for the three
  layers that require one; Save is disabled until every layer-appropriate
  field is filled, the same "disabled until valid, client-side, before
  ever calling the server" posture ADR 0021 already uses. Documents are a
  repeatable kind-`Select` plus `Textarea` list, "Add document"/"Remove"
  per row. An "Impact preview" card and a Delete button (edit mode only,
  same confirmation dialog as the list) round it out.

`ui/src/lib/connect.ts` gained `patchClient` (`createClient(PatchService,
transport)`), the same one-line addition as the three existing clients --
no new transport, no new pattern.

`ui/src/pages/NodeDetail.tsx`'s effective-config section (ADR 0034) is
unchanged in its grouping logic -- per the task's own instruction, that
computation was not redone -- only its one-line caveat was updated from
"editing isn't available from this screen yet" to a link to `/patches`,
since that is no longer true.

### Testing

Followed ADR 0021's own precedent exactly: no component-rendering
library exists in this project (`ui/package.json`'s `test` script is
`node --test "src/**/*.test.ts"`, chosen specifically to add none), so
pages themselves are not unit-tested -- only pure logic is, the same as
`resumableStream.ts`/`.test.ts`. The one piece of new logic worth
extracting that way is `ui/src/lib/patchTarget.ts` (`targetKindOf`,
`stringifyLabelSelector`, `parseLabelSelector`), covered by
`patchTarget.test.ts`: round-tripping a selector through the textarea
representation, and rejecting invalid JSON or a non-object JSON value
with its own message rather than guessing. That file deliberately imports
nothing from `ui/src/gen` or `@bufbuild/protobuf`: the generated client's
own files use plain, extensionless relative imports among themselves,
which only a bundler's resolution (Vite, `tsc`'s `moduleResolution:
bundler`) follows -- Node's own ESM resolver, which `node --test` uses
directly with no bundler in front of it, cannot. `patchTarget.ts`'s
exported functions instead take structurally-compatible `*Like`
interfaces, confirmed against the generated types at every call site in
`PatchList.tsx`/`PatchDetail.tsx` by `tsc -b --noEmit` passing clean.

Pages that do construct generated-message-shaped request objects
(`PatchDetail.tsx`) use `MessageInitShape<typeof PatchSchema>` (from
`@bufbuild/protobuf`), not the generated `Patch` message type itself:
confirmed by first getting this wrong -- a `Patch`-typed local variable
built from a plain object literal failed `tsc` with "Property
'$typeName' is missing," because `createClient`'s own call signatures
(`@connectrpc/connect`'s `promise-client.d.ts`) accept `MessageInitShape<I>`
specifically so a caller never has to carry that brand on code it writes
from scratch -- the same shape every other page in this project already
passes without a cast, confirmed by not needing one anywhere in
`ClusterList.tsx`/`MachineList.tsx`/`NodeDetail.tsx`.

## Consequences

- New: `ui/src/pages/PatchList.tsx`, `ui/src/pages/PatchDetail.tsx`,
  `ui/src/lib/patchTarget.ts`, `ui/src/lib/patchTarget.test.ts`.
- Changed: `ui/src/lib/connect.ts` (`patchClient`), `ui/src/app.tsx` (nav
  entry, three routes), `ui/src/pages/NodeDetail.tsx` (one caveat line and
  a link, grouping logic untouched).
- No new preactcn component was installed -- `Textarea`, `Select`,
  `Dialog`, `Input`, `Label`, `Badge`, `Button`, `Card`, `Table` (all
  already present, ADR 0021) cover every field this unit needed.
- No proto change; `ui/src/gen/` was not touched (it already carries
  `patch_pb.ts` from ADR 0035).
- `make build test lint` (Go) and `make ui-ci`/`make ui-test` (UI) all
  pass; `rm -rf cmd/ichor-ui/dist` then `git status` confirmed no
  generated-artifact drift from this unit.
- Not built, per the task's own boundary and because none of it needs
  to exist: `matchExpressions`-only convenience fields on the label
  selector form (folded into the one JSON `Textarea` instead, see Question
  2); a live/typing-triggered impact preview (Question 3); any schema
  introspection of the CRD's OpenAPI document in the browser (Question 1).

## Alternatives considered

**Reading `config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml` as a
static asset and walking it to render document fields.** ADR 0034
proposed this as viable (no live cluster state in it, so serving it
doesn't cross "browser talks only to the backend"). Not built: viable to
*serve* is not the same question as worth *building a renderer for* --
the renderer itself would be the parallel-validation problem, not the
asset fetch. Revisit only if the opaque-JSON `Textarea` proves too rough
for a real operator, which this unit has no evidence of yet.

**A dedicated row-based editor for `LabelSelector.matchExpressions`.**
Considered and dropped in favor of the single JSON `Textarea` covering
both `matchLabels` and `matchExpressions` -- see Question 2. The two
target kinds that are simple enough for a dedicated widget (role, a single
machine reference) already have one (`Select`, `Input`); label selector is
the one case complex enough that a bespoke structured editor and the
opaque-JSON pattern are close in cost, and only one of those two patterns
is worth maintaining in this codebase.
