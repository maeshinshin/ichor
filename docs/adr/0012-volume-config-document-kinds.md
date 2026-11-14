# 0012. Mirroring the volume config document kinds for disk roles

Status: Accepted · Date: 2026-10-25 · Design section: 4.4 (Typed configuration), ### Nodes

Decided: Mirror `VolumeConfig`/`UserVolumeConfig`/`RawVolumeConfig` as three more mirrorgen roots, for disk roles

## Context

Phase 3 (disk roles, `### Nodes` in `docs/design/architecture.md`) assigns each disk a role:
`system` (install target plus EPHEMERAL), `ephemeral` (EPHEMERAL on its own disk), `storage`
(handed to a storage preset via a CSI provisioner), `userVolume`, or `unused`. `system` is
already representable — it is `machine.install`, a `v1alpha1.Config` field already on the
mirror allow-list. The other four roles are not: Talos expresses them as separate, modern
config document kinds — `VolumeConfig`, `UserVolumeConfig`, `RawVolumeConfig` — none of which
were on mirrorgen's allow-list. So, as the design already says, disk roles begin with generator
work, not controller work; this ADR is that first step.

## Decision

Add three roots to `internal/tools/mirrorgen/main.go`'s `roots`:
`block.VolumeConfigV1Alpha1` (kind `VolumeConfig`), `block.UserVolumeConfigV1Alpha1`
(kind `UserVolumeConfig`), `block.RawVolumeConfigV1Alpha1` (kind `RawVolumeConfig`), all from
`github.com/siderolabs/talos/pkg/machinery/config/types/block`. `VolumeConfig` covers `ephemeral`
(and, in principle, a non-default `system` disk layout); `UserVolumeConfig` covers `userVolume`;
`RawVolumeConfig` covers `storage` — its own doc comment says it is "intended to be used with CSI
provisioners," which is exactly what a storage preset is. `unused` needs no document at all: the
absence of an assignment is the representation.

`VolumeConfig.MetaName` (which system volume this document configures) mirrors as a plain,
unrestricted `string` — Talos's own type carries no enum here either, since `VolumeConfig` also
names volumes phase 2 never touches. **Both `STATE` and `EPHEMERAL` are representable**, the same
way, through the same field, and nothing about the mechanism is specific to either name
(`EPHEMERAL` is the role the design calls out).

These three need the same two kinds of handling ADR 0003 already has tables for, plus one the
generator had no table for:

- **Six more type-keyed substitutions**, added to `substTable`: `cel.Expression` (the disk
  selector's type — a compiled CEL AST, no exported fields, behind `TextMarshaler`, exactly
  `ByteSize`'s shape from ADR 0003's original spike); `block.Size` and `block.ByteSize`
  (private-field structs behind `TextMarshaler`, used for `minSize`/`maxSize`); and three
  `int`-backed enums with a `TextMarshaler` of their own (`block.VolumeType`,
  `block.FilesystemType`, `block.EncryptionProviderType` — all from the *separate*
  `resources/block` package, not `config/types/block`, which happens to share its bare package
  name). The enums are a judgment call rather than a hard requirement: the generic path would
  mirror them as a bare `int` and still round-trip correctly, but the CRD would then show a raw
  ordinal (`type: 2`) instead of the name Talos itself uses (`type: xfs`) — inconsistent with
  `InstallDiskType`, already mirrored as a string, and genuinely confusing in a user-facing
  field.
- **One more secret field**, added to `secretFields`: `EncryptionKey.KeyStatic`, a disk
  encryption passphrase embedded directly in the document. Talos's own `EncryptionSpec.Redact`
  method targets this exact field for the same reason Ichor drops it. The *whole* `static` key
  source is dropped, not just its one `passphrase` field, because an empty `static: {}` left
  behind would be unusable and only confusing — there would be no way to say what the key
  actually is. KMS, TPM and node-ID key sources carry no secret (an endpoint URL, hardware-sealed,
  or derived) and stay. One table entry, keyed by machinery's own type and field name (`EncryptionKey.KeyStatic`),
  drops it from both `block.EncryptionKey` and the older, already-mirrored
  `v1alpha1.EncryptionKey` (legacy per-partition disk encryption), since both share that name and
  the table is keyed by bare name, not by package.
- **A genuine name collision**, the first the generator has hit: `block.EncryptionKey`,
  `EncryptionKeyKMS`, `EncryptionKeyTPM` and `EncryptionKeyNodeID` all share a bare name with an
  existing, unrelated mirror struct generated from the legacy `v1alpha1.EncryptionKey` family
  (older, per-partition disk encryption, already mirrored via `v1alpha1.Config`). Mirror struct
  names are one flat Go namespace (`zz_generated.mirror.go`), so this is a real conflict, not a
  type-keyed or field-keyed question the existing tables could resolve. A new, small table,
  `typeRenames` (qualified machinery type name → mirror name), disambiguates it: the four
  `block`-sourced types take a `Volume` prefix; the already-established `v1alpha1` names are left
  alone. `structDef` carries a `machName` field (distinct from the possibly-renamed mirror `name`)
  so the generated conversion code still calls the real machinery type regardless of the rename.
  `EncryptionKeyStatic` itself never needed a rename: dropping `EncryptionKey.KeyStatic` means it
  is never reached in either package.

`TalosConfigPatchDocument` (`api/v1alpha1/talosconfigpatch_types.go`) gained one optional field
per new kind (`volumeConfig`, `userVolumeConfig`, `rawVolumeConfig`), three more names in the
`kind` enum, and the matching three terms in both CEL rules (exactly-one-populated,
kind-matches-field) — the same shape phase 2's three existing kinds already use.

## What the fallback chain can and can't express

The disk selector (`ProvisioningSpec.diskSelector.match`) is the one field this whole phase
turns on, since it is how a volume document says which disk it means. It mirrors as a plain
string carrying an arbitrary CEL expression, exactly like every other substTable entry: Ichor
carries the text, Talos defines what is inside it. That matters here specifically because
architecture.md's fallback chain (WWID → serial → by-id path → bus path) is not a schema Ichor
has to model — it is just a choice of which fields on Talos's own `disk.*` CEL variable (a
`block.DiskSpec` protobuf: `wwid`, `serial`, `symlinks` for by-id-style udev links, `bus_path`,
`transport`, `rotational`, `size`, …) a given expression happens to reference, and the string
field carries any of them unchanged.
A *negative* `maxSize` (`"-10GiB"`) keeps the reserve-free-space meaning architecture.md
documents through the mirror, not just the common positive case.

Nothing about the three new kinds turned out to be unrepresentable. The things phase 3 still
can't do are a different kind of gap, not a mirroring one:

- **User volumes and EPHEMERAL sharing one disk** (including what a negative `maxSize` means in
  that combination) is still an open question — `docs/design/architecture.md`'s Open questions,
  unchanged by this ADR. It needs a design decision, not a generator fix.
- **Actually applying a disk role to a node** — the reconciler that turns a `TalosMachine`'s
  approved disk roles into these documents — is controller work, not mirroring.
- **A changed selector silently doing nothing**, and a **lowered `maxSize` being silently
  ignored** (both documented in architecture.md) are
  behaviors of the *running* system, not of the CRD schema; detecting and reporting them is
  reconciler work.

## Served CRD size

`talosconfigpatches` serves **96,792 bytes, 6.2% of etcd's 1,572,864-byte
`--max-request-bytes` ceiling** (210,130 bytes on disk), the size of the
`CustomResourceDefinition` object the API server serves. The three volume kinds
add about 15 KB served between them (from 81,965 bytes, 5.2%). Headroom remains
in no way the constraint, and there is room for the rest of Talos's modern
document kinds many times over.

## `pkg/fieldmeta` rules for the three new kinds

Making the three kinds representable means a user can now write a path `pkg/fieldmeta` has no
rule for, which would silently fall through to its reboot-required default. The default being
*safe* does not make it *correct*: the dangerous direction is an install-time-only field left at
reboot-required, since a reboot that cannot possibly take effect gets re-detected as drift and
rebooted again -- a loop. The safe-but-wasteful direction is a live-safe field left at
reboot-required: an unneeded reboot, not a loop, since the reboot (if performed) still leaves the
node converged with the rendered config afterwards.

**Basis, with sources:**

- Talos's own documented no-reboot allow-list (`docs.siderolabs.com`, "Edit Machine
  Configuration" guide, v1.13) lists exact config paths (`.machine.network`, `.machine.kubelet`,
  `.cluster`, …) that a no-reboot apply accepts; it says plainly that anything outside that list
  makes a no-reboot apply "fail[] if the change contains any fields that can not be updated
  without a reboot." None of `VolumeConfig`, `UserVolumeConfig` or `RawVolumeConfig` appear on it
  at all. That alone rules out marking any of their paths `safe`.
- Talos's own volume manager
  (`internal/app/machined/pkg/controllers/block/volume_manager.go`'s `processVolumeConfig`) runs
  a state machine per volume: `Missing`/`Waiting` → `LocateAndProvision` (consults the disk
  selector) → `Located` → `Grow` → `Provisioned` → `HandleEncryption` → `Prepared` → `Format` →
  `Ready`, where `Ready` does nothing further. The disk selector and the grow step are only ever
  reached on the way to a volume's *first* `Ready`; nothing re-enters that path afterwards. This
  matches, for all three kinds (the state machine is shared, not system-volume-specific
  -- see `internal/.../volumeconfig/user_volumes.go`'s `UserVolumeTransformer`, which feeds the
  same `VolumeConfig` resource type), the behavior architecture.md already documents for
  `EPHEMERAL` specifically: a selector change on an already-provisioned volume does nothing.
- The loop concern: `internal/controller/talosmachine_controller.go`'s `switch` on `impact`
  handles `InstallTimeOnly` *before* `RebootRequired`, and that case returns through
  `r.ready(...)` with `ConditionFalse` and a "needs a reset and reinstall" message — it never
  calls `r.refuseReboot` (the only path that actually reboots a node). **An install-time-only
  classification cannot cause a reboot loop**, here or for any existing rule; it is purely
  reported.

**Classified, with the reasoning above:** `VolumeConfig.provisioning` (disk selector, min/max
size, grow) is `installTimeOnly` — the same classification `machine.install` already has, for the
matching reason architecture.md gives: changing which disk a system volume lives on is a
reset-and-reinstall operation. This is the only new rule added.

**Left at the reboot-required default, because their behaviour is not established rather than
because it is known safe:**

- `VolumeConfig.filesystem`, `.encryption` and `.mount`. The same state machine gates `Format` and
  `HandleEncryption` the same way it gates provisioning, which is suggestive, but changing
  encryption or filesystem settings is consequential enough that a suggestive reading is not
  enough to act on.
- `VolumeConfig.trim` and `.scrub`. These are read by their own continuously-scheduled
  controllers (`volume_trim.go`, `fs_scrub.go`), outside the provisioning state machine entirely,
  which argues for `safe` — but that is inference from a different controller's shape, not something Talos
  states, so it stays at the default too.
- Any `UserVolumeConfig` or `RawVolumeConfig` path. Architecture.md states that assigning
  `storage` or `userVolume` to a previously unused disk does *not* need a reset, which rules out
  `installTimeOnly`; the no-reboot allow-list finding above rules out `safe`. Reboot-required is
  each kind's correct classification by elimination, needing no rule at all —
  `TestAssess_UserAndRawVolumeConfigStayRebootRequired` pins this so a future change does not
  "fix" it into something wrong.

**`Unsupported` was considered for the maxSize-shrink case and rejected.** Architecture.md records
that a *lowered* `maxSize` on an existing volume is silently ignored (partitions only ever grow) —
a real gap, but not one `Impact.Unsupported` can hold without changing its meaning:
`Unsupported` is specifically "needs a newer Talos version than the cluster's pinned contract"
(`Assess`'s own doc comment, and every existing use of it), a per-*path*, version-gated question.
Whether a `maxSize` change is a no-op is a per-*value* question — the same shrink value is a no-op
on an existing volume and a perfectly normal initial size on a brand-new one — and `Assess` only
ever sees changed paths, never old/new values or whether the volume already exists. Overloading
`Unsupported` (or `RebootRequired`, or a new field) to mean "this specific value won't do what you
asked" would conflate two different failure modes behind one boolean, which is the kind of
silent-wrongness this project otherwise tries hard to avoid. The gap stays exactly where
architecture.md already put it: a controller-side detection problem ("Ichor has to detect that
itself and report 'accepted, no effect'"), `Impact`'s three fields are sufficient for what this needs to express; no fourth field or new
axis is added.

## Consequences

- Disk roles have a typed, schema-validated representation for everything except the
  `system`/install-target role, which already had one. The reconciler and CLI work phase 3 still
  needs can build on `documents[]` the same way phase 2's kinds already do.
- `typeRenames` and `machName` are small, general additions (not special-cased to these three
  kinds), so the next genuine name collision — and there will eventually be one, since Talos's
  document kinds are not designed with a single flat Go namespace in mind — has a place to go
  without new generator machinery.
- `secretFields` has ten entries instead of nine. One entry, keyed by machinery's struct and
  field name, catches the same field across unrelated machinery types.
