import assert from 'node:assert/strict'
import { test } from 'node:test'
import { caRotationAdvised, detectedCni, isCniUnmanaged } from './cni.ts'

test('only the unmanaged preset is unmanaged', () => {
  assert.equal(isCniUnmanaged('unmanaged'), true)
  assert.equal(isCniUnmanaged('cilium'), false)
  assert.equal(isCniUnmanaged(''), false)
})

test('an undetected CNI reads unknown', () => {
  assert.equal(detectedCni('flannel'), 'flannel')
  assert.equal(detectedCni(''), 'unknown')
})

test('CA rotation advice shows only while the condition is True', () => {
  assert.equal(caRotationAdvised([{ type: 'CARotationAdvised', status: 'True' }]), true)
  assert.equal(caRotationAdvised([{ type: 'CARotationAdvised', status: 'False' }]), false)
  assert.equal(caRotationAdvised([{ type: 'Ready', status: 'True' }]), false)
  assert.equal(caRotationAdvised([]), false)
})
