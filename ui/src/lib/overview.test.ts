import assert from 'node:assert/strict'
import { test } from 'node:test'
import { attentionItems, expiringCertificatesMessage, machineCounts } from './overview.ts'

test('attentionItems lists only what applies', () => {
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, opState: 'running' }), [])
  const conds = [
    { type: 'CertificatesExpiring', status: 'True', message: 'etcd CA expires' },
    { type: 'BundleBackupStale', status: 'True', message: 'bundle is old' },
  ]
  assert.deepEqual(attentionItems({ conditions: conds, pending: 2, configWaiting: 1, opState: 'failed' }), [
    { kind: 'opFailed' },
    { kind: 'certs', message: 'etcd CA expires' },
    { kind: 'backupStale', message: 'bundle is old' },
    { kind: 'pending', count: 2 },
    { kind: 'config', count: 1 },
  ])
  assert.deepEqual(attentionItems({ conditions: [{ type: 'BundleBackupStale', status: 'False' }], pending: 0, configWaiting: 0, opState: 'paused' }), [{ kind: 'opPaused' }])
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

test('expiringCertificatesMessage returns the message only while CertificatesExpiring is True', () => {
  const on = [{ type: 'Ready', status: 'True' }, { type: 'CertificatesExpiring', status: 'True', message: 'the etcd CA expires on 2026-12-11.' }]
  assert.equal(expiringCertificatesMessage(on), 'the etcd CA expires on 2026-12-11.')
  assert.equal(expiringCertificatesMessage([{ type: 'CertificatesExpiring', status: 'False', message: 'none' }]), null)
  assert.equal(expiringCertificatesMessage([]), null)
})
