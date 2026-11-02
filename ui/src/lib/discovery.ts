// Pure helpers for the "Add node" page. No import from "@/gen".

export type AddressCheck = 'ok' | 'empty' | 'invalid' | 'duplicate'

const OCTET = /^(0|[1-9]\d{0,2})$/

// checkDiscoveryAddress is the same test the backend makes: one plain IPv4
// address (no range, no IPv6, no leading zeros), not already listed. The
// backend stays the judge; this only saves a round trip.
export function checkDiscoveryAddress(input: string, existing: readonly string[]): AddressCheck {
  const a = input.trim()
  if (a === '') return 'empty'
  const parts = a.split('.')
  if (parts.length !== 4 || !parts.every((p) => OCTET.test(p) && Number(p) <= 255)) return 'invalid'
  return existing.includes(a) ? 'duplicate' : 'ok'
}

export type CidrCheck = AddressCheck | 'tooLarge' | 'full'

export const MAX_DISCOVERY_CIDRS = 4

// maskCidr returns the canonical range for a.b.c.d/n (host bits cleared, as the
// backend stores it), or null when it is not an IPv4 CIDR with a prefix of 0-32.
export function maskCidr(input: string): string | null {
  const [ip, bits, ...rest] = input.trim().split('/')
  if (bits === undefined || rest.length > 0 || !/^(0|[1-9]\d?)$/.test(bits) || Number(bits) > 32) return null
  const parts = ip.split('.')
  if (parts.length !== 4 || !parts.every((p) => OCTET.test(p) && Number(p) <= 255)) return null
  const n = Number(bits)
  const mask = n === 0 ? 0 : (0xffffffff << (32 - n)) >>> 0
  const v = (parts.reduce((acc, p) => acc * 256 + Number(p), 0) & mask) >>> 0
  return `${v >>> 24}.${(v >>> 16) & 255}.${(v >>> 8) & 255}.${v & 255}/${n}`
}

// checkDiscoveryCidr is the backend's test for a range: IPv4, /24 or smaller, not already listed, and at most four ranges in all.
export function checkDiscoveryCidr(input: string, existing: readonly string[]): CidrCheck {
  if (input.trim() === '') return 'empty'
  const masked = maskCidr(input)
  if (masked === null) return 'invalid'
  if (Number(masked.split('/')[1]) < 24) return 'tooLarge'
  if (existing.includes(masked)) return 'duplicate'
  return existing.length >= MAX_DISCOVERY_CIDRS ? 'full' : 'ok'
}

export type DiscoveryTarget = { kind: 'address' | 'cidr'; check: CidrCheck }

// checkDiscoveryTarget takes the one input of the Add node page: an input with a
// slash is a range, anything else is a single address.
export function checkDiscoveryTarget(
  input: string,
  addresses: readonly string[],
  cidrs: readonly string[],
): DiscoveryTarget {
  return input.includes('/')
    ? { kind: 'cidr', check: checkDiscoveryCidr(input, cidrs) }
    : { kind: 'address', check: checkDiscoveryAddress(input, addresses) }
}

// Discovery is off when nothing is listed: the controller opens no connection.
export function discoveryOn(addresses: readonly string[], cidrs: readonly string[]): boolean {
  return addresses.length + cidrs.length > 0
}
