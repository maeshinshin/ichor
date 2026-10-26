// No import from "@/gen" or "@bufbuild/protobuf" here, by design: this
// file is loaded directly by node --test (ADR 0021's own no-bundler,
// no-framework choice for ui/src/lib tests), and the generated client
// (ui/src/gen) uses plain, extensionless relative imports among its own
// files that only a bundler's module resolution understands - Node's own
// ESM resolver can't follow them. The "Like" interfaces below are
// structurally compatible with the generated PatchTarget/LabelSelector
// types (and their Init-shaped, "$typeName"-free form a fresh form builds),
// so callers in ui/src/pages pass either one straight through untouched.
interface LabelSelectorLike {
  matchLabels?: Record<string, string>
  matchExpressions?: { key?: string; operator?: string; values?: string[] }[]
}

interface TargetLike {
  role?: string
  labelSelector?: LabelSelectorLike
  machineRef?: string
}

export type TargetKind = 'none' | 'role' | 'labelSelector' | 'machineRef'

// targetKindOf reads back which of PatchTarget's three mutually-exclusive
// fields (patch.proto's own "exactly one" comment, enforced by the CRD's
// CEL rule on write - not re-checked here) is populated, so the edit form
// can show the right sub-fields for an existing patch.
export function targetKindOf(target: TargetLike | undefined): TargetKind {
  if (!target) return 'none'
  if (target.role) return 'role'
  if (target.labelSelector) return 'labelSelector'
  if (target.machineRef) return 'machineRef'
  return 'none'
}

// LabelSelector is a structured wire message (a map plus a list), not an
// opaque string like PatchDocument.document - but the same "edit as one
// JSON blob in a Textarea, let the server validate it" choice (ADR 0039's
// own design question 2) still applies, since there's no editor library to
// build a dedicated matchLabels/matchExpressions form with. Unlike
// PatchDocument.document, turning the text back into a wire message needs a
// JSON.parse here; that's syntax only, not a second copy of any CRD rule.
export function stringifyLabelSelector(ls: LabelSelectorLike | undefined): string {
  return JSON.stringify(
    { matchLabels: ls?.matchLabels ?? {}, matchExpressions: ls?.matchExpressions ?? [] },
    null,
    2,
  )
}

// The message stays English for logs and tests; `reason` is what the screen
// translates (docs/adr/0064).
function selectorError(message: string, reason: 'selectorNotJson' | 'selectorNotObject'): Error {
  return Object.assign(new Error(message), { reason })
}

export function parseLabelSelector(text: string): LabelSelectorLike {
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw selectorError('Label selector must be valid JSON', 'selectorNotJson')
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw selectorError('Label selector must be a JSON object with "matchLabels" and/or "matchExpressions"', 'selectorNotObject')
  }
  const obj = parsed as Record<string, unknown>
  return {
    matchLabels: (obj.matchLabels ?? {}) as Record<string, string>,
    matchExpressions: (obj.matchExpressions ?? []) as LabelSelectorLike['matchExpressions'],
  }
}

// The layer drives the target (ADR 0072): the CRD accepts role <-> target.role,
// selector <-> target.labelSelector, machine <-> target.machineRef and cluster
// <-> no target, nothing else. The page maps its ConfigLayer enum to this key
// (this file takes no ui/src/gen import).
export type LayerKey = 'unset' | 'cluster' | 'role' | 'selector' | 'machine'

export function targetKindForLayer(layer: LayerKey): TargetKind {
  switch (layer) {
    case 'role':
      return 'role'
    case 'selector':
      return 'labelSelector'
    case 'machine':
      return 'machineRef'
    default:
      return 'none'
  }
}

// targetFieldsFor loads an existing patch's target into the form fields for
// its layer: only the field the layer uses is filled, so an incompatible
// leftover can never be sent back. Also the "clear" a layer change applies
// (call it with undefined).
export function targetFieldsFor(
  layer: LayerKey,
  target: TargetLike | undefined,
): { role: string; labelSelectorText: string; machineRef: string } {
  const kind = targetKindForLayer(layer)
  return {
    role: kind === 'role' ? (target?.role ?? '') : '',
    labelSelectorText: stringifyLabelSelector(kind === 'labelSelector' ? target?.labelSelector : undefined),
    machineRef: kind === 'machineRef' ? (target?.machineRef ?? '') : '',
  }
}

// buildPatchTarget turns the form's target fields into the wire target for
// the layer; the cluster layer has none. Throws parseLabelSelector's error on
// malformed JSON, so the caller can refuse to send anything: the one
// client-side check that stops a bad request.
export function buildPatchTarget(layer: LayerKey, role: string, labelSelectorText: string, machineRef: string): TargetLike | undefined {
  switch (targetKindForLayer(layer)) {
    case 'role':
      return { role, labelSelector: undefined, machineRef: '' }
    case 'machineRef':
      return { role: '', labelSelector: undefined, machineRef }
    case 'labelSelector':
      return { role: '', labelSelector: parseLabelSelector(labelSelectorText), machineRef: '' }
    default:
      return undefined
  }
}

// canSavePatch gates the Save button: a layer is chosen and the target value
// that layer asks for is filled (a selector always has a value: its JSON).
export function canSavePatch(f: { name: string; hasCluster: boolean; layer: LayerKey; role: string; machineRef: string }): boolean {
  if (f.name === '' || !f.hasCluster || f.layer === 'unset') return false
  if (f.layer === 'role') return f.role !== ''
  if (f.layer === 'machine') return f.machineRef !== ''
  return true
}
