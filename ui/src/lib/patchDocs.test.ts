import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  buildSysctlDocument,
  DOCUMENT_KINDS,
  parseSysctlParams,
  previewKey,
  summarizeDocument,
  targetedMachines,
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

test('targetedMachines picks the machines a patch applies to, or null when it cannot tell', () => {
  const ms = [
    { name: 'cp-1', cluster: 'ns/c', role: 'controlplane' },
    { name: 'w-1', cluster: 'ns/c', role: 'worker' },
    { name: 'w-2', cluster: 'ns/other', role: 'worker' },
  ]
  const base = { cluster: 'ns/c', clusterLayer: false, targetKind: 'role' as const, role: 'worker', machineRef: '' }
  assert.deepEqual(targetedMachines({ ...base, clusterLayer: true, targetKind: 'none' }, ms), ['cp-1', 'w-1'])
  assert.deepEqual(targetedMachines(base, ms), ['w-1'])
  assert.deepEqual(targetedMachines({ ...base, targetKind: 'machineRef', machineRef: 'cp-1' }, ms), ['cp-1'])
  assert.equal(targetedMachines({ ...base, targetKind: 'labelSelector' }, ms), null)
})
