import assert from 'node:assert/strict'
import { test } from 'node:test'
import { pullRequestHref } from './pullRequest.ts'

test('an https pull request URL is linkable', () => {
  assert.equal(pullRequestHref('https://git.example.com/acme/infra/pulls/3'), 'https://git.example.com/acme/infra/pulls/3')
})

test('anything that is not http(s) is not linked', () => {
  assert.equal(pullRequestHref('javascript:alert(1)'), null)
  assert.equal(pullRequestHref('not a url'), null)
  assert.equal(pullRequestHref(''), null)
})
