import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Disk } from '@/gen/ichor/v1alpha1/node_pb'
import { diskKey, diskSelector, isValidHostname } from './installDisk.ts'

const disk = (d: Partial<Disk>) => d as Disk

test('hostname accepts RFC 1123 names and an empty value, and refuses anything else', () => {
  for (const ok of ['', 'node-1', 'a', 'node-1.lab.example.com']) assert.equal(isValidHostname(ok), true, ok)
  for (const bad of ['てすと', 'Node1', 'node_1', '-node', 'node-', 'node.', '.node', 'a b', 'a'.repeat(254)]) {
    assert.equal(isValidHostname(bad), false, bad)
  }
})

test('a disk is selected by the most stable identifier it has', () => {
  assert.deepEqual(diskSelector(disk({ wwid: 'w', serial: 's', devPath: '/dev/sda' })), { wwid: 'w' })
  assert.deepEqual(diskSelector(disk({ serial: 's', byIdPath: '/dev/disk/by-id/x' })), { serial: 's' })
  assert.deepEqual(diskSelector(disk({ byIdPath: '/dev/disk/by-id/x', busPath: '/pci' })), { byIdPath: '/dev/disk/by-id/x' })
  assert.deepEqual(diskSelector(disk({ busPath: '/pci', devPath: '/dev/sda' })), { busPath: '/pci' })
  assert.deepEqual(diskSelector(disk({ devPath: '/dev/vda' })), { devPath: '/dev/vda' })
  assert.equal(diskSelector(disk({})), undefined)
})

test('two disks never share a form key', () => {
  assert.notEqual(diskKey(disk({ wwid: 'a' })), diskKey(disk({ wwid: 'b' })))
})
