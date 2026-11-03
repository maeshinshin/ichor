import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Disk } from '@/gen/ichor/v1alpha1/node_pb'
import {
  defaultInstallDisk,
  diskKey,
  diskSelector,
  installChoice,
  isValidHostname,
  needsInstallDisk,
  type DiskOption,
} from './installDisk.ts'

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

test('the picker prefers a listed disk and only accepts a typed device path when none are listed', () => {
  const d = disk({ wwid: 'w' })
  assert.deepEqual(installChoice({ disks: [d], key: diskKey(d), typed: '/dev/x', image: ' img ' }).install, {
    disk: { wwid: 'w' },
    image: 'img',
  })
  assert.equal(installChoice({ disks: [d], key: '', typed: '', image: '' }).install, undefined)
  const typed = installChoice({ disks: [], key: '', typed: ' /dev/sda ', image: '' })
  assert.deepEqual(typed.install, { disk: { devPath: '/dev/sda' }, image: '' })
  assert.equal(typed.ok, true)
  assert.equal(installChoice({ disks: [], key: '', typed: 'sda', image: '' }).ok, false)
})

test('only an approved machine without install and waiting for a disk offers to set one', () => {
  const waiting = [{ type: 'Ready', reason: 'InstallDiskUnknown' }]
  assert.equal(needsInstallDisk({ approved: true, conditions: waiting }), true)
  assert.equal(needsInstallDisk({ approved: true, install: {}, conditions: waiting }), false)
  assert.equal(needsInstallDisk({ approved: false, conditions: waiting }), false)
  assert.equal(needsInstallDisk({ approved: true, conditions: [{ type: 'Ready', reason: 'NodeJoined' }] }), false)
})

test('the install picker starts on the smallest disk that can take Talos', () => {
  const GiB = 1024 ** 3
  const opt = (d: Partial<DiskOption>): DiskOption => ({
    wwid: '', serial: '', byIdPath: '', busPath: '', devPath: '', sizeBytes: 0, ...d,
  })
  const small = opt({ wwid: 'w-small', sizeBytes: 20 * GiB })
  const big = opt({ wwid: 'w-big', sizeBytes: 480 * GiB })
  const tiny = opt({ wwid: 'w-tiny', sizeBytes: 2 * GiB })
  const unnamed = opt({ sizeBytes: 100 * GiB })
  assert.equal(defaultInstallDisk([big, small, tiny]), small)
  assert.equal(defaultInstallDisk([tiny, unnamed]), undefined, 'too small, or nothing to select it by')
  assert.equal(defaultInstallDisk([]), undefined)
  // Equal sizes keep the first one listed.
  assert.equal(defaultInstallDisk([small, opt({ wwid: 'w-twin', sizeBytes: 20 * GiB })]), small)
})
