import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  buildPatchTarget,
  canSavePatch,
  parseLabelSelector,
  stringifyLabelSelector,
  targetFieldsFor,
  targetKindForLayer,
  targetKindOf,
} from './patchTarget.ts'

test('targetKindOf reads back the one populated field', () => {
  assert.equal(targetKindOf(undefined), 'none')
  assert.equal(targetKindOf({ role: 'worker', labelSelector: undefined, machineRef: '' }), 'role')
  assert.equal(
    targetKindOf({ role: '', labelSelector: { matchLabels: {}, matchExpressions: [] }, machineRef: '' }),
    'labelSelector',
  )
  assert.equal(targetKindOf({ role: '', labelSelector: undefined, machineRef: 'node-1' }), 'machineRef')
  assert.equal(targetKindOf({ role: '', labelSelector: undefined, machineRef: '' }), 'none')
})

test('stringifyLabelSelector/parseLabelSelector round-trip', () => {
  const ls = {
    matchLabels: { tier: 'storage' },
    matchExpressions: [{ key: 'env', operator: 'In', values: ['prod'] }],
  }
  const text = stringifyLabelSelector(ls)
  assert.deepEqual(parseLabelSelector(text), ls)
})

test('stringifyLabelSelector defaults an unset selector to empty, valid JSON', () => {
  assert.deepEqual(parseLabelSelector(stringifyLabelSelector(undefined)), { matchLabels: {}, matchExpressions: [] })
})

test('parseLabelSelector rejects invalid JSON rather than guessing', () => {
  assert.throws(() => parseLabelSelector('{not json'), /valid JSON/)
})

test('parseLabelSelector rejects a JSON value that is not an object', () => {
  assert.throws(() => parseLabelSelector('[1,2,3]'), /JSON object/)
  assert.throws(() => parseLabelSelector('"just a string"'), /JSON object/)
})

test('targetKindForLayer maps each layer to exactly one target kind', () => {
  assert.equal(targetKindForLayer('unset'), 'none')
  assert.equal(targetKindForLayer('cluster'), 'none')
  assert.equal(targetKindForLayer('role'), 'role')
  assert.equal(targetKindForLayer('selector'), 'labelSelector')
  assert.equal(targetKindForLayer('machine'), 'machineRef')
})

test('buildPatchTarget throws on a malformed selector layer, so nothing is sent', () => {
  assert.throws(() => buildPatchTarget('selector', '', '{oops', ''), /valid JSON/)
})

test('buildPatchTarget builds exactly the target the layer implies', () => {
  assert.deepEqual(buildPatchTarget('role', 'worker', '{', 'ignored'), { role: 'worker', labelSelector: undefined, machineRef: '' })
  assert.deepEqual(buildPatchTarget('machine', 'worker', '{', 'm1'), { role: '', labelSelector: undefined, machineRef: 'm1' })
  assert.deepEqual(buildPatchTarget('selector', 'worker', '{"matchLabels":{"a":"b"}}', 'm1')?.labelSelector, {
    matchLabels: { a: 'b' },
    matchExpressions: [],
  })
})

test('buildPatchTarget sends no target at the cluster layer, even with a stale invalid selector', () => {
  assert.equal(buildPatchTarget('cluster', 'worker', '{oops', 'm1'), undefined)
  assert.equal(buildPatchTarget('unset', '', '', ''), undefined)
})

test('targetFieldsFor keeps only the field the layer uses, clearing the rest', () => {
  const empty = stringifyLabelSelector(undefined)
  const target = { role: 'worker', labelSelector: undefined, machineRef: '' }
  assert.deepEqual(targetFieldsFor('role', target), { role: 'worker', labelSelectorText: empty, machineRef: '' })
  assert.deepEqual(targetFieldsFor('machine', target), { role: '', labelSelectorText: empty, machineRef: '' })
  assert.deepEqual(targetFieldsFor('cluster', target), { role: '', labelSelectorText: empty, machineRef: '' })
  const ls = { matchLabels: { a: 'b' }, matchExpressions: [] }
  assert.deepEqual(targetFieldsFor('selector', { role: '', labelSelector: ls, machineRef: '' }), {
    role: '',
    labelSelectorText: stringifyLabelSelector(ls),
    machineRef: '',
  })
  assert.deepEqual(targetFieldsFor('machine', { role: '', labelSelector: undefined, machineRef: 'm1' }), {
    role: '',
    labelSelectorText: empty,
    machineRef: 'm1',
  })
  assert.deepEqual(targetFieldsFor('role', undefined), { role: '', labelSelectorText: empty, machineRef: '' })
})

test('canSavePatch needs name, cluster, layer, and the target value its layer asks for', () => {
  const ok = { name: 'p', hasCluster: true, layer: 'role' as const, role: 'worker', machineRef: '' }
  assert.equal(canSavePatch(ok), true)
  assert.equal(canSavePatch({ ...ok, name: '' }), false)
  assert.equal(canSavePatch({ ...ok, hasCluster: false }), false)
  assert.equal(canSavePatch({ ...ok, layer: 'unset' }), false)
  assert.equal(canSavePatch({ ...ok, role: '' }), false)
  assert.equal(canSavePatch({ ...ok, layer: 'machine' }), false)
  assert.equal(canSavePatch({ ...ok, layer: 'machine', machineRef: 'm1' }), true)
  assert.equal(canSavePatch({ ...ok, layer: 'selector' }), true)
  assert.equal(canSavePatch({ ...ok, layer: 'cluster', role: '' }), true)
})
