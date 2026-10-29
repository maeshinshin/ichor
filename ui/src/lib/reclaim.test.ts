import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canOfferReclaim, reclaimSettled, uuidMatches } from './reclaim.ts'

test('uuidMatches ignores case and surrounding space, never matches empty', () => {
  assert.equal(uuidMatches(' ABC-123 ', 'abc-123'), true)
  assert.equal(uuidMatches('abc-12', 'abc-123'), false)
  assert.equal(uuidMatches('', ''), false)
  assert.equal(uuidMatches('  ', ' '), false)
})

test('canOfferReclaim needs the switch and the verb', () => {
  assert.equal(canOfferReclaim(undefined), false)
  assert.equal(canOfferReclaim({ enabled: true, allowed: false }), false)
  assert.equal(canOfferReclaim({ enabled: false, allowed: true }), false)
  assert.equal(canOfferReclaim({ enabled: true, allowed: true }), true)
})

test('reclaimSettled is true for the end phases only', () => {
  assert.equal(reclaimSettled('Done'), true)
  assert.equal(reclaimSettled('AwaitingConfirmation'), false)
})
