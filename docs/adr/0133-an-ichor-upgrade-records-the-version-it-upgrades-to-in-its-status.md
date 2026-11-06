# 0133. An Ichor upgrade records the version it upgrades to in its status

Status: Accepted · Date: 2026-11-06 · Design section: Operations · Builds on: 0043, 0108

Decided: An `ichor` TalosOperation carries `status.toVersion`, the controller copies `IchorInstallation.spec.version` into it once, when it first advances the upgrade, and never rewrites it; the UI and CLI show it for every phase.

## Context

The operation records where an Ichor upgrade started (the `ichor.maesh.dev/from-version` annotation) but not where it goes. The target was read from the live installation, which a later upgrade changes: a Completed operation could not show a target without showing the wrong one, and the Operations list showed identical "Upgrade Ichor" rows.

## Decision

1. `status.toVersion` is an optional string, appended to the status (CRD fields are append-only).
2. It is stamped in status, not spec: the controller never mutates `spec`, and the value is an observation of what the installation asked for at that moment, not a request.
3. It is set once, the first time the controller advances the operation, and never rewritten. A later change of `spec.version` starts a new operation; it must not rewrite the history of this one.
4. The UI summary reads "Upgrade Ichor from vX to vY" (or "to vY" without a from-version) and the detail "Target Ichor version" shows the stamp. Operations without a stamp (started before this ADR) fall back to the live installation target only while active; finished or paused ones show no target, as before.

## Alternatives rejected

- An annotation set by the UI/CLI at creation, like the from-version: `kubectl` creates would miss it, and the installation could change before the controller starts.
- Reading the installation live: wrong for any operation that is not the latest.

## Risks

An operation that started before the upgrade of Ichor to a version with this field has none; it shows what it did before.
