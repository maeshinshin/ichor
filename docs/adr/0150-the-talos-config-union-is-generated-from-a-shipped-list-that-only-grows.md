# 0150. The TalosConfigPatch union is generated from a shipped list that only grows

Status: Accepted · Date: 2026-11-12 · Design section: Typed configuration · Builds on: 0003, 0012, 0149

Decided: the kinds that have a field in `TalosConfigPatchDocument` are the entries of `shipped` (`internal/tools/mirrorgen/shipped.go`), an ordered, append-only list. Everything else about a shipped kind (its Talos package, document type, kind constant and API version) is read from Talos's `registry.Register` calls, and mirrorgen writes the union struct, its `kind` enum and its two CEL rules into `api/v1alpha1/zz_generated.talosconfigpatchdocument.go`. Classifying a kind in `kindClasses` stays the only decision a person makes about it; shipping it is appending its name to `shipped`. A shipped kind is never dropped, whatever its class becomes.

## Context

ADR 0149 classified every Talos document kind. Putting an `accept` kind into `TalosConfigPatch` still took edits in three places: the generator's `roots` (package, type, kind constant, API version), the union struct and its enum in `api/v1alpha1`, and two CEL rules that list every kind. Two of the three pairs were checked against each other; `kindClasses` and `roots` were not, so a kind marked `accept` and never added to `roots` was silently unsupported.

## Decisions

1. **The per-kind data is derived.** mirrorgen loads `config/types/...` and reads each `registry.Register(kind, func(version) {...})` call: the kind constant (package and name, for `meta.Meta`), the version the factory switches on, and the document type it returns. The only value that cannot come from there is the name Ichor gives the legacy `v1alpha1` document: its registry key is `v1alpha1`, Ichor calls it `Config` (`legacyKind`, `legacyName`). It also registers a string literal, not a constant, so it has no `meta.Meta` to set.
2. **`shipped` is a list, not a column of `kindClasses`.** Field order is part of the generated API and of the committed CRD, and a map has none. Each entry is the registered kind plus, optionally, the phrase its field doc uses after "is a Talos" (the existing descriptions are hand-written and part of the CRD). Appending an entry and running `make generate manifests` is the whole change.
3. **The union is generated Go, not a CRD patch.** mirrorgen writes the struct with its `+kubebuilder` markers, and controller-gen builds the CRD from it as before. The field's JSON name is the kind with its leading capitals lowered (`SysctlConfig` is `sysctlConfig`).
4. **Append-only is held by `shipped`, and the generator refuses to break it.** The generator does not read `kindClasses` to decide whether a field exists: a shipped kind reclassified to `secret`, `ichor-owned` or `unstructured` keeps its field, its enum value and its place in both rules, and the generator prints a note that a shipped kind is no longer `accept`, so the controller can be made to refuse it. A shipped kind that Talos stops registering, or that loses its `kindClasses` row, fails generation: its mirror type can no longer be built, and silently omitting the field would drop it. The fix there is to keep the field by hand, not to delete the entry. The generated CRD's `kind` enum is checked against `shipped` by `TestShippedMatchesCRDEnum`, and `TestReclassifiedKindKeepsItsField` fixes the reclassification rule.
5. **`accept` and shipped are different states.** An `accept` kind that is not shipped is a decision made and not yet rolled out; the generator prints how many there are. This is not the "not yet" class ADR 0149 rejected: that was for a kind nobody had decided. Whether to roll them all out, or only those a real cluster needs, is a separate decision. Rolling out every `accept` kind would delete `shipped` and make `accept` itself the gate.
6. **This ADR adds no kind.** The seven shipped kinds are the seven of before, and the union generated for them is the struct, enum and rules that were written by hand.

## Rejected

- A `shipped` flag on each `kindClasses` row: no field order, and a reclassified row would be the one place the flag could be dropped by editing the row.
- Reading the committed CRD to learn what to keep: it is itself generated, so it follows `shipped` and cannot be a second source.
- Emitting only the CEL rules and leaving the struct hand-written: the struct is where the per-kind field and its type live.

## Consequences

Adding a kind is two lines: its `kindClasses` row and its `shipped` entry. The CEL rules grow with the number of shipped kinds, and the API server rejects a rule whose estimated per-element cost exceeds 10 million. The kind-to-field rule, a chain of conditionals, reaches that at 12 kinds and the exactly-one-field rule at 21; at the 67 `accept` kinds the first is 6.6 times over and the second 3.2 times. Both CRDs that embed the union (TalosConfigPatch and TalosCluster) count the rules against their own total budget. The seven kinds shipped now fit, with room for four more of the kind-to-field rule. Going past that needs the rules shortened or the agreement check moved elsewhere (the renderer already enforces it), which this ADR does not decide.
