# 0169. `TalosStorage` keeps its name: the rename to `TalosStoragePreset` is reverted

Status: Accepted · Date: 2026-11-17 · Design section: Storage · Supersedes: 0168

Decided: ADR 0168's rename is reverted. The kind is `TalosStorage` again, as in v0.2.x, with everything else v0.3.0 added; the installer's refusal and the chart's guard that existed only for the rename are removed.

## Context

ADR 0168 renamed `TalosStorage` to `TalosStoragePreset` and made the installer refuse an install while any object of the old kind remained, because the new kind reads no old object.

The new name says "preset" twice. Its field is `spec.diskPreset`, so `TalosStoragePreset.spec.diskPreset` repeats the word. `TalosStorage` with a `diskPreset` field reads correctly: the storage configuration, using this preset.

No release carries the new name. v0.3.0 is unreleased: the latest Release run is for v0.2.4, and no v0.3.0 tag exists. No user has seen `TalosStoragePreset`, so reverting it changes only the repository.

Keeping it would have made every v0.2.x user delete their `TalosStorage` objects before upgrading, with an installer refusal to enforce it and no conversion to help. Against that cost the gain was cosmetic.

## Decision

1. The kind is `TalosStorage`, as in v0.2.x: the CRD and its plural, the short name `tsto`, the Go types, the controller, the RBAC roles named `ichor-talosstorage-{admin,editor,viewer}-role`, the proto and the UI. Nothing in the rename remains.
2. Everything v0.3.0 added stays: the `rook-v1` preset of ADR 0167, the `diskSelector` rules that ADR 0167 amended, and the rest.
3. The installer's retired-kinds refusal, the chart's hook Job and its ClusterRole, the `--check-retired-kinds` mode, and the three `renamedObjects` entries for the helper ClusterRoles are removed. `renamedObjects` keeps the break-glass entry it had before ADR 0168.
4. ADR 0168 is superseded by this record. ADR 0023's `Status:` line drops its reference to ADR 0168 and keeps the one to ADR 0167.

## Consequences

- No object is migrated and no install is refused. An install or upgrade from v0.2.x keeps its `TalosStorage` objects as they are.
- The CI diagnostic for a failed Helm deploy no longer names the guard. It prints the pods and their logs, the events and the Helm release status.
- The fixes made while debugging the guard, such as selecting Job pods by the `batch.kubernetes.io/job-name` label, stay: they do not depend on the rename.
- A future rename has to show that the new name says something its field does not.

## Alternatives considered

- Keep `TalosStoragePreset` and ship ADR 0168 as built. Rejected: it makes every v0.2.x user delete objects before upgrading, for a name that repeats its field.
- Keep `TalosStoragePreset` and add a conversion from `TalosStorage`. Rejected: it adds a migration path and a second kind to maintain, for the same cosmetic gain.
