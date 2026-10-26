import { test } from 'node:test'
import assert from 'node:assert/strict'
import { conditionTone } from './conditions.ts'

test('health-like conditions are good when True and bad when False', () => {
  assert.equal(conditionTone('Ready', 'True'), 'good')
  assert.equal(conditionTone('Ready', 'False'), 'bad')
  assert.equal(conditionTone('Ready', 'Unknown'), 'neutral')
})

test('other conditions are neutral whatever their status', () => {
  assert.equal(conditionTone('Transitioning', 'True'), 'neutral')
  assert.equal(conditionTone('Transitioning', 'False'), 'neutral')
})
