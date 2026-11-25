import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Code, ConnectError } from '@connectrpc/connect'
import { isChanged, withBase } from './baseVersion.ts'

test('a replace request carries the version it was loaded at', () => {
  assert.deepEqual(withBase({ cluster: { name: 'c' }, userYaml: 'a: 1' }, '42'), {
    cluster: { name: 'c' },
    userYaml: 'a: 1',
    baseResourceVersion: '42',
  })
})

test('only an Aborted refusal is the changed-since-opened case', () => {
  assert.equal(isChanged(new ConnectError('changed', Code.Aborted)), true)
  assert.equal(isChanged(new ConnectError('no', Code.InvalidArgument)), false)
  assert.equal(isChanged(new Error('x')), false)
})
