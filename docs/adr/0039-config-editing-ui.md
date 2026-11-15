# 0039. Config editing UI: PatchService screens, opaque-JSON editing, no generic schema-driven renderer

Status: Accepted · Date: 2026-10-26 · Amended · Design section: CLI and UI, Patch layers

Decided: built the `PatchService` CRUD screens (list, create, edit, delete) and an explicit impact-preview action on `ui/src/pages/PatchList.tsx`/`PatchDetail.tsx`, editing each `PatchDocument` and `LabelSelector` as a JSON blob in a `Textarea` rather than through a generic schema-driven form renderer, and relying on the apiserver's own CRD validation (surfaced via `describeError`) instead of a second, hand-written validation layer.

## Context

ADR 0034 found three of phase 6's four pieces blocked on missing API
surface and reported the shapes needed; ADR 0035 built exactly those
shapes (`PatchService`'s five CRUD methods plus `PreviewImpact`). This
ADR is the screens on top of that backend -- the last piece of phase 6.

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
structured-editing UI in these screens, for the target type used least often
(role and a single machine reference both need only a `Select`/`Input`).
It is edited the same way as a document: one JSON blob in a `Textarea`,
parsed client-side (`ui/src/lib/patchTarget.ts`'s `parseLabelSelector`)
only because a `Textarea`'s string has to become a structured field before
it can be sent at all -- that parse is syntax only (`JSON.parse`, then a
shape check), never a rule the CRD already states. A malformed selector
therefore fails before any request is sent, with its own "must be valid
JSON" message, distinct from a server-side rejection.

No editor library is added for either case; `Textarea` (already in `components/ui/`) is enough for both.

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
unchanged in its grouping logic; only its one-line caveat was updated from
"editing isn't available from this screen yet" to a link to `/patches`,
since that is no longer true.

### Pure logic lives in `ui/src/lib`

No component-rendering library exists in this project (`ui/package.json`'s
`test` script is `node --test "src/**/*.test.ts"`, chosen to add none), so
pages carry no logic of their own worth unit-testing; pure logic lives in
`ui/src/lib`, as ADR 0021 set out. Here that is `ui/src/lib/patchTarget.ts`
(`targetKindOf`, `stringifyLabelSelector`, `parseLabelSelector`): it
round-trips a selector through the textarea representation and rejects
invalid JSON or a non-object JSON value with its own message rather than
guessing.

That file imports nothing from `ui/src/gen` or `@bufbuild/protobuf`: the
generated client's files use plain, extensionless relative imports among
themselves, which only a bundler's resolution (Vite, `tsc`'s
`moduleResolution: bundler`) follows, and Node's own ESM resolver, which
`node --test` uses directly, cannot. `patchTarget.ts`'s exported functions
instead take structurally-compatible `*Like` interfaces.

Pages that construct generated-message-shaped request objects
(`PatchDetail.tsx`) use `MessageInitShape<typeof PatchSchema>` (from
`@bufbuild/protobuf`), not the generated `Patch` message type: `createClient`'s
call signatures (`@connectrpc/connect`'s `promise-client.d.ts`) accept
`MessageInitShape<I>` so a caller building a message from scratch never has
to carry the generated type's `$typeName` brand. This is the shape every other
page in the project already passes without a cast.

## Consequences

- New: `ui/src/pages/PatchList.tsx`, `ui/src/pages/PatchDetail.tsx`,
  `ui/src/lib/patchTarget.ts`, `ui/src/lib/patchTarget.test.ts`.
- Changed: `ui/src/lib/connect.ts` (`patchClient`), `ui/src/app.tsx` (nav
  entry, three routes), `ui/src/pages/NodeDetail.tsx` (one caveat line and
  a link, grouping logic untouched).
- No new preactcn component was installed -- `Textarea`, `Select`,
  `Dialog`, `Input`, `Label`, `Badge`, `Button`, `Card`, `Table` (all
  already present, ADR 0021) cover every field the screens need.
- No proto change; `ui/src/gen/` already carries `patch_pb.ts` from
  ADR 0035.
- Not built: `matchExpressions`-only convenience fields on the label
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
for a real operator, which nothing yet suggests.

**A dedicated row-based editor for `LabelSelector.matchExpressions`.**
Considered and dropped in favor of the single JSON `Textarea` covering
both `matchLabels` and `matchExpressions` -- see Question 2. The two
target kinds that are simple enough for a dedicated widget (role, a single
machine reference) already have one (`Select`, `Input`); label selector is
the one case complex enough that a bespoke structured editor and the
opaque-JSON pattern are close in cost, and only one of those two patterns
is worth maintaining in this codebase.

## Amendment: a field search in front of the Textarea (2026-11-15)

ADR 0039 said to revisit the schema question if the JSON `Textarea` proved too
coarse. The request to look a field up in a search box and fill in its value is
that condition arriving, and ADR 0165 sized the options. The decision keeps the
rule that the browser reads no schema and the server alone validates, and adds
the search without either.

- **A generated index, not a schema read.** `make ui-fieldindex` (gendocs)
  writes `ui/src/gen/fieldindex.json`: one row for each field of each document
  kind in the `TalosConfigPatch` schema, and one row for each kind. It ships in
  the UI bundle as a lazily loaded chunk. ichor-ui's role is unchanged, and the
  Helm release record does not grow, because nothing is added to a CRD. A field
  added to a kind appears in it without a list being edited, and a gendocs test
  adds a field to a copy of the schema to hold that.
- **The index is built, not committed.** It holds Talos's own field
  descriptions, and the repository holds none of Talos's prose
  (`TestNoUpstreamProse`), so the file is ignored by git and every UI target
  that reads it (`ui-typecheck`, `ui-test`, `ui-build`, and so `ui-ci` and the
  images) writes it first. It cannot go stale for that reason. Committing it
  means naming it in NOTICE and exempting it from that test, which is a licence
  decision, not an implementation one.
- **The CRD gives the shape, Talos's tables give the words.** Paths, types,
  `required` and `format` come from the schema. A description and the allowed
  values come from the `Doc` method of the machinery type that the mirror's
  `ToMachinery` returns, matched by the YAML name down the path. The schema's
  own description is the fallback, which is how fields of the Kubernetes types
  (kubelet, kube-proxy, kube-scheduler, audit policy) keep theirs.
- **Allowed values are used for a string field only.** A boolean's values are
  its spellings, a switch covers them. A map's or an array's are examples, not
  a closed set. An integer enumeration such as a bond's `mode` lists names in
  Talos's table while the CRD takes the number, so offering the names in a
  `Select` would write a value the API server refuses.
- **The static pod is not in the index.** `KubeStaticPodConfig` is the whole of
  `core/v1` `Pod`: 1,075 of the 2,281 rows, and half of the index's compressed
  size, and each of them would answer a search for `name`, `image` or `ports`.
  The kind has one row of its own, so a search for it adds the document, which
  is then edited as JSON.
- **A row below an array element writes the first element, and one below a map
  value edits the map.** More elements and map keys are the JSON `Textarea`'s
  job.
- **Only the CRD is the validator.** An input holds no rule of its own: a
  required field missing, or a value out of range, is refused when the patch is
  saved. Where Talos documents a field with an empty table entry (the
  v1alpha1 `Config` has many), the row has no description and the search still
  finds it by its path.

## Amendment: the index has each setting once, by its path in spec.config (2026-11-16)

The index of the first amendment read the `spec.config` schema and wrote each
setting back as a Talos document kind and a path inside it. A setting that more
than one document spells was a row for each spelling (`machine.sysctls` of the
v1alpha1 document and `params` of `SysctlConfig`), and every row named a kind a
reader has no reason to know. `spec.config` is the only place a patch carries
Talos configuration (ADR 0165), and it has one path for each setting, so the
index follows it.

- **A row is a setting of `spec.config`, by its path there.** The search shows
  that path, the type and the description, and finds a setting by its name, its
  path, the kind of the document that spells it or its description. A setting
  is one row however many documents spell it.
- **The `ichor` tags give the way back to the documents.** Each setting of the
  schema is tagged with where the documents spell it, as `Kind:path`; a row
  below a setting that is a Talos type has the same rest of the path in both.
  The row's `doc` is the first of them (a document before the v1alpha1
  document), and the description and the allowed values are read there, by the
  rule of the first amendment. A gendocs test fails when two places of the
  schema name the same setting of a document, which would be two rows for one
  setting.
- **A collection has two rows.** The collection itself (`machine.volumes.user`),
  which adds a document of the kind, and its key (`machine.volumes.user.<key>`),
  which is the document's name. The fields below are written
  `machine.volumes.user.<key>.provisioning.grow`.
- **The editor still writes documents, and `doc` is how.** The wire carries
  `PatchDocument` and the kind `Select` and the JSON `Textarea` are unchanged,
  so a chosen row goes into the document its `doc` names, and a patch edited
  either way saves the same. `doc` and the kind `Select` go when the wire and
  the editor move to `spec.config`, which is the change after this one; nothing
  in the index is needed for it but the path.
