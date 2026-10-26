// No import from "@/gen" or "@bufbuild/protobuf" here, by design: this
// file is loaded directly by node --test (no-framework choice for ui/src/lib tests), and the generated client
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
// JSON blob in a Textarea, let the server validate it" choice still applies, since there's no editor library to
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

export function parseLabelSelector(text: string): LabelSelectorLike {
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error('Label selector must be valid JSON')
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw new Error('Label selector must be a JSON object with "matchLabels" and/or "matchExpressions"')
  }
  const obj = parsed as Record<string, unknown>
  return {
    matchLabels: (obj.matchLabels ?? {}) as Record<string, string>,
    matchExpressions: (obj.matchExpressions ?? []) as LabelSelectorLike['matchExpressions'],
  }
}

// buildPatchTarget turns the form's target fields into the wire target.
// requiresTarget is false for the cluster layer (no target). Throws
// parseLabelSelector's error on malformed JSON, so the caller can refuse to
// send anything: the one client-side check that stops a bad request.
export function buildPatchTarget(
  requiresTarget: boolean,
  kind: TargetKind,
  role: string,
  labelSelectorText: string,
  machineRef: string,
): TargetLike | undefined {
  if (!requiresTarget) return undefined
  if (kind === 'role') return { role, labelSelector: undefined, machineRef: '' }
  if (kind === 'machineRef') return { role: '', labelSelector: undefined, machineRef }
  if (kind === 'labelSelector') return { role: '', labelSelector: parseLabelSelector(labelSelectorText), machineRef: '' }
  return undefined
}

// canSavePatch gates the Save button: layerSet and clusterLayer are the
// caller's reading of the ConfigLayer enum (kept out of here so this file
// stays free of ui/src/gen imports).
export function canSavePatch(f: {
  name: string
  hasCluster: boolean
  layerSet: boolean
  clusterLayer: boolean
  targetKind: TargetKind
}): boolean {
  return f.name !== '' && f.hasCluster && f.layerSet && (f.clusterLayer || f.targetKind !== 'none')
}
