import assert from 'node:assert/strict'
import { test } from 'node:test'

test('attentionItems lists only what applies', () => {
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, opState: 'running' }), [])
  const conds = [
    { type: 'CertificatesExpiring', status: 'True', message: 'etcd CA expires' },
    { type: 'BundleBackupStale', status: 'True', message: 'bundle is old' },
    { type: 'CAClientsNotRestarted', status: 'True', message: 'coredns not back' },
  ]
  assert.deepEqual(attentionItems({ conditions: conds, pending: 2, configWaiting: 1, opState: 'failed' }), [
    { kind: 'opFailed' },
    { kind: 'certs', message: 'etcd CA expires' },
    { kind: 'backupStale', message: 'bundle is old' },
    { kind: 'clientsNotRestarted', message: 'coredns not back' },
    { kind: 'pending', count: 2 },
    { kind: 'config', count: 1 },
  ])
  assert.deepEqual(attentionItems({ conditions: [{ type: 'BundleBackupStale', status: 'False' }], pending: 0, configWaiting: 0, opState: 'paused' }), [{ kind: 'opPaused' }])
})

test('patch updates are one attention item naming the components, before the Ichor release notice', () => {
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, opState: null, patches: ['Talos', 'Kubernetes'], release: 'v0.3.0' }), [
    { kind: 'patch', components: ['Talos', 'Kubernetes'] },
    { kind: 'release', version: 'v0.3.0' },
  ])
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, opState: null, patches: [] }), [])
})

import { attentionItems, backupsValue, tileTargets, timeAgo, versionLabel } from './overview.ts'

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
  assert.equal(backupsValue(backups, now), '1 h ago')
})

test('tile values and targets', () => {
  assert.equal(versionLabel('1.37.1'), 'v1.37.1')
  assert.equal(versionLabel('v1.14.3'), 'v1.14.3')
  assert.equal(versionLabel(''), '-')
  assert.deepEqual(tileTargets(null).operation, { kind: 'route', to: '/operations' })
  assert.deepEqual(tileTargets('upgrade-1').operation, { kind: 'route', to: '/operations/upgrade-1' })
  assert.deepEqual(tileTargets(null).nodes, { kind: 'route', to: '/nodes' })
  assert.deepEqual(tileTargets(null).backups, { kind: 'route', to: '/backups' })
  assert.deepEqual(tileTargets(null).versions, { kind: 'anchor', id: 'versions' })
})

test('attentionItems lists a halted automatic patcher with the operation that stopped it', () => {
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, opState: null, autoPatchHalted: 'ichor-system/talos-x1' }), [
    { kind: 'autoPatchHalted', operation: 'ichor-system/talos-x1' },
  ])
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, opState: null, autoPatchHalted: '' }), [])
})

test('attentionItems lists an installer image whose signature was not checked', () => {
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, opState: null, installerUnverified: true }), [
    { kind: 'installerUnverified' },
  ])
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, opState: null, installerUnverified: false }), [])
})

test('nodes waiting for an API server restart are their own attention item, not a reboot', () => {
  assert.deepEqual(attentionItems({ conditions: [], pending: 0, configWaiting: 0, apiRestartWaiting: 1, opState: null }), [{ kind: 'apiRestart', count: 1 }])
})
