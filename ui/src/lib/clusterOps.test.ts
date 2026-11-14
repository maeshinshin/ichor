import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  blockingOperation,
  recentOperations,
  apiRestartPendingCount,
  rebootPendingCount,
  versionProblem,
  type OpRow,
} from './clusterOps.ts'

const op = (name: string, phase: string, created: number, extra: Partial<OpRow> = {}): OpRow => ({
  name,
  phase,
  failureReason: '',
  isNode: false,
  created,
  ...extra,
})

test('an unfinished operation blocks; a completed one and a node shut down do not', () => {
  assert.equal(blockingOperation([op('a', 'Completed', 1)]), null)
  assert.equal(blockingOperation([op('a', 'ShutDown', 1, { isNode: true })]), null)
  assert.equal(blockingOperation([op('a', 'Completed', 2), op('b', 'InProgress', 1)])?.name, 'b')
  assert.equal(blockingOperation([op('a', 'ShutDown', 1)])?.name, 'a') // a cluster shutdown still holds
  assert.equal(blockingOperation([op('a', '', 1)])?.name, 'a') // not seen by the controller yet
  assert.equal(blockingOperation([op('a', 'Paused', 1, { failureReason: 'x' })])?.name, 'a')
})

test('the current operation is the newest one that is not finished', () => {
  assert.equal(blockingOperation([op('old', 'InProgress', 1), op('new', 'Paused', 5)])?.name, 'new')
  assert.equal(blockingOperation([op('a', 'Completed', 5)]), null)
})

test('recent operations are newest first and limited', () => {
  const ops = [1, 2, 3, 4, 5, 6, 7].map((n) => op(`o${n}`, 'Completed', n))
  assert.deepEqual(
    recentOperations(ops, 5).map((o) => o.name),
    ['o7', 'o6', 'o5', 'o4', 'o3'],
  )
  assert.deepEqual(recentOperations([], 5), [])
})

test('a version must be a semantic version, different from and newer than the current one', () => {
  assert.equal(versionProblem('v1.14.3', ''), 'required')
  assert.equal(versionProblem('v1.14.3', 'latest'), 'invalid')
  assert.equal(versionProblem('v1.14.3', 'v1.14.3'), 'same')
  assert.equal(versionProblem('v1.14.3', '1.14.3'), 'same') // the leading v is optional
  assert.equal(versionProblem('v1.14.3', 'v1.14.2'), 'older')
  assert.equal(versionProblem('v1.14.3', 'v1.9.9'), 'older') // numbers, not text
  assert.equal(versionProblem('v1.14.3', 'v1.14.4'), null)
  assert.equal(versionProblem('v1.14.3', 'v1.15.0'), null)
  assert.equal(versionProblem('v1.14.3', 'v2.0.0'), null)
})

test('a release is newer than its own pre-release', () => {
  assert.equal(versionProblem('v1.14.3-rc.1', 'v1.14.3'), null)
  assert.equal(versionProblem('v1.14.3', 'v1.14.3-rc.1'), 'older')
  assert.equal(versionProblem('v1.14.3-rc.1', 'v1.14.3-rc.2'), null)
  assert.equal(versionProblem('v1.14.3-rc.2', 'v1.14.3-rc.10'), null)
})

test('an unknown current version only checks the shape', () => {
  assert.equal(versionProblem('', 'v1.0.0'), null)
  assert.equal(versionProblem('', 'x'), 'invalid')
})

test('nodes that wait for a reboot are counted', () => {
  const m = (rebootPaths: string[], readyReason = '') => ({ rebootPaths, readyReason })
  assert.equal(rebootPendingCount([m([]), m(['machine.install']), m(['a', 'b'])]), 2)
  assert.equal(rebootPendingCount([m([], 'RebootRequired'), m([], 'Ready')]), 1)
  assert.equal(rebootPendingCount([]), 0)
  // An API server restart needs no reboot, so it is not counted as one.
  assert.equal(rebootPendingCount([m([], 'APIServerRestartRequired')]), 0)
  assert.equal(apiRestartPendingCount([m([], 'APIServerRestartRequired'), m([], 'RebootRequired'), m([])]), 1)
})

