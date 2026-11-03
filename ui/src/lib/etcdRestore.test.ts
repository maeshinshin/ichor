import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canRestore, canReview, restoreStepProgress, snapshotChoices } from './etcdRestore.ts'

const backup = (name: string, snaps: [string, number][]) => ({
  ref: { namespace: 'ichor', name },
  snapshots: snaps.map(([n, s]) => ({ name: n, time: { seconds: BigInt(s) }, size: 4096n })),
})

test('snapshots of every backup are one list, newest first', () => {
  const choices = snapshotChoices([backup('a', [['a-new', 300], ['a-old', 100]]), backup('b', [['b-mid', 200]])])
  assert.deepEqual(choices.map((c) => c.name), ['a-new', 'b-mid', 'a-old'])
  assert.equal(choices[1].backup.name, 'b')
  assert.equal(choices[0].size, 4096)
})

test('the same snapshot name in two backups stays two choices', () => {
  const choices = snapshotChoices([backup('a', [['same', 100]]), backup('b', [['same', 100]])])
  assert.equal(new Set(choices.map((c) => c.key)).size, 2)
})

test('a backup without a ref or without snapshots adds nothing', () => {
  assert.deepEqual(snapshotChoices([{ snapshots: [{ name: 'x', size: 1n }] }, backup('a', [])]), [])
})

test('review needs a chosen snapshot and nothing in flight', () => {
  const choice = snapshotChoices([backup('a', [['s', 1]])])[0]
  assert.equal(canReview({ choice, busy: false }), true)
  assert.equal(canReview({ choice: undefined, busy: false }), false)
  assert.equal(canReview({ choice, busy: true }), false)
})

const ready = { planned: true, blocked: '', clusterName: 'prod', typed: 'prod', overridden: true, busy: false }

test('restore needs the plan, the typed cluster name and the override tick, and never goes on a blocked plan', () => {
  assert.equal(canRestore(ready), true)
  assert.equal(canRestore({ ...ready, planned: false }), false)
  assert.equal(canRestore({ ...ready, blocked: 'another operation is running' }), false)
  assert.equal(canRestore({ ...ready, typed: 'pro' }), false)
  assert.equal(canRestore({ ...ready, typed: ' prod ' }), true)
  assert.equal(canRestore({ ...ready, typed: 'Prod' }), false)
  assert.equal(canRestore({ ...ready, overridden: false }), false)
  assert.equal(canRestore({ ...ready, busy: true }), false)
  assert.equal(canRestore({ ...ready, clusterName: '', typed: '' }), false)
})

test('the step shown is its place in the four steps', () => {
  assert.deepEqual(restoreStepProgress('Wiping'), { name: 'Wiping', index: 1, total: 4 })
  assert.deepEqual(restoreStepProgress('Reconciling'), { name: 'Reconciling', index: 4, total: 4 })
  assert.equal(restoreStepProgress(''), null)
  assert.equal(restoreStepProgress('Whatever'), null)
})
