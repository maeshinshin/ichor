import assert from 'node:assert/strict'
import { test } from 'node:test'
import { detectedCni, isCniUnmanaged } from './cni.ts'

test('only the unmanaged preset is unmanaged', () => {
  assert.equal(isCniUnmanaged('unmanaged'), true)
  assert.equal(isCniUnmanaged('cilium'), false)
  assert.equal(isCniUnmanaged(''), false)
})

test('an undetected CNI reads unknown', () => {
  assert.equal(detectedCni('flannel'), 'flannel')
  assert.equal(detectedCni(''), 'unknown')
})
