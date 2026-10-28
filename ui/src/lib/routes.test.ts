import assert from 'node:assert/strict'
import { test } from 'node:test'
import { redirectFor } from './routes.ts'

test('old cluster URLs go to the overview or add-node', () => {
  assert.equal(redirectFor('/clusters'), '/')
  assert.equal(redirectFor('/clusters/ns/demo'), '/')
  assert.equal(redirectFor('/clusters/ns/demo/add-node'), '/nodes/add')
  assert.equal(redirectFor('/clusters/ns/demo/other'), null)
})

test('namespaced object URLs drop the namespace', () => {
  assert.equal(redirectFor('/machines'), '/nodes')
  assert.equal(redirectFor('/nodes/ns/n%201'), '/nodes/n%201')
  assert.equal(redirectFor('/operations/ns/op-1'), '/operations/op-1')
})

test('patches become config', () => {
  assert.equal(redirectFor('/patches'), '/config')
  assert.equal(redirectFor('/patches/new'), '/config/new')
  assert.equal(redirectFor('/patches/ns/p1'), '/config/p1')
})

test('current paths are not redirected', () => {
  for (const p of ['/', '/nodes', '/nodes/add', '/nodes/n1', '/operations', '/operations/op', '/backups', '/cilium', '/config', '/config/new', '/config/x', '/gitops', '/users', '/nope'])
    assert.equal(redirectFor(p), null, p)
})
