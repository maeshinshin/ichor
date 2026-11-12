# 0154. The patch document union is bounded by property count and its kind is checked by the webhook

Status: Accepted · Date: 2026-11-12 · Design section: Typed configuration · Amends: 0150

Decided: `TalosConfigPatchDocument` carries no CEL rule. The CRD bounds a document to `kind` plus one field with `minProperties: 2` and `maxProperties: 2`; that the one field is the field `kind` names is checked by `Validate`, a method mirrorgen emits next to the struct from `shipped`. The `TalosConfigPatch` webhook calls it on create and on an update that changes `spec.documents`, and `render.DocumentFromPatch` calls it before it converts a document.

## Context

ADR 0150 left the union with two generated CEL rules, one that exactly one field is set and one that `kind` names the set field, and noted that the API server rejects a rule whose estimated cost exceeds 10 million. Both grow with the number of shipped kinds. The API server's estimated cost for the kind-to-field rule passes the limit at 12 kinds and for the exactly-one rule at 21. At the 67 `accept` kinds they are 6.6 and 3.2 times over, and together they take nearly all of the CRD's 100M total. At the seven kinds shipped now they take about a tenth of it, in `TalosConfigPatch` and again in `TalosCluster`, which embeds the union in `spec.cni.custom.documents`. Without them the union costs nothing, whatever the number of kinds.

ADR 0150 also said the renderer already enforced the kind-to-field agreement. It did not: a document whose kind named an unset field converted to a typed nil and `container.New` panicked on it. The CEL rule was the only check.

## Decisions

1. **`minProperties: 2` and `maxProperties: 2` replace the exactly-one rule.** A document is `kind` and one optional field, so two properties is the same condition. The API server counts the properties of the object after pruning and after non-nullable `null` values are dropped, so an unknown field or a `null` field does not count. A document with a second field is refused as `Too many: 3: must have at most 2 items`, and one with no field as `should have at least 2 properties`.
2. **The kind-to-field agreement moves out of the CRD.** mirrorgen emits `(*TalosConfigPatchDocument).Validate` from the same `roots` as the struct: a `switch` from kind to field name and one nil test per field, so adding a kind stays the one `shipped` line. The error names the kind and the fields that disagree: `kind "SysctlConfig" needs the field sysctlConfig alone, but the document sets kernelModuleConfig`.
3. **The `TalosConfigPatch` webhook is where the API-time check lives.** It is registered for create and update with `failurePolicy: Fail`, in the chart (`webhook.enabled`, on by default) and in `config/webhook`, and it already dry-renders the patch. The check runs first, so a mismatched document never reaches the dry render. The message is prefixed with its place, `spec.documents[1]: `.
4. **`DocumentFromPatch` calls `Validate` too.** With the webhook off or bypassed, a mismatched stored document is an error from the renderer, not a panic.
5. **`TalosCluster` gets no check.** Its copy of the union is `spec.cni.custom.documents`, which the webhook already refuses on create ("deprecated and unused") and which nothing renders.

## Rejected

- Shortening the CEL: a chain of `kind == 'X' ? has(self.x)` is linear in the kinds however it is written, and the cost is per element of an unbounded list.
- `maxItems` on `documents` to cut the cardinality factor: it buys a constant factor, not a ceiling, and it limits what a patch may carry.
- A second rule only in the webhook for the exactly-one case: the property bounds already express it.

## Consequences

The ceiling on the number of kinds is gone for this union; only the size of the CRD itself is left. The cost is where a mismatch is caught: `kubectl apply` of a document whose field is not the one its kind names is no longer refused by the schema but by the webhook, and with `webhook.enabled=false` it is stored and only the renderer refuses it (the patch's machines then fail to render, with the same message). A document with two fields, or none, is still refused by the schema, now with the property-count message above instead of a sentence naming the fields.
