import assert from 'node:assert/strict'
import { test } from 'node:test'
import { conditionStatusTone, opStateTone } from './status.ts'

test('a health condition is good when True and bad when False', () => {
  assert.equal(conditionStatusTone('Ready', 'True'), 'good')
  assert.equal(conditionStatusTone('Ready', 'False'), 'bad')
})

test('a non-health condition or an Unknown status is neutral', () => {
  assert.equal(conditionStatusTone('Transitioning', 'False'), 'neutral')
  assert.equal(conditionStatusTone('Ready', 'Unknown'), 'neutral')
})

test('operation states map to tones', () => {
  assert.equal(opStateTone('done'), 'good')
  assert.equal(opStateTone('failed'), 'bad')
  assert.equal(opStateTone('running'), 'progress')
  assert.equal(opStateTone('paused'), 'warn')
  assert.equal(opStateTone('pending'), 'neutral')
  assert.equal(opStateTone('shutdown'), 'neutral')
})
