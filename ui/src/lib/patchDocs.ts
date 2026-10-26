// Pure helpers for the patch screens' typed documents. No DOM, no preact and
// no ui/src/gen import, so it runs under node --test.
import type { Key } from './i18n.ts'

export interface KindField {
  name: string
  helpKey: Key
}

export interface DocumentKind {
  kind: string
  descKey: Key
  // The top-level fields of the Talos document, with a line of help each.
  fields: KindField[]
}

export const DOCUMENT_KINDS: DocumentKind[] = [
  {
    kind: 'SysctlConfig',
    descKey: 'patch.kind.SysctlConfig',
    fields: [{ name: 'params', helpKey: 'patch.field.sysctl.params' }],
  },
  {
    kind: 'KernelModuleConfig',
    descKey: 'patch.kind.KernelModuleConfig',
    fields: [
      { name: 'name', helpKey: 'patch.field.module.name' },
      { name: 'parameters', helpKey: 'patch.field.module.parameters' },
    ],
  },
  {
    kind: 'Config',
    descKey: 'patch.kind.Config',
    fields: [
      { name: 'machine', helpKey: 'patch.field.config.machine' },
      { name: 'cluster', helpKey: 'patch.field.config.cluster' },
    ],
  },
  {
    kind: 'VolumeConfig',
    descKey: 'patch.kind.VolumeConfig',
    fields: [
      { name: 'name', helpKey: 'patch.field.volume.name' },
      { name: 'provisioning', helpKey: 'patch.field.provisioning' },
      { name: 'filesystem', helpKey: 'patch.field.filesystem' },
      { name: 'encryption', helpKey: 'patch.field.encryption' },
      { name: 'mount', helpKey: 'patch.field.mount' },
      { name: 'trim', helpKey: 'patch.field.trim' },
      { name: 'scrub', helpKey: 'patch.field.scrub' },
    ],
  },
  {
    kind: 'UserVolumeConfig',
    descKey: 'patch.kind.UserVolumeConfig',
    fields: [
      { name: 'name', helpKey: 'patch.field.userVolume.name' },
      { name: 'volumeType', helpKey: 'patch.field.userVolume.type' },
      { name: 'provisioning', helpKey: 'patch.field.provisioning' },
      { name: 'filesystem', helpKey: 'patch.field.filesystem' },
      { name: 'encryption', helpKey: 'patch.field.encryption' },
      { name: 'mount', helpKey: 'patch.field.mount' },
      { name: 'trim', helpKey: 'patch.field.trim' },
      { name: 'scrub', helpKey: 'patch.field.scrub' },
    ],
  },
  {
    kind: 'RawVolumeConfig',
    descKey: 'patch.kind.RawVolumeConfig',
    fields: [
      { name: 'name', helpKey: 'patch.field.rawVolume.name' },
      { name: 'provisioning', helpKey: 'patch.field.provisioning' },
      { name: 'encryption', helpKey: 'patch.field.encryption' },
    ],
  },
]

// defaultDocument is the starting text for a newly chosen kind: the shape the
// kind's required field wants, so the editor never opens on a wrong skeleton.
export function defaultDocument(kind: string): string {
  if (kind === 'SysctlConfig') return '{"params":{}}'
  if (kind === 'Config') return '{}'
  return '{"name":""}'
}

export function kindInfo(kind: string): DocumentKind | undefined {
  return DOCUMENT_KINDS.find((k) => k.kind === kind)
}

function parseObject(text: string): Record<string, unknown> | null {
  try {
    const v: unknown = JSON.parse(text)
    return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null
  } catch {
    return null
  }
}

// summarizeDocument is the one-line description of a document on the card:
// sysctl pairs, a module or volume name, or the sections a Config sets.
// Empty when the text is not a JSON object (the card then shows only the kind).
export function summarizeDocument(kind: string, text: string): string {
  const obj = parseObject(text)
  if (!obj) return ''
  if (kind === 'SysctlConfig') {
    const params = obj.params
    if (typeof params !== 'object' || params === null) return ''
    return Object.entries(params as Record<string, unknown>)
      .map(([k, v]) => `${k}=${String(v)}`)
      .join(', ')
  }
  if (kind === 'Config') return Object.keys(obj).join(', ')
  return typeof obj.name === 'string' ? obj.name : ''
}

export interface SysctlRow {
  key: string
  value: string
}

// parseSysctlParams reads a SysctlConfig document into editable rows; null
// when the text is not a JSON object, so the screen falls back to the text box.
export function parseSysctlParams(text: string): SysctlRow[] | null {
  const obj = parseObject(text)
  if (!obj) return null
  const params = obj.params
  if (params === undefined) return []
  if (typeof params !== 'object' || params === null || Array.isArray(params)) return null
  return Object.entries(params as Record<string, unknown>).map(([key, value]) => ({ key, value: String(value) }))
}

export function buildSysctlDocument(rows: SysctlRow[]): string {
  const params: Record<string, string> = {}
  for (const r of rows) {
    const key = r.key.trim()
    if (key !== '') params[key] = r.value
  }
  return JSON.stringify({ params })
}

// previewKey identifies what a preview was run for, so the screen can tell
// when the form has changed since (the preview is stale).
export function previewKey(f: {
  cluster: string
  layer: number
  priority: number
  target: string
  documents: { kind: string; document: string }[]
}): string {
  return JSON.stringify([f.cluster, f.layer, f.priority, f.target, f.documents.map((d) => [d.kind, d.document])])
}
