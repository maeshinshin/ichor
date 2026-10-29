import assert from 'node:assert/strict'
import { test } from 'node:test'
import { opStateTone } from './status.ts'

test('operation states map to tones', () => {
  assert.equal(opStateTone('done'), 'good')
  assert.equal(opStateTone('failed'), 'bad')
  assert.equal(opStateTone('running'), 'progress')
  assert.equal(opStateTone('paused'), 'warn')
  assert.equal(opStateTone('pending'), 'neutral')
  assert.equal(opStateTone('shutdown'), 'neutral')
})
