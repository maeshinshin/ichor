import assert from 'node:assert/strict'
import { test } from 'node:test'
import { backNowBlock, destinationLabel, exampleBackupYaml, newestSnapshot, totalBytes } from './backups.ts'

const snap = (name: string, seconds?: bigint, size = 1n) => ({ name, time: seconds === undefined ? undefined : { seconds }, size })

test('newestSnapshot finds the latest time', () => {
  assert.equal(newestSnapshot([]), undefined)
  assert.equal(newestSnapshot([snap('a', 1n), snap('b', 5n), snap('c', 3n)])?.name, 'b')
  assert.equal(newestSnapshot([snap('a'), snap('b', 2n)])?.name, 'b')
})

test('totalBytes sums sizes', () => {
  assert.equal(totalBytes([snap('a', 1n, 10n), snap('b', 2n, 5n)]), 15)
})

test('exampleBackupYaml names the cluster', () => {
  const y = exampleBackupYaml({ namespace: 'ns', name: 'c1' })
  assert.ok(y.includes('namespace: ns'))
  assert.ok(y.includes('clusterRef:\n    name: c1'))
})

test('destinationLabel shows bucket, prefix and endpoint', () => {
  assert.equal(destinationLabel(undefined), '-')
  assert.equal(destinationLabel({ endpoint: 'https://s3.x', bucket: 'b', prefix: 'c1' }), 'b/c1 @ https://s3.x')
})

test('backNowBlock refuses a suspended backup and a waiting request', () => {
  assert.equal(backNowBlock({ suspended: false, manualBackupPending: false }), undefined)
  assert.equal(backNowBlock({ suspended: true, manualBackupPending: false }), 'backups.nowSuspended')
  assert.equal(backNowBlock({ suspended: false, manualBackupPending: true }), 'backups.nowPending')
})
