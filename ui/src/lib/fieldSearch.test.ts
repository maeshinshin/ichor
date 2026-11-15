import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { editablePath, getField, inputKind, pickField, requiredFields, searchFields, setField, type FieldRow } from './fieldSearch.ts'

const read = (name: string) => JSON.parse(readFileSync(new URL(name, import.meta.url), 'utf8'))
const rows: FieldRow[] = read('../gen/fieldindex.json')
const row = (kind: string, path: string) => {
  const r = rows.find((x) => x.kind === kind && x.path === path)
  assert.ok(r, `${kind} ${path} is not in the index`)
  return r
}

test('searching a field name returns the row with its kind and path', () => {
  const hits = searchFields(rows, 'volumeType')
  assert.deepEqual(hits[0], row('UserVolumeConfig', 'volumeType'))
  assert.equal(hits[0].kind, 'UserVolumeConfig')
  assert.equal(hits[0].path, 'volumeType')
})

test('a search finds a nested field by a part of its path, and a kind by its name', () => {
  assert.ok(searchFields(rows, 'provisioning grow').some((r) => r.kind === 'UserVolumeConfig' && r.path === 'provisioning.grow'))
  assert.ok(searchFields(rows, 'KubeStaticPodConfig').some((r) => r.kind === 'KubeStaticPodConfig' && !r.path))
  assert.deepEqual(searchFields(rows, '  '), [])
  assert.deepEqual(searchFields(rows, 'no-such-field-anywhere'), [])
})

test('the description is searched too, after the names', () => {
  const hits = searchFields(rows, 'originated')
  assert.deepEqual(hits[0], row('BGPInstanceConfig', 'advertise'))
})

test('a field with a list of allowed values is edited with a Select, never free text', () => {
  const r = row('UserVolumeConfig', 'volumeType')
  assert.deepEqual(r.values, ['directory', 'disk', 'partition'])
  assert.equal(inputKind(r), 'select')
  assert.equal(inputKind(row('KernelModuleConfig', 'name')), 'text')
})

test('the control follows the type: number, switch, and JSON for what is not a scalar', () => {
  assert.equal(inputKind(row('UserVolumeConfig', 'encryption.blockSize')), 'number')
  assert.equal(inputKind(row('UserVolumeConfig', 'provisioning.grow')), 'switch')
  assert.equal(inputKind(row('UserVolumeConfig', 'provisioning')), 'json')
  assert.equal(inputKind(row('KernelModuleConfig', 'parameters')), 'json')
})

test('a row below a map value is edited as the map', () => {
  const r = rows.find((x) => x.path?.includes('.<key>'))
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

test('the fields chosen for a kind make the document the API server is sent', () => {
  const cases = read('./fieldSearch.fixtures.json') as { kind: string; sets: [string, unknown][]; document: unknown }[]
  assert.ok(cases.length >= 3)
  for (const c of cases) {
    let text = '{}'
    for (const [path, value] of c.sets) {
      const r = row(c.kind, path)
      assert.notEqual(inputKind(r), 'json', `${c.kind} ${path}`)
      text = setField(text, path, value)!
    }
    assert.deepEqual(JSON.parse(text), c.document, c.kind)
  }
})

test('a kind lists the top-level fields it requires', () => {
  assert.deepEqual(requiredFields(rows, 'KernelModuleConfig').map((r) => r.path), ['name'])
  assert.deepEqual(requiredFields(rows, 'ResolverConfig'), [])
})

test('every row has a kind that is a document kind, and every kind has a row of its own', () => {
  const kinds = new Set(rows.map((r) => r.kind))
  for (const k of kinds) assert.ok(rows.some((r) => r.kind === k && !r.path), `${k} has no row of its own`)
  assert.ok(kinds.size >= 60)
})

test('choosing a row puts its field in a document of its kind, with the fields the kind requires', () => {
  let n = 0
  const id = () => ++n
  const field = row('KernelModuleConfig', 'parameters')
  const docs = pickField([], field, requiredFields(rows, 'KernelModuleConfig'), id)
  assert.equal(docs.length, 1)
  assert.equal(docs[0].kind, 'KernelModuleConfig')
  assert.deepEqual(docs[0].fields?.map((f) => f.path), ['name', 'parameters'])
  assert.equal(docs[0].document, '{}')
  // The same kind again: the field joins the document the patch has.
  const again = pickField(docs, row('KernelModuleConfig', 'name'), requiredFields(rows, 'KernelModuleConfig'), id)
  assert.equal(again.length, 1)
  assert.equal(again[0].fields?.length, 2)
  // A kind's own row adds a document, also when the patch has one of the kind.
  const kindRow = rows.find((r) => r.kind === 'KernelModuleConfig' && !r.path)!
  assert.equal(pickField(docs, kindRow, requiredFields(rows, 'KernelModuleConfig'), id).length, 2)
})
