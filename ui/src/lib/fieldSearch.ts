// Pure helpers for the config field search. No DOM, no preact and no
// ui/src/gen import, so it runs under node --test. The rows are the generated
// index (ui/src/gen/fieldindex.json, written by `make docs-reference`).

export interface FieldRow {
  kind: string
  // Relative to the document, as in the CRD reference: [] for an array
  // element, <key> for a map value. Empty on a kind's own row.
  path?: string
  type?: string
  required?: boolean
  format?: string
  values?: string[]
  description?: string
}

const lower = (s: string | undefined) => (s ?? '').toLowerCase()

// searchFields is a substring scan: every word of the query has to occur in the
// row, and a row scores higher where it occurs in the field's own name than in
// its path, its kind or its description.
export function searchFields(rows: FieldRow[], query: string, limit = 50): FieldRow[] {
  const words = lower(query).split(/\s+/).filter(Boolean)
  if (words.length === 0) return []
  const scored: { row: FieldRow; score: number }[] = []
  for (const row of rows) {
    const path = lower(row.path)
    const name = path.split('.').pop()!.replace(/\[\]$/, '')
    const kind = lower(row.kind)
    const description = lower(row.description)
    let score = 0
    for (const w of words) {
      const s = name === w ? 8 : name.includes(w) ? 5 : path.includes(w) ? 3 : kind.includes(w) ? 2 : description.includes(w) ? 1 : 0
      if (s === 0) {
        score = 0
        break
      }
      score += s
    }
    if (score > 0) scored.push({ row, score })
  }
  // Array.prototype.sort is stable: equal scores keep the index order.
  return scored.sort((a, b) => b.score - a.score).slice(0, limit).map((s) => s.row)
}

export type InputKind = 'text' | 'number' | 'switch' | 'select' | 'json'

// The part of a row's path an input can edit. A map value has no fixed key,
// so the map itself is what is edited, as JSON.
export function editablePath(row: FieldRow): string {
  const path = row.path ?? ''
  const i = path.indexOf('.<key>')
  return i < 0 ? path : path.slice(0, i)
}

// inputKind is the control a field is edited with: a Select when Talos lists
// the allowed values, an Input for a string or a number, a Switch for a
// boolean, and the JSON Textarea for an object, an array or a map.
export function inputKind(row: FieldRow): InputKind {
  if (editablePath(row) !== (row.path ?? '')) return 'json'
  if (row.values && row.values.length > 0 && row.type === 'string') return 'select'
  switch (row.type) {
    case 'string':
    case 'integer or string':
      return 'text'
    case 'integer':
    case 'number':
      return 'number'
    case 'boolean':
      return 'switch'
    default:
      return 'json'
  }
}

type Json = Record<string, unknown> | unknown[]

// "a.b[].c" is a, b, 0, c: the first element of an array. A row below an array
// writes the first element; more elements are the Textarea's job.
function steps(path: string): (string | number)[] {
  return path.split('.').flatMap((seg) => (seg.endsWith('[]') ? [seg.slice(0, -2), 0] : [seg]))
}

function parseObject(text: string): Record<string, unknown> | null {
  try {
    const v: unknown = JSON.parse(text)
    return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null
  } catch {
    return null
  }
}

// getField reads the value at a row's path from a document's JSON text.
export function getField(text: string, path: string): unknown {
  let cur: unknown = parseObject(text)
  for (const s of steps(path)) {
    if (typeof cur !== 'object' || cur === null) return undefined
    cur = (cur as Record<string | number, unknown>)[s]
  }
  return cur
}

// setField writes value at path and returns the new JSON text; an undefined
// value removes the field. null when the text is not a JSON object, so the
// caller leaves it alone.
export function setField(text: string, path: string, value: unknown): string | null {
  const root = parseObject(text)
  if (!root) return null
  const st = steps(path)
  let cur: Json = root
  for (let i = 0; i < st.length - 1; i++) {
    const s = st[i]
    const next = st[i + 1]
    let child = (cur as Record<string | number, unknown>)[s]
    if (typeof child !== 'object' || child === null) {
      if (value === undefined) return JSON.stringify(root)
      child = typeof next === 'number' ? [] : {}
      ;(cur as Record<string | number, unknown>)[s] = child
    }
    cur = child as Json
  }
  const last = st[st.length - 1]
  if (value === undefined) delete (cur as Record<string | number, unknown>)[last]
  else (cur as Record<string | number, unknown>)[last] = value
  return JSON.stringify(root)
}

// requiredFields are the top-level fields a kind's document must have; a
// document added from the search shows an input for each.
export function requiredFields(rows: FieldRow[], kind: string): FieldRow[] {
  return rows.filter((r) => r.kind === kind && r.required && r.path && !r.path.includes('.'))
}

// One document of a patch under edit. fields are the rows chosen in the search:
// each is edited with its own control, over the same JSON text as the document.
export interface DocItem {
  id: number
  kind: string
  document: string
  // fresh marks a document added in this session: its editor opens at once.
  fresh?: boolean
  fields?: FieldRow[]
}

// pickField is what choosing a search row does to a patch's documents. A field
// goes into the patch's document of its kind, or into a new one if the patch
// has none; a kind's own row always adds a new document, since a patch may
// hold several of one kind (two bonds). A new document starts with an input
// for each field its kind requires.
export function pickField(docs: DocItem[], row: FieldRow, required: FieldRow[], newId: () => number): DocItem[] {
  const existing = row.path ? docs.find((d) => d.kind === row.kind) : undefined
  const have = existing?.fields ?? []
  const wanted = [...(existing ? [] : required), ...(row.path ? [row] : [])].filter((r) => !have.some((h) => h.path === r.path))
  if (existing) return docs.map((d) => (d === existing ? { ...d, fields: [...have, ...wanted] } : d))
  return [...docs, { id: newId(), kind: row.kind, document: '{}', fresh: true, fields: wanted }]
}
