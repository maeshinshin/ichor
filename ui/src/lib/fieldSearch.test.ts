import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { test as nodeTest, type TestContext } from 'node:test'
import { complete, getField, inputKind, keyCount, pickField, removeField, renameKey, searchFields, setField, settle, steps, type Chosen, type FieldRow } from './fieldSearch.ts'

const read = (name: string) => JSON.parse(readFileSync(new URL(name, import.meta.url), 'utf8'))
// The index is written, untracked, by make ui-fieldindex. Without it this module throws at load, so a bare npm test
// fails naming the command; skipping every test would report green.
if (!existsSync(new URL('../gen/fieldindex.json', import.meta.url))) throw new Error('ui/src/gen/fieldindex.json is missing: run make ui-fieldindex')
const test = (name: string, fn: (t: TestContext) => void | Promise<void>) => nodeTest(name, fn)
const rows: FieldRow[] = read('../gen/fieldindex.json')
const row = (path: string) => {
  const r = rows.find((x) => x.path === path)
  assert.ok(r, `${path} is not in the index`)
  return r
}

// What the editor does with a setting chosen in the search: pickField, the keys named, and the row's own control
// writing its value into the config's JSON text.
function choose(sets: { path: string; keys: string[]; value?: unknown }[]) {
  let chosen: Chosen[] = []
  let config = '{}'
  let n = 0
  for (const s of sets) {
    const r = row(s.path)
    const id = ++n
    chosen = pickField(chosen, r, id)
    config = settle(config, chosen)
    s.keys.forEach((k, i) => ({ config, chosen } = renameKey(config, chosen, id, i, k)))
    const at = chosen.find((c) => c.id === id)!
    assert.ok(complete(at), s.path)
    if (s.value !== undefined) {
      if (typeof s.value !== 'object') assert.notEqual(inputKind(r), 'json', s.path)
      config = setField(config, steps(r.path, at.keys), s.value)!
    }
  }
  return { chosen, config }
}

test('a setting is one row, by its path in spec.config, and a row carries no Talos document', () => {
  const hits = searchFields(rows, 'sysctls')
  assert.equal(hits[0].path, 'machine.sysctls')
  assert.deepEqual(hits[0], row('machine.sysctls'))
  assert.equal(new Set(rows.map((r) => r.path)).size, rows.length)
  assert.ok(rows.every((r) => !('doc' in r)))
})

test('a search finds a nested setting by a part of its path', () => {
  assert.ok(searchFields(rows, 'provisioning grow').some((r) => r.path === 'machine.volumes.user.<key>.provisioning.grow'))
  assert.ok(searchFields(rows, 'staticPods').some((r) => r.path === 'machine.staticPods'))
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
})

test('the control follows the type: key, number, switch, and JSON for what is not a scalar', () => {
  assert.equal(inputKind(row('machine.kernel.modules.<key>')), 'key')
  assert.equal(inputKind(row('machine.volumes.user.<key>.encryption.blockSize')), 'number')
  assert.equal(inputKind(row('machine.volumes.user.<key>.provisioning.grow')), 'switch')
  assert.equal(inputKind(row('machine.volumes.user.<key>.provisioning')), 'json')
  assert.equal(inputKind(row('machine.kernel.modules.<key>.parameters')), 'json')
  assert.equal(inputKind(row('machine.install.disk')), 'text')
})

test('steps puts a key at each <key> and the first element at each []', () => {
  assert.deepEqual(steps('machine.volumes.user.<key>.provisioning.grow', ['data']), ['machine', 'volumes', 'user', 'data', 'provisioning', 'grow'])
  assert.deepEqual(steps('machine.network.nameservers[].address', []), ['machine', 'network', 'nameservers', 0, 'address'])
  assert.deepEqual(steps('a.<key>.b.<key>', ['x.y', 'z']), ['a', 'x.y', 'b', 'z'])
  assert.equal(keyCount(row('machine.volumes.user.<key>.provisioning.grow')), 1)
})

test('setField and getField walk objects and the first element of an array, and a removal takes the empty objects with it', () => {
  let text: string | null = '{}'
  text = setField(text, steps('machine.network.nameservers[].address', []), '1.1.1.1')
  assert.equal(text, '{"machine":{"network":{"nameservers":[{"address":"1.1.1.1"}]}}}')
  assert.equal(getField(text!, steps('machine.network.nameservers[].address', [])), '1.1.1.1')
  assert.equal(getField(text!, steps('machine.network.nameservers[].protocol', [])), undefined)
  assert.equal(setField('{"a":{"b":{"c":1}},"d":2}', ['a', 'b', 'c'], undefined), '{"d":2}')
  assert.equal(setField('not json', ['a'], 1), null)
  assert.equal(setField('{}', ['a', 'b'], undefined), '{}')
})

test('the settings chosen make the spec.config the API server is sent, each at its path, whatever keys they are under', () => {
  const cases = read('./fieldSearch.fixtures.json') as { sets: { path: string; keys: string[]; value?: unknown }[]; config: unknown }[]
  assert.ok(cases.length >= 5)
  for (const c of cases) assert.deepEqual(JSON.parse(choose(c.sets).config), c.config)
})

test('the same row chosen again is another entry, so a patch holds two of a kind', () => {
  const { chosen, config } = choose([
    { path: 'machine.kernel.modules.<key>', keys: ['br_netfilter'] },
    { path: 'machine.kernel.modules.<key>', keys: ['nbd'] },
  ])
  assert.equal(chosen.length, 2)
  assert.deepEqual(chosen.map((c) => c.keys), [['br_netfilter'], ['nbd']])
  assert.deepEqual(JSON.parse(config), { machine: { kernel: { modules: { br_netfilter: {}, nbd: {} } } } })
})

test('another row of a collection starts under the key of the latest entry chosen in it', () => {
  const entry = row('machine.volumes.user.<key>')
  const chosen = pickField(pickField([], entry, 1), entry, 2)
  assert.deepEqual(chosen.map((c) => c.keys), [[''], ['']])
  const named = renameKey('{}', chosen, 2, 0, 'logs')
  const next = pickField(named.chosen, row('machine.volumes.user.<key>.volumeType'), 3)
  assert.deepEqual(next[2].keys, ['logs'])
})

test('naming an entry anew moves it, with what is in it and the keys of the settings chosen in it, and never over another', () => {
  const { chosen, config } = choose([
    { path: 'machine.volumes.user.<key>', keys: ['data'] },
    { path: 'machine.volumes.user.<key>.volumeType', keys: ['data'], value: 'partition' },
    { path: 'machine.volumes.user.<key>', keys: ['logs'] },
  ])
  const moved = renameKey(config, chosen, 1, 0, 'archive')
  assert.deepEqual(JSON.parse(moved.config).machine.volumes.user, { archive: { volumeType: 'partition' }, logs: {} })
  assert.deepEqual(moved.chosen.map((c) => c.keys[0]), ['archive', 'archive', 'logs'])
  assert.deepEqual(renameKey(config, chosen, 1, 0, 'logs'), { config, chosen })
})

test('removing a setting takes its value out of the config', () => {
  const { chosen, config } = choose([
    { path: 'machine.install.disk', keys: [], value: '/dev/sda' },
    { path: 'machine.sysctls', keys: [], value: { a: '1' } },
  ])
  const out = removeField(config, chosen, 1)
  assert.deepEqual(JSON.parse(out.config), { machine: { sysctls: { a: '1' } } })
  assert.equal(out.chosen.length, 1)
})

test('the JSON text and the typed fields are one object: an edit of either keeps what the other wrote', () => {
  const { chosen, config } = choose([{ path: 'machine.install.disk', keys: [], value: '/dev/sda' }])
  // The text box adds a setting the typed field knows nothing of.
  const edited = JSON.stringify({ ...JSON.parse(config), cluster: { clusterName: 'demo' } })
  const sda = chosen[0]
  assert.equal(getField(edited, steps(sda.row.path, sda.keys)), '/dev/sda')
  // The typed field writes again and the text box's setting is still there.
  const typed = setField(edited, steps(sda.row.path, sda.keys), '/dev/sdb')!
  assert.deepEqual(JSON.parse(typed), { machine: { install: { disk: '/dev/sdb' } }, cluster: { clusterName: 'demo' } })
  // Removing the typed field leaves the other one.
  assert.deepEqual(JSON.parse(removeField(typed, chosen, sda.id).config), { cluster: { clusterName: 'demo' } })
})

// The required-field gate (A). Imported as a namespace so a function not written yet fails its own test, by name.
import * as A from './fieldSearch.ts'

test('choosing machine.install.disk alone names machine.install.wipe as missing before save', () => {
  const chosen = A.pickField([], row('machine.install.disk'), 1)
  assert.deepEqual(
    A.requiredMissing(rows, '{}', chosen).map((m) => m.path),
    ['machine.install.wipe'],
  )
})

test('a required sibling that is already set is not reported missing', () => {
  const chosen = A.pickField([], row('machine.install.disk'), 1)
  assert.deepEqual(A.requiredMissing(rows, '{"machine":{"install":{"wipe":false}}}', chosen), [])
})

test('choosing a setting under a present parent adds its missing required sibling as a chosen row', () => {
  const chosen = A.pickField([], row('machine.install.disk'), 1)
  const next = A.withRequiredSiblings(rows, '{}', chosen, 2)
  assert.deepEqual(
    next.map((c) => c.row.path),
    ['machine.install.disk', 'machine.install.wipe'],
  )
})

test('a setting under a scalar parent is refused, and the scalar is kept, not replaced', () => {
  assert.equal(A.scalarBlock('{"machine":{"install":"x"}}', steps('machine.install.disk', [])), 'machine.install')
  assert.equal(setField('{"machine":{"install":"x"}}', steps('machine.install.disk', []), '/dev/sda'), null)
  assert.equal(A.scalarBlock('{"machine":{"install":{"wipe":true}}}', steps('machine.install.disk', [])), null)
})

test('a required setting under a map entry is named with the entry key it is missing under', () => {
  const chosen = A.pickField([], row('machine.volumes.user.<key>.encryption.provider'), 1)
  const named = [{ ...chosen[0], keys: ['data'] }]
  assert.deepEqual(
    A.requiredMissing(rows, '{}', named).map((m) => m.path),
    // The chosen provider is required too and has no value yet, so it is missing as well.
    ['machine.volumes.user.data.encryption.keys', 'machine.volumes.user.data.encryption.provider'],
  )
})

// The browse tree (B) reads the index one level at a time: the names under a path, each with its own setting row when it has one.
test('the tree lists the top level, then one level under a path, with a setting row where the path has one', () => {
  assert.deepEqual(
    A.childrenOf(rows, '').map((c) => c.seg),
    ['cluster', 'debug', 'machine', 'persist'],
  )
  const install = A.childrenOf(rows, 'machine.install')
  assert.ok(install.some((c) => c.seg === 'wipe' && c.row?.path === 'machine.install.wipe'))
  assert.ok(install.some((c) => c.seg === 'disk' && c.row?.path === 'machine.install.disk'))
  // A map's entries are one child, named by the key placeholder, and the entry is a row of its own.
  assert.deepEqual(
    A.childrenOf(rows, 'machine.containers').map((c) => [c.seg, c.path, c.row?.path]),
    [['<key>', 'machine.containers.<key>', 'machine.containers.<key>']],
  )
  // An array's item fields are under the array's own segment.
  assert.ok(A.childrenOf(rows, 'machine.network.interfaces[]').some((c) => c.seg === 'bond'))
})
