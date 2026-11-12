# 0072. The patch screens explain themselves; documents are cards, and the preview nudges without blocking

Status: Accepted · Date: 2026-10-26 · Design section: CLI and UI, Patch layers
Decided: The patch edit page opens with a short explanation of what a patch is, describes each layer and the priority rule next to the fields, shows each document as a card (kind and key fields, with Edit and Remove) whose editor says what the chosen kind configures and names its fields, and reminds the user to run the impact preview when it is missing or out of date, without ever blocking Save; the list adds what each patch targets, as written

## Context

The page from ADR 0039 was correct but bare: four fields, a JSON box per document and a preview button. A new user could not tell what a layer was, which patch wins, what a kind such as `UserVolumeConfig` configures, or whether the preview mattered.

## Decision

- **Wording lives next to the field.** Layer descriptions, the priority rule ("a higher number is applied later, so it wins": `pkg/render` sorts ascending by layer, then priority) and per-kind field lists are `t()` keys beside the related patch keys. Field help is written by hand from the Talos types, one line per top-level field, in `ui/src/lib/patchDocs.ts`; it is not read from the CRD schema, which ADR 0039 already rejected as a generic renderer.
- **Documents stay JSON on the wire and in the editor**, as ADR 0039 decided (the server validates). The one exception is `SysctlConfig`, edited as key/value rows because it is the commonest and simplest document; the rows are kept in the component so a row with an empty key does not vanish before it is typed.
- **Cards, not always-open editors.** A loaded document is collapsed to its kind and a summary (`summarizeDocument`); a new document opens in its editor. Documents get a client-side id so removing one does not shift another's open state.
- **The preview is a nudge.** The page remembers the form state a preview was run for (`previewKey`). A missing or stale preview shows a line in the preview card and a reminder above Save. Save is never disabled by it: a preview is a round trip that can fail for reasons unrelated to the patch.
- **List page machines.** The "Applies to" column shows the patch's target as written, not computed machine names, so the UI never re-implements the controller's selection: every machine for a cluster-layer patch, `Role: <role>`, `Machine: <name>`, or "Machines matching the selector" for a label selector.
- **The layer decides the target.** `role` takes `target.role`, `selector` takes `target.labelSelector`, `machine` takes `target.machineRef`, `cluster` takes no target. The CRD enforces it in CEL (before, only "target present iff not cluster" was checked, and `pkg/render` silently matched nothing for a mismatch); the form has no separate target-kind control, choosing a layer shows that one input and clears the old target, and the machine picker lists the chosen cluster's machines.

## Consequences

- New: `ui/src/components/PatchDocumentCard.tsx`, `ui/src/lib/patchDocs.ts` and its test. No preactcn component was added.
- No API or proto change.
