import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { editablePath, getField, inputKind, pickField, requiredFields, rowDocPath, rowKind, searchFields, setField, type DocItem, type FieldRow } from './fieldSearch.ts'

const read = (name: string) => JSON.parse(readFileSync(new URL(name, import.meta.url), 'utf8'))
const rows: FieldRow[] = read('../gen/fieldindex.json')
const row = (path: string) => {
  const r = rows.find((x) => x.path === path)
  assert.ok(r, `${path} is not in the index`)
  return r
}

test('a setting is one row, by its path in spec.config, whichever documents Talos spells it in', () => {
  const hits = searchFields(rows, 'sysctls')
  assert.equal(hits[0].path, 'machine.sysctls')
  assert.deepEqual(hits[0], row('machine.sysctls'))
  assert.equal(rowKind(hits[0]), 'SysctlConfig')
  // The document's own spelling of the same setting is not a second row.
  assert.deepEqual(searchFields(rows, 'sysctlconfig').map((r) => r.path), ['machine.sysctls'])
  assert.equal(new Set(rows.map((r) => r.path)).size, rows.length)
  assert.equal(new Set(rows.map((r) => r.doc)).size, rows.length)
})

test('a search finds a nested setting by a part of its path, and a document by its kind', () => {
  assert.ok(searchFields(rows, 'provisioning grow').some((r) => r.path === 'machine.volumes.user.<key>.provisioning.grow'))
  assert.ok(searchFields(rows, 'KubeStaticPodConfig').some((r) => r.path === 'machine.staticPods' && !rowDocPath(r)))
  assert.deepEqual(searchFields(rows, '  '), [])
  assert.deepEqual(searchFields(rows, 'no-such-field-anywhere'), [])
})

test('an entry is found by the name of its collection, not by the placeholder of its key', () => {
  assert.equal(searchFields(rows, 'user')[0].path, 'machine.volumes.user')
  assert.ok(searchFields(rows, 'user').some((r) => r.path === 'machine.volumes.user.<key>'))
})

test('a setting with a list of allowed values is edited with a Select, never free text', () => {
  const r = row('machine.volumes.user.<key>.volumeType')
  assert.deepEqual(r.values, ['directory', 'disk', 'partition'])
  assert.equal(inputKind(r), 'select')
  assert.equal(inputKind(row('machine.kernel.modules.<key>')), 'text')
})

test('the control follows the type: number, switch, and JSON for what is not a scalar', () => {
  assert.equal(inputKind(row('machine.volumes.user.<key>.encryption.blockSize')), 'number')
  assert.equal(inputKind(row('machine.volumes.user.<key>.provisioning.grow')), 'switch')
  assert.equal(inputKind(row('machine.volumes.user.<key>.provisioning')), 'json')
  assert.equal(inputKind(row('machine.kernel.modules.<key>.parameters')), 'json')
})

test('a row below a map value is edited as the map', () => {
  const r = rows.find((x) => rowDocPath(x).includes('.<key>'))
  assert.ok(r)
  assert.equal(inputKind(r), 'json')
  assert.ok(!editablePath(r).includes('<key>'))
})

test('setField and getField walk objects and the first element of an array', () => {
  let text: string | null = '{}'
  text = setField(text, 'nameservers[].address', '1.1.1.1')
  assert.equal(text, '{"nameservers":[{"address":"1.1.1.1"}]}')
  assert.equal(getField(text!, 'nameservers[].address'), '1.1.1.1')
  assert.equal(getField(text!, 'nameservers[].protocol'), undefined)
  text = setField(text!, 'nameservers[].address', undefined)
  assert.equal(text, '{"nameservers":[{}]}')
  assert.equal(setField('not json', 'a', 1), null)
  assert.equal(setField('{}', 'a.b', undefined), '{}')
})

test('the settings chosen make the documents the API server is sent, and each lands at its path in spec.config', () => {
  const cases = read('./fieldSearch.fixtures.json') as {
    sets: [string, unknown][]
    documents: { kind: string; document: unknown }[]
    config: unknown
  }[]
  assert.ok(cases.length >= 3)
  for (const c of cases) {
    // What the editor does with a chosen row: pickField, then the row's control writing the document's JSON.
    let items: DocItem[] = []
    let n = 0
    const key = c.sets.find(([p]) => p.endsWith('.<key>'))?.[1] as string | undefined
    for (const [path, value] of c.sets) {
      const r = row(path)
      items = pickField(items, r, requiredFields(rows, rowKind(r)), () => ++n)
      const item = items.find((d) => d.kind === rowKind(r))!
      if (typeof value !== 'object') assert.notEqual(inputKind(r), 'json', path)
      item.document = setField(item.document, editablePath(r), value)!
      // The same value is at the row's path in the spec.config the fixture expects; a key row is the key itself.
      const config = JSON.stringify(c.config)
      const at = path.replace('<key>', key ?? '')
      if (path.endsWith('.<key>')) assert.ok(Object.keys(getField(config, path.slice(0, -'.<key>'.length)) as object).includes(value as string), path)
      else assert.deepEqual(getField(config, at), value, path)
    }
    assert.deepEqual(
      items.map((d) => ({ kind: d.kind, document: JSON.parse(d.document) })),
      c.documents,
    )
  }
})

test('a document kind lists the top-level fields it requires', () => {
  assert.deepEqual(requiredFields(rows, 'KernelModuleConfig').map((r) => r.path), ['machine.kernel.modules.<key>'])
  assert.deepEqual(requiredFields(rows, 'ResolverConfig'), [])
})

test('every row names the Talos document it is written in', () => {
  assert.ok(rows.every((r) => rowKind(r) !== ''))
  assert.ok(new Set(rows.map(rowKind)).size >= 60)
})

test('choosing a row puts its setting in a document of its kind, with the fields the kind requires', () => {
  let n = 0
  const id = () => ++n
  const kind = 'KernelModuleConfig'
  const setting = row('machine.kernel.modules.<key>.parameters')
  const docs = pickField([], setting, requiredFields(rows, kind), id)
  assert.equal(docs.length, 1)
  assert.equal(docs[0].kind, kind)
  assert.deepEqual(docs[0].fields?.map((f) => f.path), ['machine.kernel.modules.<key>', 'machine.kernel.modules.<key>.parameters'])
  assert.equal(docs[0].document, '{}')
  // The same kind again: the setting joins the document the patch has.
  const again = pickField(docs, row('machine.kernel.modules.<key>'), requiredFields(rows, kind), id)
  assert.equal(again.length, 1)
  assert.equal(again[0].fields?.length, 2)
  // A collection's own row adds a document, also when the patch has one of the kind.
  assert.equal(pickField(docs, row('machine.kernel.modules'), requiredFields(rows, kind), id).length, 2)
})
