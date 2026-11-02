import assert from 'node:assert/strict'
import { test } from 'node:test'
import { checkDiscoveryAddress, checkDiscoveryCidr, checkDiscoveryTarget, discoveryOn, maskCidr } from './discovery.ts'

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

test('maskCidr clears host bits and rejects what is not an IPv4 range', () => {
  assert.equal(maskCidr('192.168.0.7/24'), '192.168.0.0/24')
  assert.equal(maskCidr(' 10.1.2.77/28 '), '10.1.2.64/28')
  assert.equal(maskCidr('10.0.0.1/32'), '10.0.0.1/32')
  assert.equal(maskCidr('10.0.0.0/33'), null)
  assert.equal(maskCidr('10.0.0.0/'), null)
  assert.equal(maskCidr('10.0.0.0/24/1'), null)
  assert.equal(maskCidr('fe80::/64'), null)
  assert.equal(maskCidr('300.0.0.0/24'), null)
  assert.equal(maskCidr('10.0.0.0'), null)
})

test('checkDiscoveryCidr applies the cluster rules', () => {
  assert.equal(checkDiscoveryCidr('192.168.0.0/24', []), 'ok')
  assert.equal(checkDiscoveryCidr('192.168.0.0/28', ['10.0.0.0/24']), 'ok')
  assert.equal(checkDiscoveryCidr('', []), 'empty')
  assert.equal(checkDiscoveryCidr('10.0.0.0/16', []), 'tooLarge')
  assert.equal(checkDiscoveryCidr('10.0.0.0/23', []), 'tooLarge')
  assert.equal(checkDiscoveryCidr('10.0.0.5/24', ['10.0.0.0/24']), 'duplicate')
  assert.equal(checkDiscoveryCidr('fe80::/64', []), 'invalid')
  assert.equal(checkDiscoveryCidr('10.9.0.0/24', ['a', 'b', 'c', 'd']), 'full')
})

test('checkDiscoveryTarget detects an address or a range', () => {
  assert.deepEqual(checkDiscoveryTarget('192.168.0.50', [], []), { kind: 'address', check: 'ok' })
  assert.deepEqual(checkDiscoveryTarget('192.168.0.0/24', [], []), { kind: 'cidr', check: 'ok' })
  assert.deepEqual(checkDiscoveryTarget('192.168.0.50', ['192.168.0.50'], []), { kind: 'address', check: 'duplicate' })
  assert.deepEqual(checkDiscoveryTarget('10.0.0.0/8', [], []), { kind: 'cidr', check: 'tooLarge' })
  assert.deepEqual(checkDiscoveryTarget('', [], []), { kind: 'address', check: 'empty' })
})

test('discovery is off when there are no addresses and no ranges', () => {
  assert.equal(discoveryOn([], []), false)
  assert.equal(discoveryOn(['10.0.0.1'], []), true)
  assert.equal(discoveryOn([], ['10.0.0.0/24']), true)
})
