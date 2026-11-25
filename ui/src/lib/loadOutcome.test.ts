import assert from 'node:assert/strict'
import { test } from 'node:test'
import { loadOutcome } from './loadOutcome.ts'

test('a successful load returns the value and no error', async () => {
  assert.deepEqual(await loadOutcome(async () => [1, 2]), { value: [1, 2], error: null })
})

test('a failed load returns null and the error, never an empty list', async () => {
  const err = new Error('unavailable')
  assert.deepEqual(await loadOutcome<number[]>(async () => { throw err }), { value: null, error: err })
})
