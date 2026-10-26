// Pure helpers for the "Add node" page (docs/adr/0083). No import from "@/gen".

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

// Discovery is off when nothing is listed: the controller opens no connection.
export function discoveryOn(addresses: readonly string[], cidrs: readonly string[]): boolean {
  return addresses.length + cidrs.length > 0
}
