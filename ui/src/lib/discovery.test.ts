import assert from 'node:assert/strict'
import { test } from 'node:test'
import { checkDiscoveryAddress, discoveryOn } from './discovery.ts'

test('checkDiscoveryAddress accepts one plain IPv4 address', () => {
  assert.equal(checkDiscoveryAddress('192.168.0.50', []), 'ok')
  assert.equal(checkDiscoveryAddress('  10.0.0.1 ', ['10.0.0.2']), 'ok')
})

test('checkDiscoveryAddress names the problem', () => {
  assert.equal(checkDiscoveryAddress('', []), 'empty')
  assert.equal(checkDiscoveryAddress('   ', []), 'empty')
  assert.equal(checkDiscoveryAddress('node-1', []), 'invalid')
  assert.equal(checkDiscoveryAddress('10.0.0.0/24', []), 'invalid')
  assert.equal(checkDiscoveryAddress('300.1.1.1', []), 'invalid')
  assert.equal(checkDiscoveryAddress('1.2.3', []), 'invalid')
  assert.equal(checkDiscoveryAddress('01.2.3.4', []), 'invalid')
  assert.equal(checkDiscoveryAddress('fe80::1', []), 'invalid')
  assert.equal(checkDiscoveryAddress('10.0.0.1', ['10.0.0.1']), 'duplicate')
})

test('discovery is off when there are no addresses and no ranges', () => {
  assert.equal(discoveryOn([], []), false)
  assert.equal(discoveryOn(['10.0.0.1'], []), true)
  assert.equal(discoveryOn([], ['10.0.0.0/24']), true)
})
