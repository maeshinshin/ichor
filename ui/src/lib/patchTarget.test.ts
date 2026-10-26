import assert from 'node:assert/strict'
import { test } from 'node:test'
import { buildPatchTarget, canSavePatch, parseLabelSelector, stringifyLabelSelector, targetKindOf } from './patchTarget.ts'

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

test('buildPatchTarget throws on a malformed selector, so nothing is sent', () => {
  assert.throws(() => buildPatchTarget(true, 'labelSelector', '', '{oops', ''), /valid JSON/)
})

test('buildPatchTarget builds exactly the one chosen target field', () => {
  assert.deepEqual(buildPatchTarget(true, 'role', 'worker', '{', 'ignored'), { role: 'worker', labelSelector: undefined, machineRef: '' })
  assert.deepEqual(buildPatchTarget(true, 'machineRef', 'worker', '{', 'm1'), { role: '', labelSelector: undefined, machineRef: 'm1' })
  assert.deepEqual(buildPatchTarget(true, 'labelSelector', '', '{"matchLabels":{"a":"b"}}', '')?.labelSelector, {
    matchLabels: { a: 'b' },
    matchExpressions: [],
  })
})

test('buildPatchTarget ignores the target (even an invalid selector) at the cluster layer', () => {
  assert.equal(buildPatchTarget(false, 'labelSelector', '', '{oops', ''), undefined)
  assert.equal(buildPatchTarget(true, 'none', '', '', ''), undefined)
})

test('canSavePatch needs name, cluster, layer, and a target below the cluster layer', () => {
  const ok = { name: 'p', hasCluster: true, layerSet: true, clusterLayer: false, targetKind: 'role' as const }
  assert.equal(canSavePatch(ok), true)
  assert.equal(canSavePatch({ ...ok, name: '' }), false)
  assert.equal(canSavePatch({ ...ok, hasCluster: false }), false)
  assert.equal(canSavePatch({ ...ok, layerSet: false }), false)
  assert.equal(canSavePatch({ ...ok, targetKind: 'none' }), false)
  assert.equal(canSavePatch({ ...ok, clusterLayer: true, targetKind: 'none' }), true)
})
