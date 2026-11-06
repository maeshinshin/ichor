import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  availableActions,
  captureInterfaces,
  checkCapture,
  collect,
  DownloadTooLarge,
  fileNameOf,
  filterProblem,
  parseDuration,
  type Chunk,
} from './diagnostics.ts'
import { admin } from './roles.testdata.ts'

const all = { ...admin, supportBundle: true, etcdSnapshot: true, packetCapture: true, clusterCredentials: true, clusterUpdate: true, talosconfigReader: true }
const nothing = Object.fromEntries(Object.keys(all).map((k) => [k, false])) as typeof all

test('a user with no permissions, or unknown ones, is offered nothing', () => {
  assert.deepEqual(availableActions(undefined, true), [])
  assert.deepEqual(availableActions(nothing, true), [])
})

test('the etcd snapshot is offered on a control plane node only', () => {
  assert.deepEqual(availableActions(all, true), ['support-bundle', 'etcd-snapshot', 'packet-capture'])
  assert.deepEqual(availableActions(all, false), ['support-bundle', 'packet-capture'])
})

test('each permission shows its own action', () => {
  assert.deepEqual(availableActions({ ...all, supportBundle: false, packetCapture: false }, true), ['etcd-snapshot'])
  assert.deepEqual(availableActions({ ...all, etcdSnapshot: false }, true), ['support-bundle', 'packet-capture'])
})

test('the capture interface defaults to eth0, then the first physical link that is up', () => {
  const l = (name: string, linkUp: boolean, kind = '') => ({ name, linkUp, kind })
  assert.equal(captureInterfaces([l('lo', true), l('eth0', false), l('eth1', true)]).initial, 'eth0')
  assert.equal(captureInterfaces([l('lo', true, 'x'), l('ens3', false), l('ens4', true)]).initial, 'ens4')
  assert.equal(captureInterfaces([l('kubespan', false, 'wireguard')]).initial, 'kubespan')
  assert.deepEqual(captureInterfaces([]), { names: [], initial: '' })
})

test('a duration is whole seconds from 1 to 300', () => {
  assert.equal(parseDuration('30'), 30)
  assert.equal(parseDuration(' 45s '), 45)
  assert.equal(parseDuration('2m'), 120)
  assert.equal(parseDuration('5m'), 300)
  for (const bad of ['', '0', '301', '6m', '-1', '1.5', 'abc', '10h', '99999']) assert.equal(parseDuration(bad), undefined, bad)
})

test('a filter must be tcpdump -dd lines; an expression is refused with a hint', () => {
  assert.equal(filterProblem(''), undefined)
  assert.equal(filterProblem('{ 0x28, 0, 0, 0x0000000c },\n{ 0x6, 0, 0, 0x00040000 },\n'), undefined)
  assert.match(filterProblem('tcp port 80') ?? '', /line 1 is not in tcpdump -dd format/)
  assert.match(filterProblem('{ 0x28, 0, 0, 0x0000000c },\nwat') ?? '', /line 2/)
})

test('the capture form reports every problem at once and otherwise builds the request', () => {
  const bad = checkCapture({ iface: '', duration: 'soon', filter: 'tcp' })
  assert.equal(bad.ok, false)
  if (!bad.ok) assert.deepEqual(Object.keys(bad.errors).sort(), ['duration', 'filter', 'iface'])

  const good = checkCapture({ iface: 'eth0', duration: '2m', filter: '  ' })
  assert.deepEqual(good, { ok: true, interface: 'eth0', durationSeconds: 120, bpfFilter: '' })
})

async function* chunks(...c: Chunk[]) {
  for (const x of c) yield x
}

const enc = (s: string) => new TextEncoder().encode(s)

test('collect keeps the order, the file name and the type from the first chunk', async () => {
  const got = await collect(
    chunks(
      { filename: 'a.zip', contentType: 'application/zip', data: enc('ab') },
      { filename: '', contentType: '', data: enc('cd') },
    ),
  )
  assert.equal(got.filename, 'a.zip')
  assert.equal(got.contentType, 'application/zip')
  assert.equal(got.size, 4)
  assert.equal(new TextDecoder().decode(await new Blob(got.parts).arrayBuffer()), 'abcd')
})

test('collect refuses a stream past the limit', async () => {
  await assert.rejects(collect(chunks({ filename: 'x', contentType: '', data: enc('12345') }), 4), DownloadTooLarge)
})

test('collect surfaces a stream error instead of returning half a file', async () => {
  async function* broken(): AsyncGenerator<Chunk> {
    yield { filename: 'x', contentType: '', data: enc('part') }
    throw new Error('cut off')
  }
  await assert.rejects(collect(broken()), /cut off/)
})

test('a file name falls back to a sensible one', () => {
  const c = { filename: '', contentType: '', parts: [], size: 0 }
  assert.equal(fileNameOf(c, 'packet-capture'), 'capture.pcap')
  assert.equal(fileNameOf(c, 'ca-credentials'), 'credentials.zip')
  assert.equal(fileNameOf({ ...c, filename: 'mine.zip' }, 'support-bundle'), 'mine.zip')
})
