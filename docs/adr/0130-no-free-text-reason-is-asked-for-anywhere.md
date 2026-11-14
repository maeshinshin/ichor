# 0130. No free-text reason is asked for anywhere

Status: Accepted · Date: 2026-11-05 · Design section: Trust, Validation, UI · Touches: 0018, 0021, 0065, 0085, 0089, 0109, 0110, 0115, 0122

Decided: nowhere in Ichor does a person have to, or get asked to, type a reason. The user decided on 2026-11-05 that every place that asks for a reason goes. What gates a dangerous action does not change: the RBAC verbs (`override`, `start`, `approve`), the typed cluster or machine name, the `--yes` prompt, and the Events and audit records that say who did what. Only the free text goes: the CLI has no `--reason` and no `--override-reason`, `--skip-signature-check` is a bare boolean flag, the UI dialogs have no reason box, no CRD rule requires a reason, and no controller or backend code reads or prints text a person typed as a reason. Every reason field of a CRD or a Connect message stays (append-only) as a deprecated, unused field.

## Context

ADR 0065 made the override of a safety check carry a reason, ADR 0110 made the reason optional, ADR 0021 added an optional reason to approve and reject, ADR 0089 required one on an etcd restore, and ADR 0122 required one to skip the installer image's signature check. The user asked for all of it to go. The reasons were never the gate: the verb is (ADR 0110), and a typed reason pushed people to type placeholders. The records that say who acted, when, and on what, stay; they do not need a sentence from the actor.

## Inventory

A reason a person typed, before this change:

| Where | What | Now |
| --- | --- | --- |
| `IchorInstallation.spec.installerSignature.reason`, CEL "reason is required when mode is skip" (ADR 0122) | required with `mode: skip` | CEL rule removed; field optional, deprecated, never read |
| `IchorInstallation.status.installerImage.reason` | the reason copied from the spec | deprecated, no longer written |
| `TalosOperation.spec.etcdRestore.reason`, `MinLength=1`, required (ADR 0089, 0110) | `no reason given` stored when none was typed | optional (`omitempty`), deprecated, no longer written |
| `TalosOperation.spec.node.overrideReason` (ADR 0110) | the override's optional reason | deprecated, never set or read; a value written by an older build still counts as an override |
| `TalosOperation.spec.node.breakGlassReason` (ADR 0109) | the earlier name | as above |
| `TalosMachine.spec.approvalDecision.reason` (ADR 0021) | approve and reject reason | deprecated, no longer written, no longer sent to clients |
| Connect `Override.reason`, `ApproveMachineRequest.reason`, `RejectMachineRequest.reason`, `SetMachineInstallRequest.reason`, the four discovery requests' `reason`, `MachineApprovalDecision.reason`, `IchorInstallation` response's `installer_skip_reason` | the wire side of the above | `[deprecated = true]`, never read, never set |
| Connect `IchorInstallerOptions.skip_signature_reason` | skip with a reason | deprecated; new `bool skip_signature = 3`; a non-empty old value still means skip, its text is dropped |
| CLI `--override-reason`, `approve node --reason`, `reject node --reason`, `--skip-signature-check REASON` | flags | removed; `--skip-signature-check` is a boolean |
| UI approve, reject, node action, etcd restore and override dialogs, the skip-signature field | textareas and an input | removed, with the i18n keys only they used; the Rejected list drops its Reason column |
| Override and restore Events (`... under override: <reason>`), the audit record's reason, the etcd restore marker's `reason`, the `InstallerSignatureSkipped` Event | text that came from the field | the Events stay and say who did what; the marker has no reason |
| `internal/webhook` settings gate: `spec.installerSignature.reason` | a gated setting | removed from the gate; `mode` is still gated by the `version` subresource |

Machine-generated text is not touched: condition `reason` (`status.conditions[].reason`), `status.failureReason`, `unavailable_reason`, the `reason` of an Event, the cni `OwnedKey.reason`, the Ready reasons, the AuditRecord's `reason` when the backend itself adds a remark. `AuditRecord.reason` is kept without a deprecation mark: it is empty, or says that the Event of an override could not be recorded.

## Decision

1. **CRDs.** No field is removed. `etcdRestore.reason` becomes optional and loses `MinLength=1`; the CEL rule on `installerSignature` is dropped. Each field's comment says "deprecated: unused, kept so the schema never drops a field". `Overrides()` keeps counting `overrideReason` and `breakGlassReason` as an override, so an operation an older build wrote keeps its gate. `EffectiveOverrideReason()` is deleted.
2. **Proto.** The reason fields keep their numbers and are marked deprecated; nothing sets or reads them. The one exception: `skip_signature_reason` is read as "skip", because an older CLI or UI sends it; its text is dropped. The new `skip_signature` carries the skip.
3. **CLI.** The flags above are removed without an alias (as ADR 0109 did for the earlier flag). `--override` and `--skip-signature-check` are booleans. `--yes`, the typed names and `--confirm` stay.
4. **UI.** The dialogs lose the reason inputs; the override checkbox, the typed name and the skip checkbox stay, with the hint that the override is recorded in an Event.
5. **Events and audit.** The override Event says who asked and what, with no reason clause. The audit record's action stays (`create-override`).
6. **Docs.** The guides, the CHANGELOG and the older ADRs (by amendment) no longer tell anyone to give a reason.

## Alternatives considered

- **Keep the reason optional and only drop the requirements.** Rejected: the user asked for the inputs to go, and an optional box still invites a typed placeholder.
- **Remove the fields from the CRDs and the proto.** Rejected: CRD fields are append-only (installed builds have them), and a removed proto field number could be reused by mistake.
- **Keep the reason in the audit trail for the dangerous operations only (reset, restore, skip).** Rejected: it is the same free text; the verb and the Event already record who and when.

## Compatibility

- **Existing objects.** A `TalosOperation` or `IchorInstallation` written with a reason still validates; the reason is ignored. A TalosMachine's old `approvalDecision.reason` stays in the object and is not shown.
- **Old clients, new server.** An older CLI or UI that still sends `Override{reason}` or `skip_signature_reason` works: the override is the message's presence, a non-empty skip reason means skip. An older client's approve or reject with a reason is accepted and the reason ignored.
- **New clients, old server.** A server built before this change refuses an etcd restore without a reason (CRD) and a skip without one. Update the server first.
- **Scripts.** `--override-reason`, `--reason` and `--skip-signature-check "<text>"` (cobra reads the text as an argument, which the boolean flag rejects) stop working. Dropping them is the one change needed.
- **Restore marker.** A restore marker written by an older controller holds a `reason`; the newer one ignores it.

## Consequences

- The safety of an override rests on the verb and the typed name alone, as ADR 0110 already said.
- The Events and the audit record say who acted and when; they cannot say why.
- `status.installerImage.verification: Skipped` and the `InstallerSignatureSkipped` Event are the record of a skipped check; the digest is in both.
- The deprecated fields clutter the schema forever. That is the cost of append-only CRDs.
