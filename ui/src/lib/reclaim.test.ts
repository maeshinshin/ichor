import assert from 'node:assert/strict'
import { test } from 'node:test'
import { reclaimSettled, usesClusterCredentials, uuidMatches } from './reclaim.ts'

test('uuidMatches ignores case and surrounding space, never matches empty', () => {
  assert.equal(uuidMatches(' ABC-123 ', 'abc-123'), true)
  assert.equal(uuidMatches('abc-12', 'abc-123'), false)
  assert.equal(uuidMatches('', ''), false)
  assert.equal(uuidMatches('  ', ' '), false)
})

test('reclaimSettled is true for the end phases only', () => {
  assert.equal(reclaimSettled('Done'), true)
  assert.equal(reclaimSettled('AwaitingConfirmation'), false)
})

test('usesClusterCredentials needs the right to update the cluster as well as the choice', () => {
  assert.equal(usesClusterCredentials(true, true), true)
  assert.equal(usesClusterCredentials(true, false), false)
  assert.equal(usesClusterCredentials(false, true), false)
})
