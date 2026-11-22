import assert from 'node:assert/strict'
import { test } from 'node:test'
import { contractAtLeast, deviceOwnershipCanTurnOn } from './containerd.ts'

test('contractAtLeast compares major and minor numerically', () => {
  assert.equal(contractAtLeast('v1.14', 'v1.14'), true)
  assert.equal(contractAtLeast('v1.15', 'v1.14'), true)
  assert.equal(contractAtLeast('v1.9', 'v1.14'), false)
  assert.equal(contractAtLeast('v2.0', 'v1.14'), true)
  assert.equal(contractAtLeast('', 'v1.14'), false)
  assert.equal(contractAtLeast('1.14', 'v1.14'), false)
})

test('deviceOwnershipCanTurnOn refuses an old contract but never blocks turning it off', () => {
  assert.equal(deviceOwnershipCanTurnOn('v1.13', false), false)
  assert.equal(deviceOwnershipCanTurnOn('v1.13', true), true)
  assert.equal(deviceOwnershipCanTurnOn('v1.14', false), true)
})
