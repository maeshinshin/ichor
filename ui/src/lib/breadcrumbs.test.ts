import assert from 'node:assert/strict'
import { test } from 'node:test'
import { breadcrumbsFor } from './breadcrumbs.ts'

test('a top-level route is a single current crumb', () => {
  assert.deepEqual(breadcrumbsFor('/'), [{ label: 'nav.overview' }])
  assert.deepEqual(breadcrumbsFor('/clusters'), [{ label: 'nav.clusters' }])
  assert.deepEqual(breadcrumbsFor('/machines'), [{ label: 'nav.machines' }])
  assert.deepEqual(breadcrumbsFor('/gitops'), [{ label: 'nav.gitops' }])
})

test('a detail route links its section and names the object', () => {
  assert.deepEqual(breadcrumbsFor('/clusters/ns/demo'), [{ label: 'nav.clusters', to: '/clusters' }, { text: 'demo' }])
  assert.deepEqual(breadcrumbsFor('/nodes/ns/node-1'), [{ label: 'nav.machines', to: '/machines' }, { text: 'node-1' }])
  assert.deepEqual(breadcrumbsFor('/operations/ns/op-1'), [{ label: 'nav.operations', to: '/operations' }, { text: 'op-1' }])
  assert.deepEqual(breadcrumbsFor('/patches/ns/p%201'), [{ label: 'nav.patches', to: '/patches' }, { text: 'p 1' }])
})

test('a new patch and an unknown path', () => {
  assert.deepEqual(breadcrumbsFor('/patches/new'), [{ label: 'nav.patches', to: '/patches' }, { label: 'patch.newTitle' }])
  assert.deepEqual(breadcrumbsFor('/nope/x'), [{ label: 'common.notFound' }])
})

test('a published title replaces the name in the last crumb', () => {
  const titles = { 'nodes/ns/9f95-uuid': 'cp-1' }
  assert.deepEqual(breadcrumbsFor('/nodes/ns/9f95-uuid', titles), [{ label: 'nav.machines', to: '/machines' }, { text: 'cp-1' }])
  assert.deepEqual(breadcrumbsFor('/nodes/ns/other', titles), [{ label: 'nav.machines', to: '/machines' }, { text: 'other' }])
})
