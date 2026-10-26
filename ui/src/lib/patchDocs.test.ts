import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  buildSysctlDocument,
  defaultDocument,
  DOCUMENT_KINDS,
  parseSysctlParams,
  previewKey,
  summarizeDocument,
} from './patchDocs.ts'
import { en } from './i18n.ts'

test('every document kind has a description and field help that exist in the dictionary', () => {
  assert.equal(DOCUMENT_KINDS.length, 6)
  for (const k of DOCUMENT_KINDS) {
    assert.ok(k.descKey in en, k.descKey)
    for (const f of k.fields) {
      assert.ok(f.helpKey in en, f.helpKey)
    }
  }
})

test('summarizeDocument shows the key fields of each kind', () => {
  assert.equal(
    summarizeDocument('SysctlConfig', '{"params":{"net.core.somaxconn":"1024","vm.swappiness":"10"}}'),
    'net.core.somaxconn=1024, vm.swappiness=10',
  )
  assert.equal(summarizeDocument('KernelModuleConfig', '{"name":"br_netfilter","parameters":["a=1"]}'), 'br_netfilter')
  assert.equal(summarizeDocument('UserVolumeConfig', '{"name":"data"}'), 'data')
  assert.equal(summarizeDocument('Config', '{"machine":{},"cluster":{}}'), 'machine, cluster')
})

test('summarizeDocument returns an empty string for text that is not a JSON object', () => {
  assert.equal(summarizeDocument('SysctlConfig', '{oops'), '')
  assert.equal(summarizeDocument('SysctlConfig', '[]'), '')
  assert.equal(summarizeDocument('SysctlConfig', '{}'), '')
})

test('sysctl rows round-trip through the document JSON', () => {
  const rows = parseSysctlParams('{"params":{"a.b":"1"}}')
  assert.deepEqual(rows, [{ key: 'a.b', value: '1' }])
  assert.equal(buildSysctlDocument([{ key: 'a.b', value: '1' }, { key: ' ', value: 'x' }]), '{"params":{"a.b":"1"}}')
  assert.equal(parseSysctlParams('{oops'), null)
})

test('previewKey changes with anything the preview depends on, and with nothing else', () => {
  const base = { cluster: 'ns/c', layer: 2, priority: 0, target: 'role:worker', documents: [{ kind: 'SysctlConfig', document: '{}' }] }
  assert.equal(previewKey(base), previewKey({ ...base }))
  assert.notEqual(previewKey(base), previewKey({ ...base, priority: 1 }))
  assert.notEqual(previewKey(base), previewKey({ ...base, documents: [{ kind: 'SysctlConfig', document: '{"params":{}}' }] }))
})

test('defaultDocument starts each kind on the right skeleton', () => {
  assert.equal(defaultDocument('SysctlConfig'), '{"params":{}}')
  assert.equal(defaultDocument('UserVolumeConfig'), '{"name":""}')
  assert.equal(defaultDocument('Config'), '{}')
})
