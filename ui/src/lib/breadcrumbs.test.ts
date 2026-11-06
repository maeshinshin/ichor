import assert from 'node:assert/strict'
import { test } from 'node:test'
import { breadcrumbsFor } from './breadcrumbs.ts'

test('a top-level route is a single current crumb', () => {
  assert.deepEqual(breadcrumbsFor('/'), [{ label: 'nav.overview' }])
  assert.deepEqual(breadcrumbsFor('/nodes'), [{ label: 'nodes.title' }])
  assert.deepEqual(breadcrumbsFor('/config'), [{ label: 'config.title' }])
  assert.deepEqual(breadcrumbsFor('/cilium'), [{ label: 'nav.cilium' }])
  assert.deepEqual(breadcrumbsFor('/users'), [{ label: 'nav.users' }])
  assert.deepEqual(breadcrumbsFor('/backups'), [{ label: 'nav.backups' }])
  assert.deepEqual(breadcrumbsFor('/settings'), [{ label: 'nav.settings' }])
})

test('a detail route links its section and names the object', () => {
  assert.deepEqual(breadcrumbsFor('/nodes/node-1'), [{ label: 'nodes.title', to: '/nodes' }, { text: 'node-1' }])
  assert.deepEqual(breadcrumbsFor('/operations/op-1'), [{ label: 'nav.operations', to: '/operations' }, { text: 'op-1' }])
  assert.deepEqual(breadcrumbsFor('/config/p%201'), [{ label: 'config.title', to: '/config' }, { text: 'p 1' }])
})

test('the add-node page sits under nodes', () => {
  assert.deepEqual(breadcrumbsFor('/nodes/add'), [{ label: 'nodes.title', to: '/nodes' }, { label: 'addNode.pageTitle' }])
})

test('a new patch and an unknown path', () => {
  assert.deepEqual(breadcrumbsFor('/config/new'), [{ label: 'config.title', to: '/config' }, { label: 'patch.newTitle' }])
  assert.deepEqual(breadcrumbsFor('/nope/x'), [{ label: 'common.notFound' }])
})

test('a published title replaces the name in the last crumb', () => {
  const titles = { 'nodes/9f95-uuid': 'cp-1' }
  assert.deepEqual(breadcrumbsFor('/nodes/9f95-uuid', titles), [{ label: 'nodes.title', to: '/nodes' }, { text: 'cp-1' }])
  assert.deepEqual(breadcrumbsFor('/nodes/other', titles), [{ label: 'nodes.title', to: '/nodes' }, { text: 'other' }])
})
