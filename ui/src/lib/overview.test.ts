import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clusterReadiness, machineCounts, operationCounts, operationsLine } from './overview.ts'

test('clusterReadiness counts the clusters whose Ready condition is True', () => {
  assert.deepEqual(clusterReadiness(['True', 'True']), { total: 2, ready: 2 })
  assert.deepEqual(clusterReadiness(['True', 'False', '']), { total: 3, ready: 1 })
  assert.deepEqual(clusterReadiness([]), { total: 0, ready: 0 })
})

test('operationsLine says what a person wants to know first', () => {
  const c = (o: Partial<ReturnType<typeof operationCounts>>) => ({ total: 4, running: 0, failed: 0, paused: 0, done: 4, ...o })
  assert.deepEqual(operationsLine(c({})), ['allCompleted'])
  assert.deepEqual(operationsLine(c({ total: 0, done: 0 })), ['none'])
  assert.deepEqual(operationsLine(c({ running: 2 })), ['running'])
  assert.deepEqual(operationsLine(c({ running: 1, failed: 1, paused: 1 })), ['running', 'failed', 'paused'])
})

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
