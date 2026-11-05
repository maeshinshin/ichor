# 0110. The safety-check override is a flag, and its reason is optional

Status: Accepted · Date: 2026-11-03 · Design section: Trust, Validation · Touches: 0065, 0085, 0089, 0107, 0109

Decided: the override of a safety check is requested with a flag (`--override`, `override` on a node operation), and a reason is optional, recorded with `--override-reason` or `overrideReason`; the RBAC verb `override` stays the only gate, and an override with no reason is recorded as "no reason given".

## Context

ADR 0065, as renamed by ADR 0109, made the override a string: `--override "<reason>"` on the CLI, `Override{reason}` on the API, and `spec.node.overrideReason` with MinLength=1 as the signal itself. A non-empty reason was what turned the override on, and the etcd restore spec required one. The user asked on 2026-11-03 that the override be usable without a reason.

The reason was never the gate. The gate is the Kubernetes verb `override`, checked as the person making the request. A typed reason adds friction in an incident without adding any authority, and it pushed people to type placeholders, which made the record no more useful. The decision to override, and who made it, still needs to be recorded; only the requirement to explain it goes.

## Decision

1. **The request is a flag.** `--override` is a boolean on the CLI. A node operation carries `spec.node.override` (boolean, appended after `overrideReason`). On the API the `Override` message is sent when the caller wants an override, and its presence is the request. A request overrides when `override` is true, or when a reason is set in `overrideReason` or in the deprecated `breakGlassReason`.
2. **The reason is optional and only recorded.** `--override-reason <text>` on the CLI, `overrideReason` on the CRD (its MinLength=1 stays; an empty value is omitted), and `Override.reason` on the API. The CLI refuses `--override-reason` without `--override`, so a reason is never dropped silently.
3. **RBAC is the gate, with or without a reason.** The same `SelfSubjectAccessReview` (in the handlers) or `SubjectAccessReview` (in the webhook) asks for the verb `override` on the same resource and name, whenever the request overrides. The affected operations are unchanged: reset, reboot or shutdown past the last-worker guard, and the etcd restore, which is always an override.
4. **The record says plainly what was given.** The audit record's reason, the Warning Event (`under override: <reason>`) and the reason stored on an etcd restore say `no reason given` when none was typed. The etcd restore's spec `reason` is required by the CRD, so it stores that text rather than an empty value; the schema does not change.
5. **The UI** offers "Override the safety check" as a checkbox that gates Start, with an optional reason box below it. The checkbox is the consent; the reason is not required.

## Why this shape

- The verb already says who may override. Asking for a reason on top of it adds nothing a permission check does not already settle.
- The request still records who overrode and when, through the audit record, the Event and the API server's own audit log. The "no reason given" text marks the record for an auditor who wants a reason.
- A flag replaces a value because a value made the reason the switch. Keeping the reason separate lets the switch be one word.

## Compatibility

- **Existing operations.** An operation written with `overrideReason`, or with the deprecated `breakGlassReason`, still overrides and is still gated by the same verb. `Overrides()` counts a reason as an override, so nothing that was gated becomes ungated.
- **Existing CRD.** `override` is appended. No field is removed or renamed. An object written before it has `override` false, and whatever reason it carries still gates it.
- **Old clients.** An older CLI or UI sends `Override{reason}` with a non-empty reason, which the new server reads as an override with that reason. The Connect message shape is unchanged; only the comments on it change.
- **New clients against an older server.** A reason-less override is refused by a server built before this decision ("needs an override reason"). The fix is to add `--override-reason` and retry, or to update the server.
- **Scripts.** `--override <reason>` no longer takes a value. Moving the reason to `--override-reason` is the one change needed. There is no alias, as ADR 0109 chose for the earlier flag.

## Consequences

- The verb is the only gate for an override. Anyone holding it can override without giving a reason, and the record will say so.
- The reason stays available for an incident that needs one, and is recorded whenever it is typed.
- The etcd restore keeps a required `reason` in its spec, now filled with "no reason given" when none was typed, so the restore record always has a value.
- The Connect messages keep their names; the proto comments and the generated code change only where their text does.

Amendment 2026-11-05 (ADR 0130): the optional reason is gone too. Decisions 2, 4 and 5 no longer hold: there is no `--override-reason`, `Override.reason` and `overrideReason` are deprecated and unused, the audit record and the Event no longer say `no reason given` (they say nothing about a reason), the etcd restore's `spec.etcdRestore.reason` is optional and not written, and the UI's override checkbox has no reason box. Decisions 1 and 3 (the flag, and the verb as the only gate) stand.
