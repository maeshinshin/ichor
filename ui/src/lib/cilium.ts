// Pure helpers for the Cilium settings page. No import from "@/gen" or
// preact, so they run under `node --test`.

export type DiffKind = 'add' | 'del' | 'hunk' | 'meta' | 'ctx'

// classifyDiffLine says how a line of a unified diff is drawn. The "+++ " and "--- " file headers
// are meta, not an added or removed line.
export function classifyDiffLine(line: string): DiffKind {
  if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('diff ') || line.startsWith('index ')) return 'meta'
  if (line.startsWith('@@')) return 'hunk'
  if (line.startsWith('+')) return 'add'
  if (line.startsWith('-')) return 'del'
  return 'ctx'
}

// diffLines splits a diff into drawn lines; the trailing newline does not make an empty last line.
export function diffLines(diff: string): { kind: DiffKind; text: string }[] {
  if (diff === '') return []
  const lines = diff.split('\n')
  if (lines[lines.length - 1] === '') lines.pop()
  return lines.map((text) => ({ kind: classifyDiffLine(text), text }))
}

// canEdit: the write controls show only when the caller may update the cluster (the permission
// probe answered yes).
export function canEdit(perms: { clusterUpdate: boolean } | undefined): boolean {
  return !!perms?.clusterUpdate
}

// canApply: the text must be the exact text that was validated and diffed, and a diff must exist.
export function canApply(text: string, reviewed: { text: string; valid: boolean; diff: string } | null): boolean {
  return reviewed !== null && reviewed.text === text && reviewed.valid && reviewed.diff !== ''
}
