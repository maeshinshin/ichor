import assert from 'node:assert/strict'
import { test } from 'node:test'
import { matchesQuery } from './filter.ts'

test('an empty query matches everything', () => {
  assert.equal(matchesQuery('', 'a'), true)
  assert.equal(matchesQuery('  ', undefined), true)
})

test('matching is case-insensitive and any field may match', () => {
  assert.equal(matchesQuery('WORK', 'node-1', 'Worker'), true)
  assert.equal(matchesQuery('zzz', 'node-1', 'Worker'), false)
})

test('every term must match some field', () => {
  assert.equal(matchesQuery('node worker', 'node-1', 'Worker'), true)
  assert.equal(matchesQuery('node control', 'node-1', 'Worker'), false)
})
