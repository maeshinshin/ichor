import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Code, ConnectError } from '@connectrpc/connect'
import { breakGlassRequest, offersBreakGlass, reasonIsValid } from './breakGlass.ts'
import { describeError } from './errors.ts'
import { setLang } from './lang.ts'

setLang('en')

test('an empty or blank reason cannot be sent', () => {
  assert.equal(reasonIsValid(''), false)
  assert.equal(reasonIsValid('   \n\t'), false)
  assert.equal(reasonIsValid('etcd is down, node must go'), true)
})

test('the request carries the trimmed reason', () => {
  assert.deepEqual(breakGlassRequest('  incident 42  '), { reason: 'incident 42' })
})

test('the emergency path is offered when a pull request was opened', () => {
  assert.equal(offersBreakGlass({ pullRequestUrl: 'https://git.example/pr/1' }), true)
  assert.equal(offersBreakGlass({ pullRequestUrl: '' }), false)
  assert.equal(offersBreakGlass({}), false)
})

test('the emergency path is offered after a refusal, not after other failures', () => {
  assert.equal(offersBreakGlass({ error: new ConnectError('tracked', Code.FailedPrecondition) }), true)
  assert.equal(offersBreakGlass({ error: new ConnectError('webhook', Code.PermissionDenied) }), true)
  assert.equal(offersBreakGlass({ error: new ConnectError('x', Code.Unavailable) }), false)
  assert.equal(offersBreakGlass({ error: new ConnectError('x', Code.InvalidArgument) }), false)
  assert.equal(offersBreakGlass({ error: new Error('boom') }), false)
})

test('a permission denial is shown with the server reason, as "not allowed"', () => {
  const r = describeError(new ConnectError('you do not hold the break-glass verb on talosmachines/m1', Code.PermissionDenied))
  assert.equal(r.title, 'Not allowed right now')
  assert.equal(r.detail, 'you do not hold the break-glass verb on talosmachines/m1')
})
