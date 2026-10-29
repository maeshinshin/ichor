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

import { backupsValue, newestSnapshotSeconds, nodesValue, tileTargets, timeAgo, versionLabel } from './overview.ts'

test('timeAgo picks the coarsest unit', () => {
  const now = 1_000_000_000
  assert.equal(timeAgo(now, now - 5_000), 'just now')
  assert.equal(timeAgo(now, now + 5_000), 'just now')
  assert.equal(timeAgo(now, now - 5 * 60_000), '5 min ago')
  assert.equal(timeAgo(now, now - 3 * 3600_000 - 59_000), '3 h ago')
  assert.equal(timeAgo(now, now - 49 * 3600_000), '2 d ago')
})

test('backupsValue: not loaded, not configured, no snapshot, newest age', () => {
  const now = 100_000_000
  const snap = (s: number) => ({ time: { seconds: BigInt(s) } })
  assert.equal(backupsValue(null, now), '-')
  assert.equal(backupsValue([], now), 'Not configured')
  assert.equal(backupsValue([{ snapshots: [] }], now), 'No snapshot yet')
  const backups = [{ snapshots: [snap(now / 1000 - 7200), snap(now / 1000 - 3600)] }, { snapshots: [snap(now / 1000 - 90000)] }]
  assert.equal(newestSnapshotSeconds(backups), now / 1000 - 3600)
  assert.equal(backupsValue(backups, now), '1 h ago')
})

test('tile values and targets', () => {
  assert.equal(nodesValue(3, 4), '3 / 4 ready')
  assert.equal(versionLabel('1.37.1'), 'v1.37.1')
  assert.equal(versionLabel('v1.14.3'), 'v1.14.3')
  assert.equal(versionLabel(''), '-')
  assert.deepEqual(tileTargets(null).operation, { kind: 'route', to: '/operations' })
  assert.deepEqual(tileTargets('upgrade-1').operation, { kind: 'route', to: '/operations/upgrade-1' })
  assert.deepEqual(tileTargets(null).nodes, { kind: 'route', to: '/nodes' })
  assert.deepEqual(tileTargets(null).backups, { kind: 'route', to: '/backups' })
  assert.deepEqual(tileTargets(null).versions, { kind: 'anchor', id: 'versions' })
})
