# 0109. The safety-check override (formerly ADR 0065's mechanism) is named override

Status: Accepted · Date: 2026-11-03 · Design section: Trust, Validation · Touches: 0065, 0085, 0089, 0094, 0101, 0107, 0108, 0110

Decided: the permission-gated override of a safety check with a recorded reason (ADR 0065, amended by 0101, 0107 and 0108) is called "override" everywhere, and the earlier name is removed from code, manifests, the UI, the CLI and the docs. The mechanism is unchanged: the user writes a reason, Ichor asks Kubernetes with a SubjectAccessReview made as that user whether they hold the verb, and the reason goes into the audit record and an Event. The CLI flag is `--override` (the reason is `--override-reason` and is optional, ADR 0110), the RBAC custom verb is `override`, and the old verb is no longer accepted.

## Context

The user decided on 2026-11-03 that the previous name should disappear from Ichor entirely. The behaviour stayed right; only the word changed.

## Decision

Renames, all in one change:

- CLI: the flag `--override "<reason>"` replaces the earlier flag, with no alias. `ichor node reset` and the live etcd restore need it exactly as before.
- RBAC: the custom verb is `override` on `ichor.maesh.dev` resources, in every shipped role (`config/rbac/override_role.yaml`, the chart's `ichor-override-role`, the installer's role list) and in every SubjectAccessReview. The old verb is not accepted.
- Connect API: the message is `Override` and the request fields are `override` and `override_required`. The field numbers are kept, so the binary wire format is unaffected. The JSON names change; the UI and CLI are built from the same proto and ship together.
- Go: `Override*` and `override*` identifiers, `OverrideVerb`, `OverrideHandler`, `SetupOverrideWebhookWithManager`; files `internal/webhook/v1alpha1/override_*.go` and `internal/uiapi/override.go`.
- Admission: the webhook path is `/validate-ichor-maesh-dev-v1alpha1-override` and the webhook names are `voverride` and `voverrideop`. The installer re-applies the webhook configuration, so the paths change together with the controller image.
- Environment: `ICHOR_OVERRIDE_EXEMPT_USERS` replaces the earlier exempt-users variable.
- Event and audit reasons, UI text ("Override" with the reason prompt) and the docs use the new word. ADR 0065's file is now `docs/adr/0065-override.md`, ADR 0107's file is `docs/adr/0107-reset-and-add-replaces-the-override-reclaim-switch.md`, and the other ADRs say "override" in their text with the same meaning.

## Compatibility

- CRD fields are append-only (installed builds, including the demo, have the reason field on `TalosOperation.spec.node`). `spec.node.overrideReason` is added. The earlier field, `spec.node.breakGlassReason`, stays in the schema only as a deprecated alias: its doc comment says "Deprecated: use overrideReason", the controller and the operation webhook read it when `overrideReason` is empty (the webhook gates it with the same verb), and Ichor's own CLI and UI never set it. This is the one place the earlier name remains.
- Installed bindings that grant the earlier verb stop working. Ichor's own roles are re-applied by the installer or `helm upgrade`, so they move to `override` by themselves. For roles people wrote, `ichor upgrade ichor` prints a NOTICE that lists every ClusterRole and Role (not labelled `app.kubernetes.io/name=ichor`) that grants the earlier verb on `ichor.maesh.dev`, and tells the user to change it to `override`. The notice is advice; the upgrade goes on. The CLI therefore carries that one literal verb string for the check.
- The CLI's scheme gains `rbac.authorization.k8s.io/v1` so it can list the roles.

## Consequences

- A user who scripted the old flag or verb must change both once. There is no alias, on purpose.
- Amended by ADR 0110: the flag no longer takes the reason. `--override` is a boolean and `--override-reason` carries the optional reason; `spec.node.override` is added, and a reason alone still counts as an override. Where this record says `--override "<reason>"`, read `--override` with `--override-reason`.
- During a rolling upgrade the webhook configuration and the controller image change together; a request made in that window can be refused and succeeds when repeated.
