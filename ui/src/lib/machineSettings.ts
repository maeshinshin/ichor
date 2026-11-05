import type { Key } from './i18n.ts'
import type { DiskSelector } from './installDisk.ts'
import { formatSpeed } from './talosDetails.ts'

// The checks below repeat the CRD's rules (TalosMachine.spec.network and spec.disks) and
// the backend's, so a form says what is wrong before it is sent.

const macRe = /^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$/
const volumeNameRe = /^[A-Za-z0-9-]{1,32}$/
const sizeRe = /^[1-9][0-9]*(MiB|GiB|TiB)$/
const ipv4Re = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

export const isValidMac = (s: string) => macRe.test(s)

function isIpv6(s: string): boolean {
  if (!s.includes(':') || !/^[0-9a-fA-F:.]+$/.test(s)) return false
  try {
    return new URL(`http://[${s}]/`).hostname !== ''
  } catch {
    return false
  }
}

// isIp accepts an IPv4 or IPv6 address that Talos can use: the unspecified address is not one.
export function isIp(s: string): boolean {
  if (s === '0.0.0.0' || s === '::') return false
  return ipv4Re.test(s) || isIpv6(s)
}

// isCidr accepts an address with a prefix length, like 192.168.1.10/24.
export function isCidr(s: string): boolean {
  const [ip, len, ...rest] = s.split('/')
  if (ip === undefined || len === undefined || rest.length > 0 || !/^\d{1,3}$/.test(len) || !isIp(ip)) return false
  return Number(len) <= (ip.includes(':') ? 128 : 32) && Number(len) > 0
}

// splitList reads a list typed with commas, spaces or new lines between the items.
export const splitList = (s: string): string[] => s.split(/[\s,]+/).filter((x) => x !== '')

// NicOption is what the NIC picker reads from a link: a discovered NIC (Machine.discoveredLinks) or
// one the node reports now (TalosLink), which both have these fields.
export type NicOption = { name: string; permanentMac: string; linkUp: boolean; speedMbit: number; kind?: string }

export type LinkChoice = { mac: string; label: string; up: boolean }

export function linkLabel(l: NicOption): string {
  const speed = formatSpeed(l.speedMbit)
  return [l.name, l.permanentMac, speed === '-' ? 'speed unknown' : speed, ...(l.linkUp ? [] : ['no link'])].join(' - ')
}

// selectableLinks are the physical links that can be picked: virtual ones have no permanent MAC to
// match. When the machine's identity lists MACs, the link has to be one of them, since that is what
// the backend accepts.
export function selectableLinks(links: NicOption[], identityMacs: string[]): LinkChoice[] {
  const known = identityMacs.map((m) => m.toLowerCase())
  return links
    .filter((l) => (l.kind ?? '') === '' && l.permanentMac !== '')
    .filter((l) => known.length === 0 || known.includes(l.permanentMac.toLowerCase()))
    .map((l) => ({ mac: l.permanentMac.toLowerCase(), label: linkLabel(l), up: l.linkUp }))
}

// defaultLink is the NIC the form starts on: the first card with a link, in the order given. Undefined
// when no card has one, and the person chooses.
export function defaultLink(links: LinkChoice[]): LinkChoice | undefined {
  return links.find((l) => l.up)
}

export type NetworkForm = {
  mac: string
  addressing: 'dhcp' | 'static'
  addresses: string
  gateway: string
  dns: string
  vlan: string
}

export type NetworkInit = {
  mac: string
  addressing: string
  addresses: string[]
  gateway: string
  dnsServers: string[]
  vlanId: number
}

export type NetworkChoice = {
  network: NetworkInit | undefined
  errors: Partial<Record<'mac' | 'addresses' | 'gateway' | 'dns' | 'vlan', Key>>
  ok: boolean
}

// networkChoice turns the form into the network settings to send. No NIC chosen means none: the
// node keeps Talos's default (DHCP on every link), and the other fields are not looked at.
export function networkChoice(f: NetworkForm): NetworkChoice {
  const errors: NetworkChoice['errors'] = {}
  const mac = f.mac.trim()
  if (mac === '') return { network: undefined, errors, ok: true }
  if (!isValidMac(mac)) errors.mac = 'network.errMac'

  const static_ = f.addressing === 'static'
  const addresses = static_ ? splitList(f.addresses) : []
  const gateway = static_ ? f.gateway.trim() : ''
  const dns = splitList(f.dns)

  if (static_) {
    if (addresses.length === 0) errors.addresses = 'network.errAddressRequired'
    else if (!addresses.every(isCidr)) errors.addresses = 'network.errAddress'
    if (gateway !== '' && !isIp(gateway)) errors.gateway = 'network.errGateway'
  }
  if (!dns.every(isIp)) errors.dns = 'network.errDns'

  const vlanText = f.vlan.trim()
  const vlan = vlanText === '' ? 0 : Number(vlanText)
  if (vlanText !== '' && (!/^\d+$/.test(vlanText) || vlan < 1 || vlan > 4094)) errors.vlan = 'network.errVlan'

  return {
    network: { mac: mac.toLowerCase(), addressing: f.addressing, addresses, gateway, dnsServers: dns, vlanId: vlan },
    errors,
    ok: Object.keys(errors).length === 0,
  }
}

export type VolumeRow = { key: string; name: string; size: string }
export type DiskRolesForm = { ephemeral: string; volumes: VolumeRow[] }
export type DiskRoleInit = { role: 'ephemeral' | 'userVolume'; disk: DiskSelector; name: string; maxSize: string }
export type DiskRolesChoice = {
  disks: DiskRoleInit[]
  errors: { ephemeral?: Key; volumes: Record<number, Key> }
  ok: boolean
}

const selectorOf = (key: string) => JSON.parse(key) as DiskSelector

// diskRolesChoice turns the pickers into the disk roles to send. Keys are diskKey values from the
// node's own disk list; installKey is the chosen system disk, which cannot also be EPHEMERAL's own
// disk, and a disk takes one role only.
export function diskRolesChoice(f: DiskRolesForm, installKey: string): DiskRolesChoice {
  const errors: DiskRolesChoice['errors'] = { volumes: {} }
  const disks: DiskRoleInit[] = []
  const taken = new Set<string>()
  const names = new Set<string>()

  if (f.ephemeral !== '') {
    if (f.ephemeral === installKey) errors.ephemeral = 'disks.errSameAsInstall'
    taken.add(f.ephemeral)
    disks.push({ role: 'ephemeral', disk: selectorOf(f.ephemeral), name: '', maxSize: '' })
  }

  f.volumes.forEach((v, i) => {
    const name = v.name.trim()
    const size = v.size.trim()
    if (v.key === '') errors.volumes[i] = 'disks.errDisk'
    else if (!volumeNameRe.test(name)) errors.volumes[i] = 'disks.errName'
    else if (names.has(name)) errors.volumes[i] = 'disks.errNameTwice'
    else if (size !== '' && !sizeRe.test(size)) errors.volumes[i] = 'disks.errSize'
    else if (taken.has(v.key)) errors.volumes[i] = 'disks.errTaken'
    names.add(name)
    if (v.key !== '') {
      taken.add(v.key)
      disks.push({ role: 'userVolume', disk: selectorOf(v.key), name, maxSize: size })
    }
  })

  return { disks, errors, ok: errors.ephemeral === undefined && Object.keys(errors.volumes).length === 0 }
}
