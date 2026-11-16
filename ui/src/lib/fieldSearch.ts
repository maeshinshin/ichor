// Pure helpers for the config field search. No DOM, no preact and no
// ui/src/gen import, so it runs under node --test. The rows are the generated
// index (ui/src/gen/fieldindex.json, written by `make ui-fieldindex`).

export interface FieldRow {
  // The setting's path in spec.config: [] for an array element, <key> for a map
  // value. The search shows it, and it is the same wherever Talos spells it.
  path: string
  type?: string
  required?: boolean
  format?: string
  values?: string[]
  description?: string
}

const lower = (s: string | undefined) => (s ?? '').toLowerCase()

// searchFields is a substring scan: every word of the query has to occur in the
// row, and a row scores higher where it occurs in the setting's own name than in
// its path or its description.
export function searchFields(rows: FieldRow[], query: string, limit = 50): FieldRow[] {
  const words = lower(query).split(/\s+/).filter(Boolean)
  if (words.length === 0) return []
  const scored: { row: FieldRow; score: number }[] = []
  for (const row of rows) {
    const path = lower(row.path)
    // An entry's key is named by the collection it is in.
    const name = path.split('.').filter((s) => s !== '<key>').pop()!.replace(/\[\]$/, '')
    const description = lower(row.description)
    let score = 0
    for (const w of words) {
      const s = name === w ? 8 : name.includes(w) ? 5 : path.includes(w) ? 3 : description.includes(w) ? 1 : 0
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

export type InputKind = 'text' | 'number' | 'switch' | 'select' | 'json' | 'key'

// An entry row is a map's value as a whole: the one thing it takes is the key it is under.
export const isEntry = (row: FieldRow) => row.path.endsWith('.<key>')

// inputKind is the control a field is edited with: the key for an entry, a Select
// when Talos lists the allowed values, an Input for a string or a number, a Switch
// for a boolean, and the JSON Textarea for an object, an array or a map.
export function inputKind(row: FieldRow): InputKind {
  if (isEntry(row)) return 'key'
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

// The steps from the root of spec.config to a row's setting, a key in each <key>:
// "a.<key>.b[].c" with the key k is a, k, b, 0, c. A row below an array writes its
// first element; more elements are the Textarea's job.
export function steps(path: string, keys: string[]): (string | number)[] {
  let n = 0
  return path.split('.').flatMap((seg) => (seg === '<key>' ? [keys[n++] ?? ''] : seg.endsWith('[]') ? [seg.slice(0, -2), 0] : [seg]))
}

export const keyCount = (row: FieldRow) => row.path.split('.').filter((s) => s === '<key>').length

function parseObject(text: string): Record<string, unknown> | null {
  try {
    const v: unknown = JSON.parse(text)
    return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : null
  } catch {
    return null
  }
}

// getField reads the value at steps from the config's JSON text.
export function getField(text: string, st: (string | number)[]): unknown {
  let cur: unknown = parseObject(text)
  for (const s of st) {
    if (typeof cur !== 'object' || cur === null) return undefined
    cur = (cur as Record<string | number, unknown>)[s]
  }
  return cur
}

// setField writes value at steps and returns the new JSON text; an undefined
// value removes the field and the objects it leaves empty. null when the text is
// not a JSON object, so the caller leaves it alone, and when a value in the way is
// not an object (scalarBlock names it): it is kept, never replaced.
export function setField(text: string, st: (string | number)[], value: unknown): string | null {
  const root = parseObject(text)
  if (!root) return null
  const parents: Json[] = [root]
  let cur: Json = root
  for (let i = 0; i < st.length - 1; i++) {
    const s = st[i]
    let child = (cur as Record<string | number, unknown>)[s]
    if (child === undefined) {
      if (value === undefined) return JSON.stringify(root)
      child = typeof st[i + 1] === 'number' ? [] : {}
      ;(cur as Record<string | number, unknown>)[s] = child
    } else if (typeof child !== 'object' || child === null || (Array.isArray(child) && typeof st[i + 1] === 'string')) {
      return null
    }
    cur = child as Json
    parents.push(cur)
  }
  const last = st[st.length - 1]
  if (value !== undefined) (cur as Record<string | number, unknown>)[last] = value
  else {
    delete (cur as Record<string | number, unknown>)[last]
    // An object left empty goes too, up to the root.
    for (let i = parents.length - 1; i > 0; i--) {
      if (Object.keys(parents[i]).length > 0) break
      delete (parents[i - 1] as Record<string | number, unknown>)[st[i - 1]]
    }
  }
  return JSON.stringify(root)
}

// One setting chosen in the search: the row, and the key each of its <key> is under.
// It is edited with its own control, over the same JSON text as the whole config.
export interface Chosen {
  id: number
  row: FieldRow
  keys: string[]
}

// The part of a row's path up to and including its nth <key>.
const prefixTo = (row: FieldRow, n: number) => {
  const segs = row.path.split('.')
  let seen = 0
  const end = segs.findIndex((s) => s === '<key>' && ++seen === n + 1)
  return segs.slice(0, end + 1).join('.')
}

// pickField is what choosing a search row does: it adds the setting to the chosen
// ones. Each <key> starts as the key of the latest other row chosen under the same
// collection (volumeType after a volume's own row), and as none if there is none.
// An entry's own key is always new: choosing the row again adds another entry, so
// a patch can hold two bonds.
export function pickField(chosen: Chosen[], row: FieldRow, id: number): Chosen[] {
  const keys = Array.from({ length: keyCount(row) }, (_, i) => {
    if (isEntry(row) && i === keyCount(row) - 1) return ''
    const prefix = prefixTo(row, i)
    const sibling = chosen.findLast((c) => c.row.path !== row.path && keyCount(c.row) > i && prefixTo(c.row, i) === prefix)
    return sibling?.keys[i] ?? ''
  })
  return [...chosen, { id, row, keys }]
}

// A setting is written once every one of its keys is named.
export const complete = (c: Chosen) => c.keys.every((k) => k !== '')

// settle makes the config have the entry of each chosen entry row whose key is named,
// since an entry has no value of its own to type: choosing it and naming it is all
// it takes.
export function settle(config: string, chosen: Chosen[]): string {
  let out = config
  for (const c of chosen) {
    const at = steps(c.row.path, c.keys)
    if (isEntry(c.row) && complete(c) && getField(out, at) === undefined) out = setField(out, at, {}) ?? out
  }
  return out
}

// renameKey names key i of a chosen setting anew: the entry under the old key, with
// everything in it, moves to the new one, and so does the key of each chosen
// setting that is in that entry. A key another entry has is refused, since the
// move would overwrite it.
export function renameKey(config: string, chosen: Chosen[], id: number, i: number, key: string): { config: string; chosen: Chosen[] } {
  const target = chosen.find((c) => c.id === id)
  if (!target || key === '' || key === target.keys[i]) return { config, chosen }
  const prefix = prefixTo(target.row, i)
  const was = target.keys.slice(0, i + 1)
  const to = steps(prefix, [...was.slice(0, i), key])
  if (getField(config, to) !== undefined) return { config, chosen }
  const inEntry = (c: Chosen) => keyCount(c.row) > i && prefixTo(c.row, i) === prefix && was.every((k, j) => c.keys[j] === k)
  const next = chosen.map((c) => (inEntry(c) ? { ...c, keys: c.keys.map((k, j) => (j === i ? key : k)) } : c))
  let moved = config
  if (!was.includes('')) {
    const from = steps(prefix, was)
    const value = getField(config, from)
    if (value !== undefined) moved = setField(setField(config, from, undefined) ?? config, to, value) ?? config
  }
  return { config: settle(moved, next), chosen: next }
}

// removeField takes a chosen setting out, and its value from the config.
export function removeField(config: string, chosen: Chosen[], id: number): { config: string; chosen: Chosen[] } {
  const c = chosen.find((x) => x.id === id)
  const out = c && complete(c) ? setField(config, steps(c.row.path, c.keys), undefined) : null
  return { config: out ?? config, chosen: chosen.filter((x) => x.id !== id) }
}

const dotted = (st: (string | number)[]) => st.map(String).join('.')

// scalarBlock is the path of the value in the way of a setting: a parent that exists and is not an object (or is
// an array the setting names a key of). A write through it would replace or drop that value, so setField refuses it.
// null when nothing is in the way.
export function scalarBlock(text: string, st: (string | number)[]): string | null {
  for (let i = 1; i < st.length; i++) {
    const at = st.slice(0, i)
    const v = getField(text, at)
    if (v === undefined) return null
    if (typeof v !== 'object' || v === null || (Array.isArray(v) && typeof st[i] === 'string')) return dotted(at)
  }
  return null
}

// The index as a tree: the names under each path, where a path is a row's path with [] and <key> as the rows
// write them. A group with no row of its own (machine) is a path too. Built once per index.
interface Tree {
  exact: Map<string, FieldRow>
  kids: Map<string, string[]>
}
const trees = new WeakMap<FieldRow[], Tree>()
function treeOf(rows: FieldRow[]): Tree {
  let t = trees.get(rows)
  if (t) return t
  const exact = new Map<string, FieldRow>()
  const kids = new Map<string, Set<string>>()
  for (const r of rows) {
    exact.set(r.path, r)
    const segs = r.path.split('.')
    segs.forEach((seg, i) => {
      const parent = segs.slice(0, i).join('.')
      if (!kids.has(parent)) kids.set(parent, new Set())
      kids.get(parent)!.add(seg)
    })
  }
  t = { exact, kids: new Map([...kids].map(([k, v]) => [k, [...v]])) }
  trees.set(rows, t)
  return t
}

// TreeChild is one name under a path of the index: its segment, its path, and the setting row when the path is one.
export interface TreeChild {
  seg: string
  path: string
  row?: FieldRow
}

// childrenOf lists the names directly under a path ('' for the top level). The browse tree asks for one level at a
// time, so it never holds the whole index as nodes.
export function childrenOf(rows: FieldRow[], under: string): TreeChild[] {
  const { exact, kids } = treeOf(rows)
  return (kids.get(under) ?? []).map((seg) => {
    const path = under ? `${under}.${seg}` : seg
    return { seg, path, row: exact.get(path) }
  })
}

// Missing is a required setting that its present parent does not have. keys are the keys of the entries it is under.
export interface Missing {
  path: string
  row: FieldRow
  keys: string[]
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

// augment is the config as it will be once the chosen settings are written, minus their values: each complete
// chosen setting's parents exist (as empty objects or arrays), so a parent the user is about to write counts as present.
function augment(config: string, chosen: Chosen[]): Record<string, unknown> {
  let text = config
  for (const c of chosen) {
    if (!complete(c)) continue
    const st = steps(c.row.path, c.keys)
    const parents = isEntry(c.row) ? st : st.slice(0, -1)
    for (let i = 1; i <= parents.length; i++) {
      const at = parents.slice(0, i)
      if (getField(text, at) === undefined) text = setField(text, at, typeof st[i] === 'number' ? [] : {}) ?? text
    }
  }
  return parseObject(text) ?? {}
}

// requiredMissing lists the required settings missing under each object the config has, or that a chosen setting
// is about to create.
// shortcut: this covers the CRD's schema-level required lists only, through the index. The rules Talos checks when it
// renders a config (a document-only field with a v1alpha1-only one, the bond spellings) are not checked here, so a
// config that passes this can still be refused at render. Drop it when the server's dry-run with render checks
// answers the same question (the plan's option C).
export function requiredMissing(rows: FieldRow[], config: string, chosen: Chosen[]): Missing[] {
  const { exact, kids } = treeOf(rows)
  const out: Missing[] = []
  const walk = (tmpl: string, value: Record<string, unknown>, shown: string, keys: string[]) => {
    for (const seg of kids.get(tmpl) ?? []) {
      if (seg === '<key>') continue
      const name = seg.replace(/\[\]$/, '')
      const child = tmpl ? `${tmpl}.${seg}` : seg
      const r = exact.get(child)
      const v = value[name]
      if (r?.required && !isEntry(r) && v === undefined) out.push({ path: shown + name, row: r, keys })
      if (v === undefined || v === null) continue
      if (seg.endsWith('[]')) {
        if (Array.isArray(v)) v.forEach((el, i) => isObj(el) && walk(child, el, `${shown}${name}.${i}.`, keys))
      } else if ((kids.get(child) ?? []).includes('<key>')) {
        if (isObj(v)) for (const [k, e] of Object.entries(v)) if (isObj(e)) walk(`${child}.<key>`, e, `${shown}${name}.${k}.`, [...keys, k])
      } else if (isObj(v)) walk(child, v, `${shown}${name}.`, keys)
    }
  }
  walk('', augment(config, chosen), '', [])
  return out
}

// withRequiredSiblings adds, as chosen settings, the required settings that requiredMissing reports and that are not
// chosen yet, so the form shows them to fill in. ids count up from nextId.
export function withRequiredSiblings(rows: FieldRow[], config: string, chosen: Chosen[], nextId: number): Chosen[] {
  let out = chosen
  let id = nextId
  for (;;) {
    const m = requiredMissing(rows, config, out).find((x) => !out.some((c) => c.row.path === x.row.path && c.keys.join('\0') === x.keys.join('\0')))
    if (!m) return out
    out = [...out, { id: id++, row: m.row, keys: m.keys }]
  }
}
