import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Code, ConnectError } from '@connectrpc/connect'
import { describeError, isOverridable } from './errors.ts'

test('a refusal the server marks overridable is told apart from other refusals', () => {
  const marked = new ConnectError('incompatible', Code.FailedPrecondition, { 'Ichor-Overridable': 'true' })
  assert.equal(isOverridable(marked), true)
  assert.equal(isOverridable(new ConnectError('not offered', Code.FailedPrecondition)), false)
  assert.equal(isOverridable(new Error('boom')), false)
})


test('an apiserver validation message (InvalidArgument) is shown verbatim under a headline', () => {
  const msg = 'spec.target: exactly one of role, labelSelector, machineRef must be set'
  assert.deepEqual(describeError(new ConnectError(msg, Code.InvalidArgument)), {
    title: 'Missing or invalid input',
    detail: msg,
  })
})

test('a refused action (FailedPrecondition) is distinguishable from other failures', () => {
  const r = describeError(new ConnectError('machine already approved', Code.FailedPrecondition))
  assert.equal(r.title, 'Not allowed right now')
  assert.equal(r.detail, 'machine already approved')
  assert.equal(describeError(new ConnectError('x', Code.NotFound)).title, 'Not found')
  assert.equal(describeError(new ConnectError('x', Code.Internal)).title, 'Request failed')
})

test('a non-Connect error still yields a message', () => {
  assert.equal(describeError(new Error('boom')).detail, 'boom')
  assert.equal(describeError('plain').detail, 'plain')
})
