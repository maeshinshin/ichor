import assert from 'node:assert/strict'
import { test } from 'node:test'
import { caNeedsCredentials, caTargetChoices, defaultCATargets, toggleCATarget } from './caTargets.ts'
import { credentialsPanel } from './credentials.ts'

test('the four CAs are offered in a fixed order, the Talos and Kubernetes API CAs ticked by default', () => {
  assert.deepEqual(caTargetChoices, ['talos', 'kubernetes', 'etcd', 'kubernetes-aggregator'])
  assert.deepEqual(defaultCATargets, ['talos', 'kubernetes'])
})

test('ticking and unticking keeps the order of the choices', () => {
  assert.deepEqual(toggleCATarget(['talos', 'kubernetes'], 'kubernetes-aggregator', true), ['talos', 'kubernetes', 'kubernetes-aggregator'])
  assert.deepEqual(toggleCATarget(['kubernetes-aggregator', 'talos'], 'etcd', true), ['talos', 'etcd', 'kubernetes-aggregator'])
  assert.deepEqual(toggleCATarget(['talos', 'etcd'], 'talos', false), ['etcd'])
  assert.deepEqual(toggleCATarget(['etcd'], 'etcd', false), [])
})

test('only the Talos and Kubernetes API CAs change what an operator holds', () => {
  assert.equal(caNeedsCredentials([]), true) // an operation that says nothing is the default pair
  assert.equal(caNeedsCredentials(['talos', 'kubernetes']), true)
  assert.equal(caNeedsCredentials(['etcd', 'talos']), true)
  assert.equal(caNeedsCredentials(['kubernetes']), true)
  assert.equal(caNeedsCredentials(['etcd']), false)
  assert.equal(caNeedsCredentials(['kubernetes-aggregator']), false)
  assert.equal(caNeedsCredentials(['etcd', 'kubernetes-aggregator']), false)
})

test('a rotation of etcd or the aggregator CA alone has no credentials panel', () => {
  assert.equal(credentialsPanel(true, 'AwaitingCredentials', false, ['etcd']), 'none')
  assert.equal(credentialsPanel(true, 'Issuing', false, ['etcd', 'kubernetes-aggregator']), 'none')
  assert.equal(credentialsPanel(true, 'Issuing', false, ['etcd', 'talos']), 'issued')
  assert.equal(credentialsPanel(true, 'AwaitingCredentials', false, []), 'gate')
})
