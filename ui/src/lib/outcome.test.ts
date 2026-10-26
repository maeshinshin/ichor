import assert from 'node:assert/strict'
import { test } from 'node:test'
import { outcomeMessage } from './outcome.ts'

test('a write that opened a pull request does not say it is done', () => {
  assert.equal(outcomeMessage({ pullRequestUrl: '' }, 'Saved', 'Pull request opened'), 'Saved')
  assert.equal(outcomeMessage({}, 'Saved', 'Pull request opened'), 'Saved')
  assert.equal(outcomeMessage({ pullRequestUrl: 'https://x/pull/1' }, 'Saved', 'Pull request opened'), 'Pull request opened')
})
