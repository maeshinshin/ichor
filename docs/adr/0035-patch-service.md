# 0035. PatchService: the config-editing backend

Status: Accepted · Date: 2026-10-26 · Design section: CLI and UI, Patch layers

Decided: built `PatchService` (CRUD for `TalosConfigPatch` plus `PreviewImpact`) exactly as ADR 0034's gap report proposed it, with the opaque-JSON `PatchDocument` shape that avoids re-modeling Talos fields a third time, and with no new RPC for schema-driven forms since the CRD's own committed OpenAPI schema already serves that purpose.

## Context

ADR 0034 found phase 6's "config editing by layer ... impact preview
before applying ... schema-driven forms" (architecture.md, "CLI and UI")
blocked on three missing pieces of API surface, and reported concrete
shapes for each rather than building against nothing. This unit builds
exactly those shapes.

## Decision

### `PatchService`: CRUD mirroring `MachineService`'s own shape

`ListPatches`/`GetPatch`/`CreatePatch`/`UpdatePatch`/`DeletePatch`, same
pattern `MachineService` already established (ADR 0016): `ListPatches`
optionally filters to one cluster, exactly like `ListMachinesRequest`.
`CreatePatch` and `UpdatePatch` are both full-replace -- no partial-field
merge semantics to get wrong -- and both require `patch.ref.namespace`/
`patch.ref.name` set by the caller; this service never generates a name.
`DeletePatch` actually deletes, unlike `RejectMachine`: a rejected machine
must never be deleted (ADR 0018, so discovery can't recreate it as fresh),
but a patch has no analogous "it would come back" concern, so deleting it
is the whole operation "remove this patch" means.

**The layer concept is not reduced.** `Patch.layer` reuses `ConfigLayer`,
the same enum `GetEffectiveConfig` already returns per value (node.proto)
-- one enum, not two, for the one concept. `PatchTarget` mirrors
`TalosConfigPatchTarget` with all three of its forms, including
`LabelSelector`'s `matchExpressions`, not only `matchLabels`: modeling
only the common case would have been a real capability regression against
what the CRD already accepts directly. `LabelSelector`/
`LabelSelectorRequirement` are new proto messages, but they mirror
`metav1.LabelSelector` -- Kubernetes' own well-known, finite API shape,
not a Talos field this project would otherwise catalogue a second time,
so modeling it fully costs little and is not the parallel-catalogue
problem ADR 0003 exists to avoid.

Every structural rule the CRD's own `+kubebuilder:validation:XValidation`
rules already state -- layer/target consistency, exactly-one-of on
`target`, exactly-one-of on a document's kind-matching field -- is
enforced there, on the write itself, via the real Kubernetes API
(`s.Create`/`s.Update`), not re-implemented in Go. `mapWriteError`
(`patch_service.go`) turns `apierrors.IsInvalid` into
`connect.CodeInvalidArgument`, carrying the CRD's own CEL message
verbatim -- already human-readable (ADR 0018's own refusal messages set
this precedent), so there is nothing to re-word.

### `PatchDocument.document` is opaque JSON, not a third catalogue

`patch.proto`'s own comment states the reasoning ADR 0034 flagged as the
one real risk of building this unit at all: modeling every typed-config
field a third time (Go mirror type, CRD schema, now protobuf) would be
exactly the parallel catalogue ADR 0003's generator exists to avoid.
`PatchDocument{kind, document}` carries `document` as plain JSON, shaped
exactly as `TalosConfigPatchDocument`'s matching Go field would unmarshal
it. `documentFieldKey` (`convert.go`) is the one place that maps a kind
string to its JSON field name -- six entries, the same allow-list
`render.DocumentFromPatch`'s own switch already enumerates, not a second
one -- and `documentFromPB`/`documentToPB` wrap/unwrap the raw JSON under
that key so `encoding/json` fills (or reads) the real typed field itself.
No field of any of the six kinds is named, checked or round-tripped by
hand anywhere in this unit.

### `PreviewImpact`: every path the patch sets, not a diff

`PreviewImpact` takes a proposed `Patch` (new, via `CreatePatch`'s own
shape, or an edit of an existing one), never writes anything, and returns
`pkg/fieldmeta.Assess`'s three fields unchanged:
`reboot_required bool`, `install_time_only []string`,
`unsupported []string`. It classifies every field path the patch's own
documents set, not a diff against whatever is currently applied:
computing a true path-level diff between an existing patch's documents
and a proposed edit is real additional work this unit does not attempt,
and `pkg/fieldmeta`'s own doc comment already states the safe direction
when exact information isn't available -- showing impact for a field even
if an edit happens to leave it unchanged over-reports rather than
under-reports, which is the direction that can't leave a node running a
configuration it doesn't have.

`install_time_only` and `reboot_required` are two different response
fields, not two severities of one signal, per the task's own instruction:
a reboot does nothing for a field Talos only reads at install/imaging
time (`pkg/fieldmeta`'s own doc comment), so a caller presenting them
identically would let an operator pick "reboot" for a change nothing
short of reset-and-reinstall actually applies.

`documentLeafPaths` (`effectiveconfig.go`, renamed from an anonymous
block inside `patchOrigins`) is the one place that renders one document in
isolation and flattens it to field paths; `patchOrigins` (origin
attribution for `GetEffectiveConfig`) and `patchPaths`
(`patch_service.go`, impact classification for `PreviewImpact`) both call
it rather than each re-implementing "what paths does this one document
set."

### Schema-driven forms: no new RPC, confirmed rather than assumed

ADR 0034 proposed this and nothing here contradicts it: `config/crd/
bases/ichor.maesh.dev_talosconfigpatches.yaml`'s OpenAPI schema is
already committed and already generated from the exact mirror types
`documentFieldKey` maps into. A form renderer reads that file as a static
asset -- no live cluster state in it, so serving it doesn't cross
"browser talks only to the backend" either way -- and submits whatever
JSON it produces as `PatchDocument.document`. Still not built in this
unit (the task's own boundary: "UI 側の画面はこのユニットに含めません"); the
backend this form would call now exists.

### Strict field validation and RBAC

`CreatePatch` and `UpdatePatch` both pass `strictFieldValidation`
(`machine_service.go`'s existing package-level var) to `s.Create`/
`s.Update` -- the same discipline every other write in this package
already has, not a new one invented for this service.
`internal/uiapi/strictvalidation_test.go`'s `recordingUpdateClient`
gained a `Create` override (it already had `Update`) so a test can assert
`CreatePatch`'s own write requests it the same way the existing
`ApproveMachine`/`RejectMachine` tests do for `Update`.

`internal/uiapi/rbac_markers.go`'s `talosconfigpatches` line moved from
`get;list` (read-only, ADR 0018) to `get;list;create;update;delete` -- the
one CRD this package now actually creates and deletes, not only updates
in place the way `talosmachines` is. `talosclusters` stays `get;list`
only: `PreviewImpact` reads a `TalosCluster` for its version contract, it
never writes one.

### No masking concern

`TalosConfigPatch`'s typed-config mirror types never carry a secret field
at all -- ADR 0003's generator drops the eleven `meta.Unstructured`
escape-hatch fields and the nine secret-bearing scalars from the mirror
types entirely, not just from what gets rendered. There is nothing for
this service to mask, because there is nothing secret-shaped in a
`TalosConfigPatch` to begin with; the task's "don't build an unmask
mechanism" instruction has no mechanism to not-build here.

## Consequences

- `proto/ichor/v1alpha1/patch.proto` is new; `buf lint` and
  `buf breaking` both pass clean against it (every addition, no removal
  or change to an existing file).
- `internal/uiapi/patch_service.go` (the five CRUD methods plus
  `PreviewImpact`), `internal/uiapi/convert.go` (the wire/CRD conversions),
  and `internal/uiapi/effectiveconfig.go` (the `documentLeafPaths`
  extraction) are the implementation; `internal/uiapi/patch_service_test.go`
  covers CRUD round-tripping, a CRD-rejected write surfacing as
  `CodeInvalidArgument`, strict field validation on both writes, and
  `PreviewImpact`'s three outcomes (none, unsupported, install-time-only
  kept separate from reboot-required).
- `cmd/ichor-ui/main.go` registers `PatchServiceHandler` on the same mux
  as the other three services.
- `ui/` gained only its generated TS client (`ui/src/gen/ichor/v1alpha1/
  patch_pb.ts`, committed per ADR 0021's existing policy); no screen in
  this unit, per the task's own boundary.
- `internal/controller/`, `internal/cli/`, `pkg/` (beyond reading
  `pkg/fieldmeta`) and `proto/`'s other files are untouched.
