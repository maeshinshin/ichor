import assert from 'node:assert/strict'
import { test } from 'node:test'
import { breadcrumbsFor } from './breadcrumbs.ts'

test('a top-level route is a single current crumb', () => {
  assert.deepEqual(breadcrumbsFor('/'), [{ label: 'nav.overview' }])
  assert.deepEqual(breadcrumbsFor('/nodes'), [{ label: 'nav.machines' }])
  assert.deepEqual(breadcrumbsFor('/config'), [{ label: 'nav.config' }])
  assert.deepEqual(breadcrumbsFor('/backups'), [{ label: 'nav.backups' }])
  assert.deepEqual(breadcrumbsFor('/gitops'), [{ label: 'nav.gitops' }])
  assert.deepEqual(breadcrumbsFor('/users'), [{ label: 'nav.users' }])
})

test('a detail route links its section and names the object', () => {
  assert.deepEqual(breadcrumbsFor('/nodes/node-1'), [{ label: 'nav.machines', to: '/nodes' }, { text: 'node-1' }])
  assert.deepEqual(breadcrumbsFor('/operations/op-1'), [{ label: 'nav.operations', to: '/operations' }, { text: 'op-1' }])
  assert.deepEqual(breadcrumbsFor('/config/p%201'), [{ label: 'nav.config', to: '/config' }, { text: 'p 1' }])
})

test('add node and new config sit under their lists', () => {
  assert.deepEqual(breadcrumbsFor('/nodes/add'), [{ label: 'nav.machines', to: '/nodes' }, { label: 'cluster.addNode' }])
  assert.deepEqual(breadcrumbsFor('/config/new'), [{ label: 'nav.config', to: '/config' }, { label: 'patch.newTitle' }])
})

test('old and unknown paths are not found', () => {
  assert.deepEqual(breadcrumbsFor('/clusters'), [{ label: 'common.notFound' }])
  assert.deepEqual(breadcrumbsFor('/nodes/ns/x'), [{ label: 'common.notFound' }])
  assert.deepEqual(breadcrumbsFor('/nope/x'), [{ label: 'common.notFound' }])
})

test('a published title replaces the name in the last crumb', () => {
  const titles = { 'nodes/ns/9f95-uuid': 'cp-1' }
  assert.deepEqual(breadcrumbsFor('/nodes/9f95-uuid', titles, 'ns'), [{ label: 'nav.machines', to: '/nodes' }, { text: 'cp-1' }])
  assert.deepEqual(breadcrumbsFor('/nodes/other', titles, 'ns'), [{ label: 'nav.machines', to: '/nodes' }, { text: 'other' }])
})
