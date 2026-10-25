import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseLabelSelector, stringifyLabelSelector, targetKindOf } from './patchTarget.ts'

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
