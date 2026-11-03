import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Disk } from '@/gen/ichor/v1alpha1/node_pb'
import type { TalosLink } from '@/gen/ichor/v1alpha1/talosdetails_pb'
import { diskKey } from './installDisk.ts'
import {
  defaultLink,
  diskRolesChoice,
  isCidr,
  isIp,
  isValidMac,
  linkLabel,
  networkChoice,
  selectableLinks,
  splitList,
  type NetworkForm,
} from './machineSettings.ts'

const link = (l: Partial<TalosLink>) => ({ kind: '', linkUp: true, speedMbit: 0, permanentMac: '', ...l }) as TalosLink
const disk = (d: Partial<Disk>) => d as Disk

test('addresses and MACs are checked the way the CRD checks them', () => {
  for (const ok of ['10.0.0.1', '255.255.255.255', 'fd00::1', '2001:db8::7']) assert.equal(isIp(ok), true, ok)
  for (const bad of ['', '10.0.0', '256.0.0.1', 'dns.example.com', '10.0.0.1/24', 'fd00:::1', '0.0.0.0', '::']) {
    assert.equal(isIp(bad), false, bad)
  }
  for (const ok of ['192.168.1.10/24', '10.0.0.2/32', 'fd00::10/64']) assert.equal(isCidr(ok), true, ok)
  for (const bad of ['192.168.1.10', '192.168.1.10/33', 'fd00::1/129', '192.168.1.10/', '10.0.0.0/-1', '/24', '0.0.0.0/0']) {
    assert.equal(isCidr(bad), false, bad)
  }
  assert.equal(isValidMac('aa:bb:cc:dd:ee:01'), true)
  assert.equal(isValidMac('AA:BB:CC:DD:EE:01'), true)
  assert.equal(isValidMac('aa-bb-cc-dd-ee-01'), false)
  assert.equal(isValidMac('aa:bb:cc:dd:ee'), false)
})

test('a list is split on commas, spaces and new lines', () => {
  assert.deepEqual(splitList('10.0.0.1, 10.0.0.2\n10.0.0.3  '), ['10.0.0.1', '10.0.0.2', '10.0.0.3'])
  assert.deepEqual(splitList('  '), [])
})

test('only physical links with a permanent MAC can be picked, and each says what it is', () => {
  const links = [
    link({ name: 'eth0', permanentMac: 'aa:bb:cc:dd:ee:01', speedMbit: 1000 }),
    link({ name: 'eth1', permanentMac: 'aa:bb:cc:dd:ee:02', speedMbit: 2500, linkUp: false }),
    link({ name: 'bond0', kind: 'bond', permanentMac: '' }),
    link({ name: 'lo' }),
    link({ name: 'eth9', permanentMac: 'aa:bb:cc:dd:ee:99' }),
  ]
  const got = selectableLinks(links, ['AA:BB:CC:DD:EE:01', 'aa:bb:cc:dd:ee:02'])
  assert.deepEqual(
    got.map((l) => l.mac),
    ['aa:bb:cc:dd:ee:01', 'aa:bb:cc:dd:ee:02'],
  )
  assert.equal(linkLabel(links[0]!), 'eth0 - aa:bb:cc:dd:ee:01 - 1 Gbit/s')
  assert.equal(linkLabel(links[1]!), 'eth1 - aa:bb:cc:dd:ee:02 - 2.5 Gbit/s - no link')
  assert.equal(linkLabel(link({ name: 'eth2', permanentMac: 'aa:bb:cc:dd:ee:03' })), 'eth2 - aa:bb:cc:dd:ee:03 - speed unknown')
  // With no identity MACs to compare against, every physical link stays.
  assert.equal(selectableLinks(links, []).length, 3)
})

const form = (f: Partial<NetworkForm>): NetworkForm => ({
  mac: 'aa:bb:cc:dd:ee:01',
  addressing: 'dhcp',
  addresses: '',
  gateway: '',
  dns: '',
  vlan: '',
  ...f,
})

test('no NIC chosen means no network settings, and nothing else is checked', () => {
  const c = networkChoice(form({ mac: '', addressing: 'static', addresses: 'junk' }))
  assert.equal(c.network, undefined)
  assert.equal(c.ok, true)
})

test('DHCP sends only the link, the DNS servers and the VLAN', () => {
  const c = networkChoice(form({ dns: '1.1.1.1, 9.9.9.9', vlan: '30', addresses: '10.0.0.2/24', gateway: '10.0.0.1' }))
  assert.equal(c.ok, true)
  assert.deepEqual(c.network, {
    mac: 'aa:bb:cc:dd:ee:01',
    addressing: 'dhcp',
    addresses: [],
    gateway: '',
    dnsServers: ['1.1.1.1', '9.9.9.9'],
    vlanId: 30,
  })
})

test('static addressing needs a valid address and checks the rest', () => {
  const ok = networkChoice(
    form({ addressing: 'static', addresses: '192.168.1.10/24', gateway: '192.168.1.1', dns: '1.1.1.1' }),
  )
  assert.equal(ok.ok, true)
  assert.deepEqual(ok.network?.addresses, ['192.168.1.10/24'])
  assert.equal(ok.network?.gateway, '192.168.1.1')

  assert.equal(networkChoice(form({ addressing: 'static' })).errors.addresses, 'network.errAddressRequired')
  assert.equal(networkChoice(form({ addressing: 'static', addresses: '192.168.1.10' })).errors.addresses, 'network.errAddress')
  assert.equal(
    networkChoice(form({ addressing: 'static', addresses: '10.0.0.2/24', gateway: 'gw' })).errors.gateway,
    'network.errGateway',
  )
  assert.equal(networkChoice(form({ dns: 'dns.example.com' })).errors.dns, 'network.errDns')
  assert.equal(networkChoice(form({ vlan: '0' })).errors.vlan, 'network.errVlan')
  assert.equal(networkChoice(form({ vlan: '4095' })).errors.vlan, 'network.errVlan')
  assert.equal(networkChoice(form({ vlan: '12x' })).errors.vlan, 'network.errVlan')
  assert.equal(networkChoice(form({ mac: 'eth0' })).errors.mac, 'network.errMac')
  assert.equal(networkChoice(form({ addressing: 'static', addresses: '10.0.0.2/24', gateway: 'gw' })).ok, false)
})

test('disk roles: ephemeral and user volumes pick distinct disks from the node', () => {
  const a = disk({ wwid: 'a' })
  const b = disk({ wwid: 'b' })
  const c = disk({ serial: 'c' })

  const none = diskRolesChoice({ ephemeral: '', volumes: [] }, '')
  assert.deepEqual(none, { disks: [], errors: { volumes: {} }, ok: true })

  const good = diskRolesChoice(
    { ephemeral: diskKey(a), volumes: [{ key: diskKey(b), name: 'data', size: '200GiB' }, { key: diskKey(c), name: 'scratch', size: '' }] },
    '',
  )
  assert.equal(good.ok, true)
  assert.deepEqual(good.disks, [
    { role: 'ephemeral', disk: { wwid: 'a' }, name: '', maxSize: '' },
    { role: 'userVolume', disk: { wwid: 'b' }, name: 'data', maxSize: '200GiB' },
    { role: 'userVolume', disk: { serial: 'c' }, name: 'scratch', maxSize: '' },
  ])

  // The install disk cannot also be EPHEMERAL's own disk, and one disk gets one role.
  assert.equal(diskRolesChoice({ ephemeral: diskKey(a), volumes: [] }, diskKey(a)).errors.ephemeral, 'disks.errSameAsInstall')
  const dup = diskRolesChoice({ ephemeral: diskKey(a), volumes: [{ key: diskKey(a), name: 'x', size: '' }] }, '')
  assert.equal(dup.errors.volumes[0], 'disks.errTaken')
  assert.equal(dup.ok, false)
})

test('disk roles: volume names and sizes are checked, and a half-filled row is an error', () => {
  const a = disk({ wwid: 'a' })
  const row = (r: { key?: string; name?: string; size?: string }) =>
    diskRolesChoice({ ephemeral: '', volumes: [{ key: diskKey(a), name: 'data', size: '', ...r }] }, '')

  assert.equal(row({ name: 'my data' }).errors.volumes[0], 'disks.errName')
  assert.equal(row({ name: '' }).errors.volumes[0], 'disks.errName')
  assert.equal(row({ name: 'a'.repeat(33) }).errors.volumes[0], 'disks.errName')
  assert.equal(row({ size: 'lots' }).errors.volumes[0], 'disks.errSize')
  assert.equal(row({ size: '0GiB' }).errors.volumes[0], 'disks.errSize')
  assert.equal(row({ size: '2TiB' }).ok, true)
  assert.equal(row({ key: '' }).errors.volumes[0], 'disks.errDisk')

  const twice = diskRolesChoice(
    { ephemeral: '', volumes: [{ key: diskKey(a), name: 'd', size: '' }, { key: diskKey(disk({ wwid: 'z' })), name: 'd', size: '' }] },
    '',
  )
  assert.equal(twice.errors.volumes[1], 'disks.errNameTwice')
})

test('the NIC form starts on the first card with a link, and says so only when it was not picked', () => {
  const choices = selectableLinks(
    [
      link({ name: 'eth0', permanentMac: 'aa:bb:cc:dd:ee:01', linkUp: false }),
      link({ name: 'eth1', permanentMac: 'aa:bb:cc:dd:ee:02', linkUp: true }),
      link({ name: 'eth2', permanentMac: 'aa:bb:cc:dd:ee:03', linkUp: true }),
    ],
    [],
  )
  assert.equal(defaultLink(choices)?.mac, 'aa:bb:cc:dd:ee:02')
  assert.equal(defaultLink(choices.map((c) => ({ ...c, up: false }))), undefined, 'no card has a link: the person picks')
  assert.equal(defaultLink([]), undefined)
})
