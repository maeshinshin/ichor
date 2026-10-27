import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clusterIsReady, clusterReadiness, expiringCertificatesMessage, machineCounts, operationCounts, operationsLine } from './overview.ts'

test('clusterReadiness uses Ready when there is one, else steady and not unhealthy', () => {
  const ready = (status: string) => [{ type: 'Ready', status }]
  assert.deepEqual(clusterReadiness([ready('True'), ready('True')]), { total: 2, ready: 2 })
  assert.deepEqual(clusterReadiness([ready('True'), ready('False'), ready('Unknown')]), { total: 3, ready: 1 })
  assert.deepEqual(clusterReadiness([]), { total: 0, ready: 0 })
  // No Ready condition: a steady cluster counts, a transitioning or unhealthy one does not.
  assert.equal(clusterIsReady([{ type: 'Transitioning', status: 'False' }]), true)
  assert.equal(clusterIsReady([]), true)
  assert.equal(clusterIsReady([{ type: 'Transitioning', status: 'True' }]), false)
  assert.equal(clusterIsReady([{ type: 'Available', status: 'False' }]), false)
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

test('expiringCertificatesMessage returns the message only while CertificatesExpiring is True', () => {
  const on = [{ type: 'Ready', status: 'True' }, { type: 'CertificatesExpiring', status: 'True', message: 'the etcd CA expires on 2026-12-11.' }]
  assert.equal(expiringCertificatesMessage(on), 'the etcd CA expires on 2026-12-11.')
  assert.equal(expiringCertificatesMessage([{ type: 'CertificatesExpiring', status: 'False', message: 'none' }]), null)
  assert.equal(expiringCertificatesMessage([]), null)
})
