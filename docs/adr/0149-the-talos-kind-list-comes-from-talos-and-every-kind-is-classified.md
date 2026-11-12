# 0149. The Talos kind list comes from Talos, and every kind is classified

Status: Accepted · Date: 2026-11-12 · Design section: Typed configuration · Builds on: 0003, 0012

Decided: the set of Talos config document kinds is read from the `registry.Register` calls in machinery's `config/types` tree, not written by hand, and every one of them has a row in `kindClasses` (`internal/tools/mirrorgen/kinds.go`) with a class and a reason: `accept`, `secret`, `ichor-owned` or `unstructured`. A test fails, naming the kinds, when Talos registers one with no row (or a row names one it no longer registers). `SwapVolumeConfig` is `accept` with its `KeyStatic` field dropped.

## Context

`TalosConfigPatch.spec.documents[]` takes an allow-list of kinds (ADR 0003), and the list is written by hand in two places. A Talos release that adds a document kind goes unsupported without anyone deciding that it should. Machinery v1.14.5 registers 90 kinds, and the aim is to support all of them except those Ichor must not or cannot accept.

## Decisions

1. **The list is derived.** The registry package is `internal` to machinery, so its `Kinds()` cannot be imported. The test loads `config/types/...` with `go/packages`, finds every call to that package's `Register`, and takes the first argument's constant value from the type checker. An argument that is not a constant string fails the test rather than being skipped.
2. **One row per registered kind, keyed by the string Talos registers.** The legacy machine and cluster document registers as `v1alpha1`; Ichor calls it `Config`.
3. **Four classes.**
   - `accept`: Ichor represents the kind in `TalosConfigPatch`.
   - `secret`: the document carries credentials, so Ichor refuses it. Every kind that implements machinery's `SecretDocument` and is neither a volume document nor the legacy `v1alpha1` document (both are mirrored with their secret fields dropped) is in this class.
   - `ichor-owned`: Ichor renders the document itself from a spec field, so a patch must not set it.
   - `unstructured`: the document is raw YAML or text with no schema to mirror.
4. **`SwapVolumeConfig` is `accept` with `KeyStatic` dropped.** The volume documents carry a static disk-encryption passphrase only as one key source of an otherwise ordinary document, and `EncryptionKey.KeyStatic` is already dropped by the generator (ADR 0012). Swap follows the same rule instead of being refused whole.
5. **An unclassified kind fails a test.** The message lists the kinds and says what to add. A row for a kind Talos dropped fails it too.
6. **This ADR adds no kind to the CRD.** The enum and the generator's allow-list are unchanged; the table only records the decision for each kind.

## Rejected

- Importing the registry: it is `internal`, and a copy of the list would drift, which is the problem.
- A class for "not yet": a kind that is not decided is the failure the test exists to raise.
- Dropping credential fields from every `secret` kind: they are credentials by nature (a private key, a token), not a field of an otherwise usable document.

## Consequences

A machinery bump that adds a document kind fails `go test ./internal/tools/mirrorgen` until a maintainer classifies it. The table is the input for adding the `accept` kinds to the CRD; that is a separate change. A kind's class is a judgement, and a wrong one is a one-line change to the table.
