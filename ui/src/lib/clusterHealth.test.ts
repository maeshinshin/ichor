import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clusterHealth } from './clusterHealth.ts'

test('health follows rollout first, then the Ready condition', () => {
  const ready = [{ type: 'Ready', status: 'True' }]
  assert.equal(clusterHealth({ conditions: ready }), 'ready')
  assert.equal(clusterHealth({ conditions: ready, rollout: {} }), 'upgrading')
  assert.equal(clusterHealth({ conditions: [{ type: 'Ready', status: 'False' }] }), 'degraded')
  assert.equal(clusterHealth({ conditions: [] }), 'degraded')
})
