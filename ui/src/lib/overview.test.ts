import assert from 'node:assert/strict'
import { test } from 'node:test'
import { machineCounts, operationCounts } from './overview.ts'

test('machineCounts splits by approval and readiness', () => {
  const c = machineCounts([
    { approval: 'approved', ready: 'True' },
    { approval: 'approved', ready: 'False' },
    { approval: 'approved', ready: '' },
    { approval: 'pending', ready: '' },
    { approval: 'rejected', ready: '' },
  ])
  assert.deepEqual(c, { total: 5, ready: 1, notReady: 1, unknown: 1, pending: 1, rejected: 1 })
})

test('operationCounts counts running, paused, failed and done', () => {
  const c = operationCounts(['running', 'running', 'failed', 'done', 'paused', 'pending'])
  assert.deepEqual(c, { total: 6, running: 2, failed: 1, paused: 1, done: 1 })
})
